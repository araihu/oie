package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
)

func TestBrowserIdentityAndPlatformAssetsDoNotNeedEngine(t *testing.T) {
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; http.Error(w, "unexpected engine read", 500) }))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "pt-BR"} {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", "/manifest.webmanifest?lang="+lang, nil))
		var manifest struct {
			ID, Name, ShortName, Description, Lang, StartURL, Scope, Display string
			Icons                                                            []manifestIcon
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || manifest.Name != "Oiê" || manifest.Lang != lang || manifest.Display != "standalone" || manifest.Description == "" || string(raw["id"]) != `"/"` || string(raw["start_url"]) != `"/"` || string(raw["scope"]) != `"/"` || len(manifest.Icons) != 4 {
			t.Fatal("incomplete localized install identity", w.Body.String())
		}
		headManifest := httptest.NewRecorder()
		ui.ServeHTTP(headManifest, httptest.NewRequest("HEAD", "/manifest.webmanifest?lang="+lang, nil))
		if headManifest.Code != 200 || headManifest.Body.Len() != 0 || headManifest.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("invalid manifest HEAD or locale cache policy")
		}
		if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/manifest+json") {
			t.Fatal("wrong manifest type")
		}
		any, maskable := 0, 0
		for _, icon := range manifest.Icons {
			address, err := url.Parse(icon.Src)
			if err != nil || !strings.HasPrefix(address.Path, "/ui/brand/") || address.Query().Get("v") == "" {
				t.Fatal("unversioned/external install icon", icon)
			}
			image := httptest.NewRecorder()
			ui.ServeHTTP(image, httptest.NewRequest("GET", icon.Src, nil))
			cfg, err := png.DecodeConfig(bytes.NewReader(image.Body.Bytes()))
			if err != nil || image.Code != 200 || icon.Type != "image/png" {
				t.Fatal("invalid install PNG", icon, err)
			}
			want := 192
			if strings.Contains(icon.Sizes, "512") {
				want = 512
			}
			if cfg.Width != want || cfg.Height != want {
				t.Fatal("wrong PNG dimensions", icon, cfg)
			}
			if icon.Purpose == "any" {
				any++
			}
			if icon.Purpose == "maskable" {
				maskable++
			}
			head := httptest.NewRecorder()
			ui.ServeHTTP(head, httptest.NewRequest("HEAD", icon.Src, nil))
			if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") == "" {
				t.Fatal("invalid icon HEAD")
			}
			conditional := httptest.NewRequest("GET", icon.Src, nil)
			conditional.Header.Set("If-None-Match", head.Header().Get("ETag"))
			cached := httptest.NewRecorder()
			ui.ServeHTTP(cached, conditional)
			if cached.Code != 304 || cached.Body.Len() != 0 {
				t.Fatal("icon conditional GET failed")
			}
		}
		if any != 2 || maskable != 2 {
			t.Fatal("ordinary and maskable icons missing")
		}
	}
	for _, file := range []string{"favicon.svg", "favicon-16.png", "favicon-32.png", "apple-touch-icon-180.png", "logo-light.svg", "logo-dark.svg", "NOTICE.txt"} {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", brandURL(file), nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatal("missing embedded brand asset", file)
		}
	}
	for _, address := range []string{"/ui/brand/../../go.mod", "/ui/brand/unknown.svg", "/ui/brand/", "/ui/brand/sources/private.txt"} {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", address, nil))
		if w.Code != 404 {
			t.Fatal("invalid asset path accepted", address)
		}
	}
	if requests != 0 {
		t.Fatalf("browser brand reads reached engine %d times", requests)
	}
}

func TestEmbeddedBrandSnapshotMatchesSharedDistributionHashes(t *testing.T) {
	data, err := brandFiles.ReadFile("brand/SOURCE.json")
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Repository, Revision, CandidateRelease, DisplayName string
		Files                                               []struct{ File, SourcePath, SHA256 string }
	}
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Repository != "https://github.com/araihu/assets" || record.DisplayName != "Oiê" || record.CandidateRelease != "v0.2.7" || len(record.Files) < 10 || len(record.Revision) != 40 {
		t.Fatal("missing shared provenance")
	}
	for _, file := range record.Files {
		b, err := brandFiles.ReadFile("brand/" + file.File)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != file.SHA256 || path.Clean(file.SourcePath) != file.SourcePath {
			t.Fatal("shared artifact drift", file.File)
		}
	}
}
