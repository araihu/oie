//go:build linux || darwin

package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPortalMediaSelectionReadsAndReleasesChosenFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "áudio.mp3")
	payload := bytes.Repeat([]byte{192}, portalChunkSize+17)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	var m portalMedia
	first, err := m.open([]string{path}, 1)
	if err != nil || first.ID == "" || len(first.Files) != 1 || first.Files[0].Name != "áudio.mp3" || first.Files[0].Size != int64(len(payload)) {
		t.Fatalf("selection: %+v, %v", first, err)
	}
	t.Cleanup(func() { m.release(first.ID) })
	if err := os.Rename(path, path+".moved"); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{0, portalChunkSize} {
		chunk, err := m.read(first.ID, 0, int64(offset))
		if err != nil || !bytes.Equal(chunk, payload[offset:min(offset+portalChunkSize, len(payload))]) {
			t.Fatalf("incorrect chunk at %d: %v", offset, err)
		}
	}
	if _, err := m.read("another-selection", 0, 0); err == nil {
		t.Fatal("read outside the selection")
	}
	second, err := m.open([]string{path + ".moved"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	m.release(first.ID)
	if _, err := m.read(first.ID, 0, 0); err == nil {
		t.Fatal("old selection remained readable")
	}
	if _, err := m.read(second.ID, 0, 0); err != nil {
		t.Fatal("stale release closed the new selection", err)
	}
	m.release(second.ID)
	if _, err := m.read(second.ID, 0, 0); err == nil {
		t.Fatal("read after release")
	}
	if limited, err := m.open([]string{path, path}, 1); err != nil || !limited.Limit || limited.ID != "" {
		t.Fatalf("count limit: %+v, %v", limited, err)
	}
}

func TestPortalMediaRefusesNamedPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selection.mp3")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	var m portalMedia
	if selection, err := m.open([]string{path}, 1); err == nil || selection.ID != "" {
		t.Fatal("named pipe accepted")
	}
}
