package sqlite

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

const maxBackupManifestSize = 32 << 20

type BackupSummary struct {
	Version       int       `json:"version"`
	IncludesMedia bool      `json:"includesMedia"`
	SourceEngine  domain.ID `json:"sourceEngine"`
	CreatedAt     time.Time `json:"createdAt"`
	Accounts      int64     `json:"accounts"`
	Conversations int64     `json:"conversations"`
	Messages      int64     `json:"messages"`
	PendingSends  int64     `json:"pendingSends"`
	MediaFiles    int       `json:"mediaFiles"`
}

// VerifyBackup checks the archived data independently of an engine profile.
func VerifyBackup(ctx context.Context, filename string) (summary BackupSummary, err error) {
	scratch, err := os.MkdirTemp("", "oie-verify-backup-")
	if err != nil {
		return summary, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	manifest, err := readBackup(ctx, filename, scratch)
	if err != nil {
		return summary, err
	}
	filename = filepath.Join(scratch, backupDatabaseName)
	if err := checkRestoreDatabase(ctx, filename); err != nil {
		return summary, err
	}
	db, err := openBackupDatabase(filename, "ro")
	if err != nil {
		return summary, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	q := dbgen.New(db)
	counts, err := q.SummarizeBackup(ctx)
	if err != nil {
		return summary, err
	}
	paused := int64(0)
	var hasRecovered bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='recovered_sends' AND type='table')").Scan(&hasRecovered); err != nil {
		return summary, err
	}
	if hasRecovered {
		paused, err = q.CountRecoveredSends(ctx)
		if err != nil {
			return summary, err
		}
	}
	return BackupSummary{
		Version: manifest.Version, IncludesMedia: manifest.IncludesMedia,
		SourceEngine: manifest.SourceEngine, CreatedAt: manifest.CreatedAt,
		Accounts: counts.Accounts, Conversations: counts.Conversations,
		Messages: counts.Messages, PendingSends: counts.PendingSends + paused,
		MediaFiles: len(manifest.Files) - 1,
	}, nil
}

// readBackup verifies the archive completely before handing staged files to a
// restore operation. The caller owns the scratch directory and its cleanup.
func readBackup(ctx context.Context, filename, scratch string) (backupManifest, error) {
	var manifest backupManifest
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return manifest, err
	}
	defer func() { _ = archive.Close() }()
	entries := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		if entries[file.Name] != nil || !file.Mode().IsRegular() {
			return manifest, errors.New("invalid or duplicate backup entry")
		}
		entries[file.Name] = file
	}
	header := entries[backupManifestName]
	if header == nil || header.UncompressedSize64 > maxBackupManifestSize {
		return manifest, errors.New("invalid backup manifest")
	}
	if err := decodeBackupManifest(header, &manifest); err != nil {
		return manifest, err
	}
	delete(entries, backupManifestName)
	if len(entries) != len(manifest.Files) || entries[backupDatabaseName] == nil {
		return manifest, errors.New("backup entries do not match the manifest")
	}
	for _, file := range manifest.Files {
		part := entries[file.Name]
		if part == nil || file.Size < 0 || (file.Name == backupDatabaseName && file.Size == 0) || file.Size == math.MaxInt64 ||
			part.UncompressedSize64 != uint64(file.Size) || !application.ValidImageHash(file.SHA256) ||
			!supportedBackupPath(file, manifest.IncludesMedia) {
			return manifest, errors.New("invalid backup file metadata")
		}
		if err := extractBackupFile(ctx, part, file, scratch); err != nil {
			return manifest, err
		}
		delete(entries, file.Name)
	}
	if err := verifyBackupMediaReferences(ctx, scratch, manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func decodeBackupManifest(file *zip.File, manifest *backupManifest) error {
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	decoder := json.NewDecoder(io.LimitReader(reader, maxBackupManifestSize+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(manifest); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing backup manifest data")
	}
	if _, err := domain.ParseID(string(manifest.SourceEngine)); err != nil || manifest.Version != backupFormat || manifest.CreatedAt.IsZero() {
		return errors.New("unsupported or invalid backup manifest")
	}
	return nil
}

func supportedBackupPath(file backupFile, media bool) bool {
	if file.Name == backupDatabaseName {
		return true
	}
	parts := strings.Split(file.Name, "/")
	return media && len(parts) == 3 && parts[0] == "media" &&
		validBackupMediaName(parts[1], parts[2], file.SHA256)
}

func extractBackupFile(ctx context.Context, part *zip.File, file backupFile, scratch string) (err error) {
	name := filepath.Join(scratch, filepath.FromSlash(file.Name))
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	in, err := part.Open()
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, digest), io.LimitReader(backupReader{ctx, in}, file.Size+1))
	if err != nil {
		return err
	}
	if size != file.Size || hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("backup entry %q failed integrity verification", file.Name)
	}
	return nil
}

func verifyBackupMediaReferences(ctx context.Context, scratch string, manifest backupManifest) (err error) {
	db, err := openBackupDatabase(filepath.Join(scratch, backupDatabaseName), "ro")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	q := dbgen.New(db)
	snapshot, err := q.Snapshot(ctx)
	if err != nil || snapshot.ID != string(manifest.SourceEngine) {
		return errors.New("backup database identity does not match the manifest")
	}
	if !manifest.IncludesMedia {
		return nil
	}
	files, err := q.ListBackupMedia(ctx)
	if err != nil {
		return err
	}
	expected := make(map[string]dbgen.ListBackupMediaRow, len(files))
	for _, file := range files {
		expected["media/"+file.Subdir+"/"+file.Filename] = file
	}
	for _, file := range manifest.Files {
		if file.Name == backupDatabaseName {
			continue
		}
		row, ok := expected[file.Name]
		if !ok || row.Hash != file.SHA256 || (row.Size >= 0 && row.Size != file.Size) {
			return fmt.Errorf("backup entry %q has no matching database reference", file.Name)
		}
	}
	return nil
}
