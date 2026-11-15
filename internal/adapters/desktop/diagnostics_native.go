//go:build (linux || darwin) && cgo && !server

package desktop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/araihu/oie/internal/diagnostics"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func (s *runtimeService) CopyDiagnostics(snapshot string) bool {
	return diagnostics.ValidSnapshot(snapshot) && s.app.Clipboard.SetText(snapshot)
}

func (s *runtimeService) SaveDiagnostics(ctx context.Context, snapshot string) (bool, error) {
	if !diagnostics.ValidSnapshot(snapshot) {
		return false, errors.New("invalid diagnostics snapshot")
	}
	s.dialogMu.Lock()
	defer s.dialogMu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dialog := s.app.Dialog.SaveFile()
	dialog.SetOptions(&application.SaveFileDialogOptions{
		Title: s.labels.SaveFile, Filename: "oie-diagnostics.jsonl",
		CanCreateDirectories: true, AllowOtherFileTypes: true, Window: s.app.Window.Current(),
	})
	target, err := dialog.PromptForSingleSelection()
	if err != nil || target == "" {
		return false, err
	}
	err = saveDiagnosticsSnapshot(ctx, target, snapshot)
	if err != nil {
		return false, errors.New("could not save diagnostics snapshot")
	}
	return true, nil
}

func diagnosticsDir(profile string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	state := filepath.Join(home, ".local", "state")
	if runtime.GOOS == "darwin" {
		state = filepath.Join(home, "Library", "Logs")
	} else if path := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(path) {
		state = path
	}
	hash := sha256.Sum256([]byte(profile))
	return filepath.Join(state, "oie", fmt.Sprintf("%x", hash[:12]))
}

func saveDiagnosticsSnapshot(ctx context.Context, target, snapshot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".oie-diagnostics-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if _, err := file.WriteString(snapshot); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}
