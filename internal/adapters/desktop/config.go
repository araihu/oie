// Package desktop hosts the existing Oie presentation in a native window.
package desktop

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config selects one local data directory and optionally a hosted engine.
type Config struct {
	DataDir    string
	EngineURL  string
	FFmpeg     string
	XMPPCAFile string
	Language   string
}

// DefaultDataDir is independent of the working directory and the installed binary.
func DefaultDataDir() (string, error) {
	if runtime.GOOS == "darwin" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "Oiê"), nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "oie"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "oie"), nil
}

func normalizeConfig(c Config) (Config, error) {
	if c.DataDir == "" {
		var err error
		c.DataDir, err = DefaultDataDir()
		if err != nil {
			return c, err
		}
	}
	dir, err := filepath.Abs(c.DataDir)
	if err != nil {
		return c, err
	}
	c.DataDir = filepath.Clean(dir)
	resolved, err := resolveDataDir(c.DataDir)
	if err != nil {
		return c, err
	}
	c.DataDir = resolved
	if c.FFmpeg == "" {
		c.FFmpeg = "ffmpeg"
	}
	if c.Language != "" && c.Language != "auto" && c.Language != "en" && c.Language != "pt-BR" {
		return c, fmt.Errorf("unsupported desktop language %q", c.Language)
	}
	return c, nil
}

// Resolve existing parents too, so first launch through a symlinked parent has
// the same instance identity as subsequent launches after its directory exists.
func resolveDataDir(path string) (string, error) {
	var suffix []string
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

const nativeAppID = "org.araihu.oie"
const flatpakAppID = "com.araihu.oie"

func desktopAppID() string {
	if runtime.GOOS == "linux" && os.Getenv("FLATPAK_ID") == flatpakAppID {
		return flatpakAppID
	}
	return nativeAppID
}

func instanceID(dataDir string) string {
	sum := sha256.Sum256([]byte(dataDir))
	return fmt.Sprintf("%s.profile-%x", desktopAppID(), sum[:12])
}

//go:embed locales/*.json
var nativeCatalogs embed.FS

type labels struct {
	AttachFiles string `json:"attachFiles"`
	SaveFile    string `json:"saveFile"`
	SaveFailed  string `json:"saveFailed"`
	Show        string `json:"show"`
	Quit        string `json:"quit"`
	File        string `json:"file"`
	Edit        string `json:"edit"`
	Undo        string `json:"undo"`
	Redo        string `json:"redo"`
	Cut         string `json:"cut"`
	Copy        string `json:"copy"`
	Paste       string `json:"paste"`
	SelectAll   string `json:"selectAll"`
}

func localizedLabels(language string) (labels, error) {
	data, err := nativeCatalogs.ReadFile("locales/" + language + ".json")
	if err != nil {
		return labels{}, err
	}
	var value labels
	if err = json.Unmarshal(data, &value); err != nil {
		return labels{}, err
	}
	if value.AttachFiles == "" || value.SaveFile == "" || value.SaveFailed == "" || value.Show == "" || value.Quit == "" || value.File == "" || value.Edit == "" || value.Undo == "" || value.Redo == "" || value.Cut == "" || value.Copy == "" || value.Paste == "" || value.SelectAll == "" {
		return labels{}, fmt.Errorf("incomplete desktop language catalog %q", language)
	}
	return value, nil
}
func deviceLanguage(override string) string {
	if override != "" && override != "auto" {
		return override
	}
	if value := platformLanguage(); value != "" {
		return normalizedLanguage(value)
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		return normalizedLanguage(value)
	}
	return "en"
}

func normalizedLanguage(value string) string {
	value = strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	if strings.HasPrefix(value, "pt") {
		return "pt-BR"
	}
	return "en"
}
