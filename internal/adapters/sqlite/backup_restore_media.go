package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func (s *Store) restoreMedia(ctx context.Context, tx *sql.Tx, scratch string, manifest backupManifest, hasDownloadCleanup, hasDownloadLineage, hasLegacyDownloads bool, now time.Time) ([]string, error) {
	files := manifest.Files
	if _, err := tx.ExecContext(ctx, restoreScript("media_download_devices")); err != nil {
		return nil, fmt.Errorf("read backup download devices: %w", err)
	}
	if hasDownloadCleanup {
		if _, err := tx.ExecContext(ctx, restoreScript("media_download_cleanup")); err != nil {
			return nil, fmt.Errorf("read backup download cleanup revisions: %w", err)
		}
	}
	if hasDownloadLineage {
		if _, err := tx.ExecContext(ctx, restoreScript("media_download_lineage")); err != nil {
			return nil, fmt.Errorf("read backup download membership and cleanup lineage: %w", err)
		}
	}
	if hasLegacyDownloads {
		if _, err := tx.ExecContext(ctx, restoreScript("media_download_legacy")); err != nil {
			return nil, fmt.Errorf("read legacy download memberships: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, restoreScript("media_metadata"),
		sql.Named("media_files", jsonText(files)), sql.Named("now_ms", now.UnixMilli()),
		sql.Named("source_engine", string(manifest.SourceEngine))); err != nil {
		return nil, fmt.Errorf("restore media metadata: %w", err)
	}
	if err := updateRestoredImageDownloads(ctx, tx, now); err != nil {
		return nil, fmt.Errorf("restore image downloads: %w", err)
	}
	if _, err := tx.ExecContext(ctx, restoreScript("media_downloads")); err != nil {
		return nil, fmt.Errorf("restore download memberships: %w", err)
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

func updateRestoredImageDownloads(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, restoreScript("media_preview_downloads"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var downloads []domain.MessagePreview
	for rows.Next() {
		var id domain.ID
		var payload, thumbnail, favicon string
		if err := rows.Scan(&id, &payload, &thumbnail, &favicon); err != nil {
			return err
		}
		var download domain.MessagePreview
		if err := json.Unmarshal([]byte(payload), &download); err != nil {
			return err
		}
		download.ID, download.ImageHash, download.FaviconHash = id, thumbnail, favicon
		downloads = append(downloads, download)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	q := dbgen.New(tx)
	for _, download := range downloads {
		preview, err := decodePreview(q.GetMessagePreview(ctx, string(download.ID)))
		if err != nil {
			return err
		}
		revision, err := uuid.NewV7()
		if err != nil {
			return err
		}
		parent := preview.RevisionID
		preview.RevisionID = domain.ID(revision.String())
		preview.UpdatedAt = now
		if preview.Kind == "link" {
			preview.Kind, preview.State = "image", "ready"
			preview.Title, preview.Description, preview.SiteName = download.Title, download.Description, download.SiteName
			preview.RefreshRequested = false
		}
		if preview.ImageHash == "" {
			preview.ImageHash = download.ImageHash
		}
		if preview.FaviconHash == "" {
			preview.FaviconHash = download.FaviconHash
		}
		if preview.DownloadState != "ready" {
			preview.DownloadState = "ready"
			preview.LastErrorCode = ""
			preview.FileHash, preview.FileMimeType, preview.FileSize = download.FileHash, download.FileMimeType, download.FileSize
			preview.VerifiedAlgorithm = download.VerifiedAlgorithm
		}
		if err := updatePreview(ctx, q, preview, parent); err != nil {
			return err
		}
	}
	return nil
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
