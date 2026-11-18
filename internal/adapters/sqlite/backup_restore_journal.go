package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/application"
)

const restoreMediaJournalName = "restore-media.pending"

func (s *Store) writeRestoreMediaJournal(files []backupFile) error {
	if len(files) == 0 {
		return nil
	}
	private := filepath.Join(s.dir, "private")
	if err := ensureBackupDirectory(private); err != nil {
		return err
	}
	journal := filepath.Join(private, restoreMediaJournalName)
	next, err := os.OpenFile(journal+".new", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(next.Name()) }()
	err = json.NewEncoder(next).Encode(files)
	if err == nil {
		err = next.Sync()
	}
	if err := errors.Join(err, next.Close()); err != nil {
		return err
	}
	// The journal must survive a crash before any media enters the profile.
	if err := os.Link(next.Name(), journal); err != nil {
		return err
	}
	if err := syncRestoreMediaDirectory(private); err != nil {
		return err
	}
	return syncRestoreMediaDirectory(s.dir)
}

func (s *Store) clearRestoreMediaJournal() error {
	journal := filepath.Join(s.dir, "private", restoreMediaJournalName)
	if err := os.Remove(journal); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return syncRestoreMediaDirectory(filepath.Dir(journal))
}

func (s *Store) recoverRestoreMedia(ctx context.Context) error {
	journal := filepath.Join(s.dir, "private", restoreMediaJournalName)
	info, err := os.Lstat(journal)
	if errors.Is(err, os.ErrNotExist) {
		// An interrupted journal write cannot have published any media.
		if _, err := os.Lstat(journal + ".new"); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return removeRestoreJournalStaging(journal)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("invalid interrupted restore media journal")
	}
	if err := removeRestoreJournalStaging(journal); err != nil {
		return err
	}
	files, err := readRestoreMediaJournal(journal)
	if err != nil {
		return err
	}
	references, err := restoredMediaReferences(ctx, s.readDB, files)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(s.dir, filepath.FromSlash(file.Name))
		if err := s.recoverRestoreMediaFile(ctx, target, file, references[file.Name] == file.SHA256); err != nil {
			return err
		}
	}
	return s.clearRestoreMediaJournal()
}

func removeRestoreJournalStaging(journal string) error {
	dir := filepath.Dir(journal)
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("invalid interrupted restore media journal directory")
	}
	if err := os.Remove(journal + ".new"); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return syncRestoreMediaDirectory(dir)
}

func readRestoreMediaJournal(name string) ([]backupFile, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, maxBackupManifestSize+1))
	decoder.DisallowUnknownFields()
	var files []backupFile
	if err := decoder.Decode(&files); err != nil {
		return nil, fmt.Errorf("read interrupted restore media journal: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid trailing restore media journal data")
	}
	for _, file := range files {
		if file.Name == backupDatabaseName || file.Size < 0 || !application.ValidImageHash(file.SHA256) || !supportedBackupPath(file, true) {
			return nil, errors.New("invalid file in interrupted restore media journal")
		}
	}
	return files, nil
}

func (s *Store) recoverRestoreMediaFile(ctx context.Context, target string, file backupFile, retained bool) error {
	parent := filepath.Dir(target)
	for _, dir := range []string{filepath.Join(s.dir, "media"), parent} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("restore media path %q is not a directory", dir)
		}
	}
	if err := os.Remove(restoreMediaStagingPath(target)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// A committed database reference owns the file. A different file may
	// have been placed at this location after the interrupted restore.
	if !retained {
		match, err := restoredMediaMatches(ctx, target, file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if match {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return syncRestoreMediaDirectory(parent)
}

func restoreMediaStagingPath(target string) string {
	return filepath.Join(filepath.Dir(target), ".restore-"+filepath.Base(target))
}

func syncRestoreMediaDirectory(name string) error {
	dir, err := os.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
