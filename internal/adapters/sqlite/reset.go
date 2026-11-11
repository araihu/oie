package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/domain"
)

const resetMarker = ".reset-pending"

// Reset replaces engine-owned data while retaining the directory lock.
// The caller must stop all users of the store before calling Reset.
func (s *Store) Reset(ctx context.Context, id domain.ID) error {
	marker, err := os.OpenFile(filepath.Join(s.dir, resetMarker), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = marker.Close(); err != nil {
		return err
	}
	err = s.db.Close()
	s.db, s.queries = nil, nil
	if err != nil {
		return err
	}
	if err = s.removeData(); err != nil {
		return err
	}
	return s.openDatabase(ctx, id)
}

func (s *Store) removeData() error {
	for _, name := range []string{"oie.sqlite", "oie.sqlite-wal", "oie.sqlite-shm", "oie.sqlite-journal", "media", "private"} {
		if err := os.RemoveAll(filepath.Join(s.dir, name)); err != nil {
			return err
		}
	}
	err := os.Remove(filepath.Join(s.dir, resetMarker))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
