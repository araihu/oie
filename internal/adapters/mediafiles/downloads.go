package mediafiles

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type Downloads struct{ dir string }

func NewDownloads(dir string) *Downloads { return &Downloads{dir: dir} }

type stagedDownload struct{ *os.File }

func (f stagedDownload) Close() error {
	return errors.Join(f.File.Close(), removeFile(f.Name()))
}

func (f stagedDownload) Publish(hash string) error {
	if !application.ValidImageHash(hash) {
		return application.ErrInvalid
	}
	if err := f.Sync(); err != nil {
		return err
	}
	dirName := filepath.Dir(f.Name())
	err := os.Link(f.Name(), filepath.Join(dirName, hash))
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	dir, err := os.Open(dirName)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func (d *Downloads) Stage(ctx context.Context) (application.DownloadStage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(d.dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(d.dir, ".part-")
	if err != nil {
		return nil, err
	}
	return stagedDownload{file}, nil
}

func (d *Downloads) Open(ctx context.Context, download domain.Download) (io.ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !application.ValidImageHash(download.Hash) {
		return nil, application.ErrInvalid
	}
	file, err := os.Open(filepath.Join(d.dir, download.Hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != download.Size {
		_ = file.Close()
		return nil, application.ErrNotFound
	}
	return file, nil
}

func (d *Downloads) Remove(hash string) error {
	return removeOriginal(d.dir, hash)
}

func (s *Store) Remove(hash string) error {
	return removeOriginal(s.dir, hash)
}

func removeOriginal(dir, hash string) error {
	if !application.ValidImageHash(hash) {
		return application.ErrInvalid
	}
	return removeFile(filepath.Join(dir, hash))
}

func removeFile(path string) error {
	if err := os.Remove(path); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
