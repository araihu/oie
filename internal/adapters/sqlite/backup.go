package sqlite

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/gofrs/flock"
)

const (
	backupFormat       = 1
	backupManifestName = "manifest.json"
	backupDatabaseName = "oie.sqlite"
)

type backupManifest struct {
	Version       int          `json:"version"`
	CreatedAt     time.Time    `json:"createdAt"`
	SourceEngine  domain.ID    `json:"sourceEngine"`
	IncludesMedia bool         `json:"includesMedia"`
	Files         []backupFile `json:"files"`
}

type backupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// WriteProfileBackup exports a stopped profile without running migrations or
// starting protocol sessions. The profile lock keeps media from changing during export.
func WriteProfileBackup(ctx context.Context, dir string, dst io.Writer, includeMedia bool) (err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, backupDatabaseName)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("the selected data directory has no Oiê database")
	}
	lock := flock.New(filepath.Join(dir, "engine.lock"))
	locked, err := lock.TryLock()
	defer func() { err = errors.Join(err, lock.Close()) }()
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("engine data directory already in use")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	return writeBackup(ctx, db, dir, dst, includeMedia)
}

func writeBackup(ctx context.Context, source *sql.DB, dir string, dst io.Writer, includeMedia bool) error {
	private := filepath.Join(dir, "private")
	if err := ensureBackupDirectory(private); err != nil {
		return err
	}
	work, err := os.MkdirTemp(private, ".manual-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	database := filepath.Join(work, backupDatabaseName)
	engineID, err := copyBackupDatabase(ctx, source, database)
	if err != nil {
		return fmt.Errorf("snapshot engine database: %w", err)
	}
	manifest := backupManifest{
		Version: backupFormat, CreatedAt: time.Now().UTC(), SourceEngine: engineID,
		IncludesMedia: includeMedia,
	}
	archive := zip.NewWriter(dst)
	entry, err := appendBackupFile(ctx, archive, backupDatabaseName, database, "", -1)
	if err != nil {
		return err
	}
	manifest.Files = append(manifest.Files, entry)
	if includeMedia {
		files, err := appendBackupMedia(ctx, archive, database, dir)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, files...)
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(manifestData) > maxBackupManifestSize {
		return errors.New("backup manifest is too large")
	}
	entryWriter, err := archive.Create(backupManifestName)
	if err != nil {
		return err
	}
	if _, err := entryWriter.Write(manifestData); err != nil {
		return err
	}
	return archive.Close()
}

func copyBackupDatabase(ctx context.Context, source *sql.DB, filename string) (id domain.ID, err error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := copySQLiteBackup(ctx, source, filename); err != nil {
		return "", err
	}
	db, err := openBackupDatabase(filename, "rw")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	var version int
	var dirty bool
	versionErr := db.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty)
	if versionErr != nil || version < firstRestoreSchema {
		// Upgrade only the private snapshot. The selected profile remains as it was.
		if err := migrateDatabase(ctx, db, filepath.Dir(filename)); err != nil {
			return "", err
		}
	} else if dirty || version > newestRestoreSchema {
		return "", fmt.Errorf("unsupported backup database version %d", version)
	}
	q := dbgen.New(db)
	snapshot, err := q.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	if err := q.RemoveBackupRoomPasswords(ctx); err != nil {
		return "", err
	}
	// Rewrite the file so deleted passwords are absent from free pages as well.
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return "", err
	}
	return domain.ParseID(snapshot.ID)
}

func openBackupDatabase(filename, mode string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: filename}
	q := u.Query()
	q.Set("mode", mode)
	if mode == "rw" {
		q.Add("_pragma", "journal_mode(DELETE)")
	} else {
		q.Set("immutable", "1")
		q.Add("_pragma", "trusted_schema(0)")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func appendBackupMedia(ctx context.Context, archive *zip.Writer, database, dir string) (out []backupFile, err error) {
	db, err := openBackupDatabase(database, "ro")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	files, err := dbgen.New(db).ListBackupMedia(ctx)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if !application.ValidImageHash(file.Hash) || !validBackupMediaName(file.Subdir, file.Filename, file.Hash) {
			return nil, errors.New("invalid retained media filename")
		}
		name := path.Join("media", file.Subdir, file.Filename)
		entry, err := appendBackupFile(ctx, archive, name, filepath.Join(dir, filepath.FromSlash(name)), file.Hash, file.Size)
		if errors.Is(err, os.ErrNotExist) {
			// A retained reference can outlive the file, for example after
			// device download cleanup or a restore without media.
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

func validBackupMediaName(kind, name, hash string) bool {
	switch kind {
	case "avatars":
		return name == hash+".png"
	case "images", "downloads":
		return name == hash
	case "staged":
		_, err := domain.ParseID(name)
		return err == nil
	default:
		return false
	}
}

func appendBackupFile(ctx context.Context, archive *zip.Writer, name, filename, expectedHash string, expectedSize int64) (entry backupFile, err error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return entry, err
	}
	if !info.Mode().IsRegular() || (expectedSize >= 0 && info.Size() != expectedSize) {
		return entry, fmt.Errorf("invalid backup source %q", name)
	}
	file, err := os.Open(filename)
	if err != nil {
		return entry, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	part, err := archive.Create(name)
	if err != nil {
		return entry, err
	}
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(part, digest), backupReader{ctx, file})
	if err != nil {
		return entry, err
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	if size != info.Size() || (expectedHash != "" && hash != expectedHash) {
		return entry, fmt.Errorf("backup source %q failed integrity verification", name)
	}
	return backupFile{Name: name, Size: size, SHA256: hash}, nil
}

type backupReader struct {
	ctx  context.Context
	body io.Reader
}

func (r backupReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.body.Read(p)
}
