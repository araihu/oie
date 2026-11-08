// Package avatarfiles validates and stores static, content-addressed profile thumbnails.
package avatarfiles

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
)

type Store struct{ dir string }

func New(dir string) *Store { return &Store{dir: dir} }
func (s *Store) Put(ctx context.Context, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > 1<<20 {
		return "", application.ErrInvalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 4<<20 {
		return "", application.ErrInvalid
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return "", application.ErrInvalid
	}
	original, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", application.ErrInvalid
	}
	width, height := config.Width, config.Height
	if width > 256 || height > 256 {
		if width >= height {
			height = height * 256 / width
			width = 256
		} else {
			width = width * 256 / height
			height = 256
		}
		if width < 1 {
			width = 1
		}
		if height < 1 {
			height = 1
		}
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, width, height))
	bounds := original.Bounds()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			thumb.SetNRGBA(x, y, color.NRGBAModel.Convert(original.At(bounds.Min.X+x*config.Width/width, bounds.Min.Y+y*config.Height/height)).(color.NRGBA))
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, thumb); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded.Bytes())
	hash := hex.EncodeToString(sum[:])
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(s.dir, ".avatar-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	if err = file.Chmod(0600); err != nil {
		return "", err
	}
	if _, err = file.Write(encoded.Bytes()); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), filepath.Join(s.dir, hash+".png")); err != nil {
		return "", err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	if err = dir.Sync(); err != nil {
		return "", err
	}
	return hash, nil
}
func (s *Store) Read(ctx context.Context, hash string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !application.ValidImageHash(hash) {
		return nil, application.ErrInvalid
	}
	name := filepath.Join(s.dir, hash+".png")
	info, err := os.Lstat(name)
	if err == nil && !info.Mode().IsRegular() {
		return nil, application.ErrNotFound
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if errors.Is(err, os.ErrNotExist) {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, application.ErrNotFound
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, application.ErrNotFound
	}
	return data, nil
}
