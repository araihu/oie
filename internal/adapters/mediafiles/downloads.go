package mediafiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	if err := os.Rename(f.Name(), filepath.Join(dirName, hash)); err != nil {
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
	digest := sha256.New()
	if _, err := io.Copy(digest, downloadReader{ctx: ctx, src: file}); err != nil {
		_ = file.Close()
		return nil, err
	}
	if hex.EncodeToString(digest.Sum(nil)) != download.Hash {
		_ = file.Close()
		return nil, application.ErrNotFound
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

type downloadReader struct {
	ctx context.Context
	src io.Reader
}

func (r downloadReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(p)
}

// Reconcile runs before the engine accepts transfers. Only database references
// establish which published originals survived a previous process.
func (d *Downloads) Reconcile(ctx context.Context, hashes []string) error {
	entries, err := os.ReadDir(d.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	retained := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		retained[hash] = struct{}{}
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if _, keep := retained[name]; keep || entry.IsDir() {
			continue
		}
		if strings.HasPrefix(name, ".part-") || application.ValidImageHash(name) {
			if err := removeFile(filepath.Join(d.dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
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
