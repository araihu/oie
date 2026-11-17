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
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
)

func (s *Store) restoreMedia(ctx context.Context, tx *sql.Tx, scratch string, files []backupFile) ([]string, error) {
	if _, err := tx.ExecContext(ctx, restoreScript("media_metadata"),
		sql.Named("media_files", jsonText(files)), sql.Named("now_ms", time.Now().UnixMilli())); err != nil {
		return nil, fmt.Errorf("restore media metadata: %w", err)
	}
	references, err := restoredMediaReferences(ctx, tx, files)
	if err != nil {
		return nil, err
	}
	needed, err := s.preflightRestoreMedia(ctx, files, references)
	if err != nil {
		return nil, err
	}
	if err := s.writeRestoreMediaJournal(needed); err != nil {
		return nil, fmt.Errorf("record pending restore media: %w", err)
	}
	var published []string
	for _, file := range needed {
		name := filepath.FromSlash(file.Name)
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

func (s *Store) preflightRestoreMedia(ctx context.Context, files []backupFile, references map[string]string) ([]backupFile, error) {
	var needed []backupFile
	for _, file := range files {
		if references[file.Name] != file.SHA256 {
			continue
		}
		name := filepath.FromSlash(file.Name)
		for _, dir := range []string{filepath.Join(s.dir, "media"), filepath.Join(s.dir, filepath.Dir(name))} {
			if err := ensureBackupDirectory(dir); err != nil {
				return nil, err
			}
		}
		target := filepath.Join(s.dir, name)
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			needed = append(needed, file)
		} else if err != nil {
			return nil, err
		} else if err := checkRestoredMedia(ctx, target, file); err != nil {
			return nil, err
		}
	}
	return needed, nil
}

func restoredMediaReferences(ctx context.Context, db dbgen.DBTX, files []backupFile) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, restoreScript("media_references"), sql.Named("media_files", jsonText(files)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	references := make(map[string]string)
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			return nil, err
		}
		references[name] = hash
	}
	return references, rows.Err()
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
	temp, err := os.OpenFile(restoreMediaStagingPath(target), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
		if err := syncRestoreMediaDirectory(dir); err != nil {
			return true, err
		}
	}
	return true, nil
}

func checkRestoredMedia(ctx context.Context, name string, manifest backupFile) error {
	match, err := restoredMediaMatches(ctx, name, manifest)
	if err != nil {
		return err
	}
	if !match {
		return fmt.Errorf("destination media %q does not match the backup", manifest.Name)
	}
	return nil
}

func restoredMediaMatches(ctx context.Context, name string, manifest backupFile) (bool, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != manifest.Size {
		return false, nil
	}
	file, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err = io.Copy(digest, backupReader{ctx, file}); err != nil {
		return false, err
	}
	return hex.EncodeToString(digest.Sum(nil)) == manifest.SHA256, nil
}
