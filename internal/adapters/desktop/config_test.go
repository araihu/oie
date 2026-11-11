package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDataDirectoryDoesNotDependOnWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := filepath.Join(home, "data")
	t.Setenv("XDG_DATA_HOME", data)
	first, err := DefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	second, err := DefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !filepath.IsAbs(first) {
		t.Fatalf("unstable desktop data directory: %q %q", first, second)
	}
	expected := filepath.Join(data, "oie")
	if runtime.GOOS == "darwin" {
		expected = filepath.Join(home, "Library", "Application Support", "Oiê")
	}
	if first != expected {
		t.Fatalf("data path %q, want %q", first, expected)
	}
}
func TestRelativeXDGDataHomeDoesNotMakeStorageWorkingDirectoryDependent(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS uses Application Support")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "relative")
	path, err := DefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, ".local", "share", "oie") {
		t.Fatal(path)
	}
}
func TestDataDirectoryAliasesSelectTheSameInstance(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "profile")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	a, err := normalizeConfig(Config{DataDir: real})
	if err != nil {
		t.Fatal(err)
	}
	b, err := normalizeConfig(Config{DataDir: link})
	if err != nil {
		t.Fatal(err)
	}
	if a.DataDir != b.DataDir || instanceID(a.DataDir) != instanceID(b.DataDir) {
		t.Fatal("aliased data directories do not share single-instance ownership")
	}
	if instanceID(a.DataDir) == instanceID(t.TempDir()) {
		t.Fatal("independent data directories share single-instance ownership")
	}
}
func TestDesktopMenuLanguageSelectionAndExplicitOverride(t *testing.T) {
	if got := deviceLanguage("en"); got != "en" {
		t.Fatal(got)
	}
	if got := deviceLanguage("pt-BR"); got != "pt-BR" {
		t.Fatal(got)
	}
	en, err := localizedLabels("en")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := localizedLabels("pt-BR")
	if err != nil {
		t.Fatal(err)
	}
	if en.Quit == pt.Quit {
		t.Fatal("native menu ignores locale")
	}
	if _, err := normalizeConfig(Config{DataDir: t.TempDir(), Language: "unknown"}); err == nil {
		t.Fatal("unsupported explicit locale accepted")
	}
}

func TestDesktopMenuLanguageEnvironmentFallback(t *testing.T) {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		t.Setenv(name, "")
	}
	t.Setenv("LANG", "pt_BR.UTF-8")
	if runtime.GOOS == "darwin" && platformLanguage() != "" {
		t.Skip("native macOS language is authoritative over shell environment")
	}
	if got := deviceLanguage("auto"); got != "pt-BR" {
		t.Fatal(got)
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	if got := deviceLanguage("auto"); got != "en" {
		t.Fatal(got)
	}
}

func TestNewProfileUnderAliasedParentKeepsInstanceIdentity(t *testing.T) {
	parent := t.TempDir()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	first, err := normalizeConfig(Config{DataDir: filepath.Join(link, "new", "profile")})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(first.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	second, err := normalizeConfig(Config{DataDir: filepath.Join(link, "new", "profile")})
	if err != nil {
		t.Fatal(err)
	}
	if first.DataDir != second.DataDir || instanceID(first.DataDir) != instanceID(second.DataDir) {
		t.Fatalf("profile identity changed: %q %q", first.DataDir, second.DataDir)
	}
}
