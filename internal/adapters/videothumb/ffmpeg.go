// Package videothumb decodes bounded local samples. FFmpeg never sees remote URLs.
package videothumb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
)

const (
	PrefixLimit int64 = 4 << 20
	SuffixLimit int64 = 1 << 20
	OutputLimit       = 1 << 20
)

type Decoder struct{ binary string }

func New(binary string) *Decoder {
	path, err := exec.LookPath(binary)
	if err != nil {
		return &Decoder{}
	}
	return &Decoder{binary: path}
}
func (d *Decoder) Available() bool { return d != nil && d.binary != "" }
func failure(code string) error    { return application.PreviewFailure{Code: code} }

func (d *Decoder) Thumbnail(ctx context.Context, source application.VideoThumbnailSource) ([]byte, error) {
	if !d.Available() {
		return nil, failure("decoder_unavailable")
	}
	first, err := source(ctx, application.MediaReadRequest{Range: &application.ByteRange{Start: 0, End: PrefixLimit - 1}})
	if err != nil {
		return nil, err
	}
	if first.Body == nil {
		return nil, failure("frame_unavailable")
	}
	defer func() { _ = first.Body.Close() }()
	if first.Total <= 0 || first.Total > application.MaxMediaSource || first.Offset != 0 || !application.VideoMediaType(first.MediaType) {
		return nil, failure("frame_unavailable")
	}
	input, err := os.CreateTemp("", "oie-video-sample-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Close(); _ = os.Remove(input.Name()) }()
	if err = input.Truncate(first.Total); err != nil {
		return nil, err
	}
	amount := min(PrefixLimit, first.Total)
	_, err = io.CopyN(input, first.Body, amount)
	_ = first.Body.Close()
	if err != nil {
		return nil, err
	}
	output, decodeErr := d.frame(ctx, input.Name(), first.MediaType)
	if decodeErr == nil {
		return output, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if first.Total <= amount {
		return nil, decodeErr
	}
	// A suffix may belong to a different file after a URL is overwritten. Require
	// a validator before combining disjoint ranges into one sparse sample.
	condition := application.MediaCondition{}
	if first.ETag != "" && !strings.HasPrefix(first.ETag, "W/") {
		condition.ETag = first.ETag
	} else if !first.ModifiedAt.IsZero() {
		condition.ModifiedAt = first.ModifiedAt
	} else {
		return nil, failure("frame_unavailable")
	}
	start := max(amount, first.Total-SuffixLimit)
	last, err := source(ctx, application.MediaReadRequest{Range: &application.ByteRange{Start: start, End: first.Total - 1}, Condition: condition})
	if err != nil {
		return nil, err
	}
	if last.Body == nil {
		return nil, failure("frame_unavailable")
	}
	defer func() { _ = last.Body.Close() }()
	if !last.Partial || last.Total != first.Total || last.Offset != start || last.Length != first.Total-start || last.MediaType != first.MediaType {
		return nil, failure("thumbnail_budget_exceeded")
	}
	if condition.ETag != "" && last.ETag != condition.ETag || !condition.ModifiedAt.IsZero() && !last.ModifiedAt.Equal(condition.ModifiedAt) {
		return nil, failure("frame_unavailable")
	}
	if _, err = input.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err = io.CopyN(input, last.Body, last.Length); err != nil {
		return nil, err
	}
	_ = last.Body.Close()
	return d.frame(ctx, input.Name(), first.MediaType)
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > OutputLimit-b.Len() {
		return 0, io.ErrShortWrite
	}
	return b.Buffer.Write(p)
}

func (d *Decoder) frame(parent context.Context, path, mime string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	format := "mov"
	switch mime {
	case "video/webm":
		format = "matroska"
	case "video/ogg":
		format = "ogg"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-max_alloc", "67108864", "-threads", "1", "-filter_threads", "1", "-probesize", "1048576", "-analyzeduration", "1000000", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,webm,ogg", "-f", format}
	if format == "mov" {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	// Output codec options are separate from the input decoder thread limit.
	args = append(args, "-i", path, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", "thumbnail=12,scale=w='min(640,iw)':h=-2", "-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "-threads", "1", "pipe:1")
	command := exec.CommandContext(ctx, d.binary, args...)
	var output limitedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, failure("thumbnail_timeout")
		}
		return nil, failure("frame_unavailable")
	}
	if output.Len() == 0 {
		return nil, failure("frame_unavailable")
	}
	return output.Bytes(), nil
}
