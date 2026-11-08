// Package mediafiles stores bounded, validated images outside SQLite.
package mediafiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

const MaxOriginal = 32 << 20
const MaxPreview = 128 << 10

type Store struct{ dir string }

func New(dir string) *Store { return &Store{dir: dir} }
func imageType(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	}
	return ""
}
func decode(data []byte) (image.Image, string, error) {
	if len(data) == 0 || len(data) > MaxOriginal {
		return nil, "", application.PreviewFailure{Code: "too_large"}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || imageType(format) == "" {
		return nil, "", application.PreviewFailure{Code: "unsupported"}
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || cfg.Width*cfg.Height > 16<<20 {
		return nil, "", application.PreviewFailure{Code: "too_large"}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", application.PreviewFailure{Code: "unsupported"}
	}
	return img, imageType(format), nil
}
func (s *Store) Put(ctx context.Context, data []byte, original bool) (domain.MediaFile, error) {
	if err := ctx.Err(); err != nil {
		return domain.MediaFile{}, err
	}
	if !original && len(data) > MaxPreview {
		return domain.MediaFile{}, application.PreviewFailure{Code: "too_large"}
	}
	_, mime, err := decode(data)
	if err != nil {
		return domain.MediaFile{}, err
	}
	return s.persist(ctx, data, mime)
}
func (s *Store) Thumbnail(ctx context.Context, data []byte) (domain.MediaFile, error) {
	if err := ctx.Err(); err != nil {
		return domain.MediaFile{}, err
	}
	source, _, err := decode(data)
	if err != nil {
		return domain.MediaFile{}, err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > 256 || height > 256 {
		if width >= height {
			height = height * 256 / width
			width = 256
		} else {
			width = width * 256 / height
			height = 256
		}
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			thumb.SetNRGBA(x, y, color.NRGBAModel.Convert(source.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height)).(color.NRGBA))
		}
	}
	var out bytes.Buffer
	if err = png.Encode(&out, thumb); err != nil {
		return domain.MediaFile{}, err
	}
	return s.persist(ctx, out.Bytes(), "image/png")
}
func (s *Store) persist(ctx context.Context, data []byte, mime string) (domain.MediaFile, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	result := domain.MediaFile{Hash: hash, MimeType: mime, Size: int64(len(data))}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return result, err
	}
	file, err := os.CreateTemp(s.dir, ".image-")
	if err != nil {
		return result, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(0600); err != nil {
		return result, err
	}
	if _, err = file.Write(data); err != nil {
		return result, err
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	if err = file.Close(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = os.Rename(file.Name(), filepath.Join(s.dir, hash)); err != nil {
		return result, err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return result, err
	}
	defer dir.Close()
	return result, dir.Sync()
}
func (s *Store) Read(ctx context.Context, file domain.MediaFile) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !application.ValidImageHash(file.Hash) {
		return nil, application.ErrInvalid
	}
	name := filepath.Join(s.dir, file.Hash)
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != file.Size || file.Size < 1 || file.Size > MaxOriginal {
		return nil, application.ErrNotFound
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxOriginal+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != file.Size || hex.EncodeToString(sum[:]) != file.Hash {
		return nil, application.ErrNotFound
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || imageType(format) != file.MimeType || config.Width > 8192 || config.Height > 8192 || config.Width*config.Height > 16<<20 {
		return nil, application.ErrNotFound
	}
	return data, nil
}
