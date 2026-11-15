package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const (
	fileLimit        = 5 << 20
	fileCount        = 3
	retention        = 7 * 24 * time.Hour
	MaxRecords       = 500
	MaxSnapshotBytes = MaxRecords * 512
)

var errUnsafeFile = errors.New("unsafe diagnostics storage")

func fileName(index int) string {
	if index == 0 {
		return "oie-diagnostics.jsonl"
	}
	return fmt.Sprintf("oie-diagnostics.%d.jsonl", index)
}

// Use an advisory lock across processes that share a profile so a second launch
// cannot race rotation. Keep simultaneous readers out of a partially written line.
func (l *Logger) withFiles(write bool, action func(*os.Root) error) (err error) {
	if write {
		if err := os.MkdirAll(l.dir, 0700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(l.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errUnsafeFile
	}
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	const lockName = ".lock"
	if _, err := privateFile(root, lockName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lock := flock.New(filepath.Join(l.dir, lockName), flock.SetPermissions(0600))
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 5*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("diagnostic files busy")
	}
	defer func() { _ = lock.Unlock() }()
	return action(root)
}

func privateFile(root *os.Root, name string) (os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errUnsafeFile
	}
	return info, nil
}

func (l *Logger) appendFile(now time.Time, line []byte) error {
	return l.withFiles(true, func(root *os.Root) error {
		var size int64
		var priorDay bool
		for i := range fileCount {
			name := fileName(i)
			info, err := privateFile(root, name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if info.ModTime().Before(now.Add(-retention)) {
				if err := root.Remove(name); err != nil {
					return err
				}
			} else if i == 0 {
				size = info.Size()
				priorDay = info.ModTime().UTC().Truncate(24 * time.Hour).Before(now.Truncate(24 * time.Hour))
			}
		}
		if size > 0 && (priorDay || size+int64(len(line)) > fileLimit) {
			if err := rotate(root); err != nil {
				return err
			}
		}
		file, err := root.OpenFile(fileName(0), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = file.Write(line)
		return errors.Join(err, file.Close())
	})
}

func rotate(root *os.Root) error {
	if err := root.Remove(fileName(fileCount - 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := fileCount - 2; i >= 0; i-- {
		if err := root.Rename(fileName(i), fileName(i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (r record) valid() bool {
	meta, ok := events[r.Event]
	return ok && r.Level == meta.level && r.Component == meta.component &&
		r.Mode.valid() && platformValid(r.Platform) && !r.Time.IsZero() &&
		r.Time.Location() == time.UTC &&
		(r.Build == "development" || versionPattern.MatchString(r.Build) || revisionPattern.MatchString(r.Build)) &&
		r.DurationMS >= 0 && r.DurationMS <= int64(24*time.Hour/time.Millisecond) &&
		r.Attempt >= 0 && r.Attempt <= 999 && (r.Status == 0 || (r.Status >= 400 && r.Status <= 599)) &&
		(r.RequestRef == "" || requestRefPattern.MatchString(r.RequestRef))
}

// Canonical comparison also rejects unknown fields, trailing data, duplicate
// fields, and arbitrary text from a damaged or externally modified file.
func parseLine(line []byte) (record, bool) {
	var r record
	if len(line) > 512 || json.Unmarshal(line, &r) != nil || !r.valid() {
		return r, false
	}
	canonical, err := json.Marshal(r)
	return r, err == nil && bytes.Equal(line, canonical)
}

// ValidSnapshot validates the exact bytes a desktop user has chosen to save.
func ValidSnapshot(value string) bool {
	if value == "" || len(value) > MaxSnapshotBytes || value[len(value)-1] != '\n' {
		return false
	}
	count := 0
	for line := range bytes.SplitSeq([]byte(value[:len(value)-1]), []byte{'\n'}) {
		count++
		if _, ok := parseLine(line); !ok || count > MaxRecords {
			return false
		}
	}
	return true
}

// Snapshot selects only complete, recent, schema-valid records. The browser
// receives at most 500 lines, and does not read logs until Diagnostics is opened.
func (l *Logger) Snapshot(hours int) ([]byte, bool, error) {
	if l.dir == "" {
		return nil, false, errUnsafeFile
	}
	now := l.now().UTC()
	cutoff := now.Add(-min(retention, time.Duration(hours)*time.Hour))
	type logFile struct {
		file *os.File
		size int64
	}
	var files []logFile
	defer func() {
		for _, log := range files {
			_ = log.file.Close()
		}
	}()
	// Hold rotation only while opening each file and capturing its size.
	// On Linux and macOS, these descriptors remain readable after rotation.
	err := l.withFiles(false, func(root *os.Root) error {
		for i := range fileCount {
			name := fileName(i)
			info, err := privateFile(root, name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Size() > fileLimit {
				continue
			}
			file, err := root.Open(name)
			if err != nil {
				return err
			}
			files = append(files, logFile{file, info.Size()})
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	var chunks [][]byte
	count := 0
	truncated := false
	// Scan the newest file first. Only use older files to fill the export
	// window or determine whether another matching record was omitted.
	for _, log := range files {
		lines := make([][]byte, MaxRecords-count+1)
		matches := 0
		reader := io.NewSectionReader(log.file, 0, log.size)
		err := scanLines(reader, cutoff, now, func(line []byte) {
			lines[matches%len(lines)] = bytes.Clone(line)
			matches++
		})
		if err != nil {
			return nil, false, err
		}
		var chunk bytes.Buffer
		keep := min(matches, MaxRecords-count)
		for i := matches - keep; i < matches; i++ {
			chunk.Write(lines[i%len(lines)])
			chunk.WriteByte('\n')
		}
		chunks = append(chunks, chunk.Bytes())
		count += keep
		if matches > keep {
			truncated = true
			break
		}
	}
	var snapshot bytes.Buffer
	for i := len(chunks) - 1; i >= 0; i-- {
		snapshot.Write(chunks[i])
	}
	return snapshot.Bytes(), truncated, nil
}

func scanLines(file io.Reader, cutoff, now time.Time, visit func([]byte)) error {
	reader := bufio.NewReaderSize(io.LimitReader(file, fileLimit), 1024)
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, io.EOF) {
			return nil // Never expose an incomplete line.
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = reader.ReadSlice('\n')
			}
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
		if err != nil {
			return err
		}
		r, ok := parseLine(line[:len(line)-1])
		if ok && !r.Time.Before(cutoff) && !r.Time.After(now) {
			visit(line[:len(line)-1])
		}
	}
}
