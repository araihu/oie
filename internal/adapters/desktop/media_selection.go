//go:build linux || darwin

package desktop

import (
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type portalMediaFile struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`

	file *os.File
}

type portalSelection struct {
	ID    string            `json:"id,omitempty"`
	Files []portalMediaFile `json:"files,omitempty"`
	Limit bool              `json:"limit,omitempty"`
}

const portalChunkSize = 1 << 20

// Only files opened by the user's current chooser are available through the
// bridge. The timer also releases descriptors if the WebView is reloaded.
type portalMedia struct {
	mu        sync.Mutex
	selection portalSelection
	timer     *time.Timer
}

func (m *portalMedia) open(paths []string, remaining int) (portalSelection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
	if len(paths) > remaining {
		return portalSelection{Limit: true}, nil
	}
	if len(paths) == 0 {
		return portalSelection{}, nil
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return portalSelection{}, err
	}
	for _, path := range paths {
		// Opening a FIFO selected in the portal must not block the next chooser.
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			m.closeLocked()
			return portalSelection{}, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = f.Close()
			m.closeLocked()
			return portalSelection{}, errors.New("cannot read selected media")
		}
		name := filepath.Base(path)
		m.selection.Files = append(m.selection.Files, portalMediaFile{
			Name: name, Type: mime.TypeByExtension(strings.ToLower(filepath.Ext(name))), Size: info.Size(), file: f,
		})
	}
	m.selection.ID = id.String()
	m.timer = time.AfterFunc(2*time.Minute, func() { m.release(id.String()) })
	return m.selection, nil
}

func (m *portalMedia) read(id string, index int, offset int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || m.selection.ID != id || index < 0 || index >= len(m.selection.Files) {
		return nil, errors.New("media selection unavailable")
	}
	entry := m.selection.Files[index]
	if offset < 0 || offset >= entry.Size {
		return nil, errors.New("invalid media offset")
	}
	m.timer.Reset(2 * time.Minute)
	bytes := make([]byte, min(portalChunkSize, entry.Size-offset))
	_, err := io.ReadFull(io.NewSectionReader(entry.file, offset, int64(len(bytes))), bytes)
	return bytes, err
}

func (m *portalMedia) release(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.selection.ID == id {
		m.closeLocked()
	}
}

func (m *portalMedia) closeLocked() {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	for _, entry := range m.selection.Files {
		_ = entry.file.Close()
	}
	m.selection = portalSelection{}
}
