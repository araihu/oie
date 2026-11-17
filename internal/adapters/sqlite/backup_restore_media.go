package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
)

func (s *Store) restoreMedia(ctx context.Context, tx *sql.Tx, scratch string, files []backupFile) ([]string, error) {
	if _, err := tx.ExecContext(ctx, restoreScript("media_metadata")); err != nil {
		return nil, fmt.Errorf("restore media metadata: %w", err)
	}
	retained, err := dbgen.New(tx).ListBackupMedia(ctx)
	if err != nil {
		return nil, err
	}
	references := make(map[string]string, len(retained))
	for _, r := range retained {
		references["media/"+r.Subdir+"/"+r.Filename] = r.Hash
	}
	var published []string
	for _, file := range files {
		if references[file.Name] != file.SHA256 {
			continue
		}
		name := filepath.FromSlash(file.Name)
		parent := filepath.Dir(name)
		for _, dir := range []string{filepath.Join(s.dir, "media"), filepath.Join(s.dir, parent)} {
			if err := ensureBackupDirectory(dir); err != nil {
				return published, err
			}
		}
		target := filepath.Join(s.dir, name)
		created, err := publishRestoredMedia(ctx, filepath.Join(scratch, name), target, file)
		if created {
			published = append(published, target)
		}
		if err != nil {
			return published, err
		}
	}
	return published, nil
}

func publishRestoredMedia(ctx context.Context, source, target string, manifest backupFile) (bool, error) {
	if _, err := os.Lstat(target); err == nil {
		return false, checkRestoredMedia(ctx, target, manifest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	from, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer func() { _ = from.Close() }()
	temp, err := os.CreateTemp(filepath.Dir(target), ".restore-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	_, err = io.Copy(temp, backupReader{ctx, from})
	if err == nil {
		err = temp.Sync()
	}
	if err := errors.Join(err, temp.Close()); err != nil {
		return false, err
	}
	if err := os.Link(temp.Name(), target); err != nil {
		return false, err
	}
	for _, dir := range []string{filepath.Dir(target), filepath.Dir(filepath.Dir(target))} {
		parent, err := os.Open(dir)
		if err != nil {
			return true, err
		}
		if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
			return true, err
		}
	}
	return true, nil
}

func checkRestoredMedia(ctx context.Context, name string, manifest backupFile) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != manifest.Size {
		return fmt.Errorf("destination media %q does not match the backup", manifest.Name)
	}
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	_, err = io.Copy(digest, backupReader{ctx, file})
	if err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != manifest.SHA256 {
		return fmt.Errorf("destination media %q does not match the backup", manifest.Name)
	}
	return nil
}
