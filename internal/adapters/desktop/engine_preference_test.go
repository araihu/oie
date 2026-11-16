package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/araihu/oie/internal/apphost"
)

func TestDesktopEnginePreferenceReadsExistingProfiles(t *testing.T) {
	config := Config{DataDir: t.TempDir()}
	path := engineModePath(config.DataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	const origin = "https://host.example"
	if err := writePrivateJSON(path, origin); err != nil {
		t.Fatal(err)
	}
	legacy, err := desktopEngineSelection(config)
	if err != nil || legacy != (apphost.DesktopEngine{URL: origin}) {
		t.Fatalf("could not read an existing address: %v", err)
	}
	saved := apphost.DesktopEngine{URL: origin, APIKey: "fixture-key"}
	if err := writePrivateJSON(path, saved); err != nil {
		t.Fatal(err)
	}
	got, err := desktopEngineSelection(config)
	if err != nil || got != saved {
		t.Fatalf("could not read the saved connection: %v", err)
	}
	config.EngineURL = "https://elsewhere.example"
	override, err := desktopEngineSelection(config)
	if err != nil || override != (apphost.DesktopEngine{URL: config.EngineURL}) {
		t.Fatalf("explicit address inherited the saved key: %v", err)
	}
}
