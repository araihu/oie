package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/apphost"
)

// An explicit launch argument overrides the saved desktop preference.
func desktopEngineSelection(config Config) (apphost.DesktopEngine, error) {
	if config.EngineURL != "" {
		return apphost.DesktopEngine{URL: config.EngineURL}, nil
	}
	if config.EngineTokenFile != "" {
		return apphost.DesktopEngine{}, errors.New("--engine-token-file requires an explicit --engine-url")
	}
	path := engineModePath(config.DataDir)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return apphost.DesktopEngine{}, nil
	}
	if err != nil {
		return apphost.DesktopEngine{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return apphost.DesktopEngine{}, fmt.Errorf("desktop engine preference must be a private regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return apphost.DesktopEngine{}, err
	}
	var selection apphost.DesktopEngine
	if err := json.Unmarshal(data, &selection); err != nil {
		var address string
		if json.Unmarshal(data, &address) != nil {
			return apphost.DesktopEngine{}, fmt.Errorf("read desktop engine preference: %w", err)
		}
		selection.URL = address
	}
	return selection, nil
}

func engineModePath(dataDir string) string {
	return filepath.Join(dataDir, "webview", "engine.json")
}
