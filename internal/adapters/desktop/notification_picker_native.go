//go:build (linux || darwin) && cgo && !server

package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	wails "github.com/wailsapp/wails/v3/pkg/application"
)

type notificationToneFile struct {
	Name  string `json:"name,omitempty"`
	Bytes []byte `json:"bytes,omitempty"`
	Size  int64  `json:"size"`
}

// ChooseNotificationTone reads the selected file through Flatpak's Documents
// portal. The nested WebKit sandbox cannot read the portal path itself.
func (s *runtimeService) ChooseNotificationTone(ctx context.Context, title string) (*notificationToneFile, error) {
	const maxSize = 5000000
	if desktopAppID() != flatpakAppID {
		return nil, errors.New("native notification file picker unavailable")
	}
	s.dialogMu.Lock()
	defer s.dialogMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialog := s.app.Dialog.OpenFile()
	dialog.SetOptions(&wails.OpenFileDialogOptions{
		Title: title, Window: s.app.Window.Current(), CanChooseFiles: true, AllowsOtherFileTypes: true,
	})
	path, err := dialog.PromptForSingleSelection()
	if err != nil || path == "" {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("cannot read notification audio")
	}
	if info.Size() > maxSize {
		return &notificationToneFile{Size: info.Size()}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSize {
		return &notificationToneFile{Size: int64(len(data))}, nil
	}
	return &notificationToneFile{Name: filepath.Base(path), Bytes: data, Size: int64(len(data))}, nil
}
