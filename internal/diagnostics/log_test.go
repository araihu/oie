package diagnostics

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsRejectPrivateDataAtBothLevels(t *testing.T) {
	private := []string{
		"message-body-sentinel", "draft-sentinel", "search-sentinel",
		"private-user@example.invalid", "private-room@conference.example.invalid",
		"password-sentinel", "cookie-sentinel", "bearer-sentinel", "203.0.113.27",
		"https://media.example.invalid/file.pdf?token=private-query",
		"/home/private-user/secret-file.pdf", "019a5df0-0000-7000-8000-000000000001",
		"fedcba98765432100123456789abcdef",
	}
	for _, debug := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "logs")
		var stderr bytes.Buffer
		logger := New(dir, &stderr, Hosted, debug)
		payload := strings.Join(private, " ")
		logger.Record(Event(payload), 0, 0)
		logger.StandardLogger().Print(payload)
		logger.Record(SendUnknown, 200*time.Millisecond, 1)
		logger.Record(SendDeferred, 0, 1)

		logger.RecordHTTP(EngineHTTPError, 0, 503, private[len(private)-1])

		file := filepath.Join(dir, fileName(0))
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, _, err := logger.Snapshot(168)
		if err != nil || !bytes.Equal(snapshot, raw) || !bytes.Equal(raw, stderr.Bytes()) {
			t.Fatalf("log sinks disagree: %v", err)
		}
		if !ValidSnapshot(string(snapshot)) || !bytes.Contains(snapshot, []byte(SendUnknown)) ||
			!bytes.Contains(snapshot, []byte(EngineHTTPError)) || !bytes.Contains(snapshot, []byte(RuntimeWarning)) {
			t.Fatal("expected safe diagnostic events")
		}
		if bytes.Contains(snapshot, []byte(SendDeferred)) != debug {
			t.Fatal("debug event did not honor the requested level")
		}
		for _, secret := range private {
			for _, sink := range [][]byte{raw, stderr.Bytes(), snapshot} {
				if bytes.Contains(sink, []byte(secret)) {
					t.Fatal("private fixture data reached a diagnostic sink")
				}
			}
		}

		// Externally altered files must never bypass the record schema.
		corrupt := append(bytes.Clone(raw), []byte("{\"message\":\""+private[0]+"\"}\n")...)
		first, _, _ := bytes.Cut(raw, []byte("\n"))
		corrupt = append(corrupt, bytes.Replace(first, []byte("\"hosted\""), []byte("\""+private[6]+"\""), 1)...)
		if err := os.WriteFile(file, corrupt, 0600); err != nil {
			t.Fatal(err)
		}
		read, _, err := logger.Snapshot(24)
		if err != nil || !bytes.Equal(read, raw) || ValidSnapshot(string(corrupt)) {
			t.Fatalf("snapshot admitted modified records: %v", err)
		}
	}
}

func TestDiagnosticsWindowLimitRotationAndExpiry(t *testing.T) {
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(15 * time.Hour)
	dir := filepath.Join(t.TempDir(), "logs")
	logger := New(dir, nil, Local, false)
	now := base.Add(-12 * time.Hour)
	logger.now = func() time.Time { return now }
	for range MaxRecords + 2 {
		logger.Record(SendFailed, 0, 0)
		now = now.Add(time.Minute)
	}
	now = base
	logger.Record(Ready, 0, 0)
	recent, truncated, err := logger.Snapshot(1)
	if err != nil || truncated || bytes.Count(recent, []byte("\n")) != 1 || !bytes.Contains(recent, []byte(Ready)) {
		t.Fatalf("unexpected one-hour snapshot %q (truncated=%v): %v", recent, truncated, err)
	}
	day, truncated, err := logger.Snapshot(24)
	if err != nil || !truncated || bytes.Count(day, []byte("\n")) != MaxRecords || !ValidSnapshot(string(day)) {
		t.Fatalf("unexpected bounded snapshot (truncated=%v): %v", truncated, err)
	}
	current := filepath.Join(dir, fileName(0))
	if err := os.Truncate(current, fileLimit); err != nil {
		t.Fatal(err)
	}
	logger.Record(WorkerFailed, 0, 0)
	if rotated, err := os.Stat(filepath.Join(dir, fileName(1))); err != nil || rotated.Size() != fileLimit {
		t.Fatalf("expected rotated file: %v", err)
	}
	info, err := os.Stat(current)
	if err != nil || info.Size() >= fileLimit || info.Mode().Perm() != 0600 {
		t.Fatalf("expected small private current file: %v", err)
	}
	expired := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, fileName(1)), expired, expired); err != nil {
		t.Fatal(err)
	}
	now = time.Now()
	logger.Record(Stopped, 0, 0)
	if _, err := os.Stat(filepath.Join(dir, fileName(1))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired file was not pruned: %v", err)
	}
}
