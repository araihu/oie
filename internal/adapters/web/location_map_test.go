package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocationMapStyle(t *testing.T) {
	for _, test := range []struct {
		url, attribution string
		valid            bool
	}{
		{"", "", true},
		{"https://tiles.openfreemap.org/styles/liberty", "OpenFreeMap © OpenMapTiles", true},
		{"https://maps.example/style.json", "", false},
		{"http://maps.example/style.json", "Map source", false},
		{"https://user:password@maps.example/style.json", "Map source", false},
		{"https://maps.example/style.json#anchor", "Map source", false},
		{"https://", "Map source", false},
	} {
		_, err := New("http://127.0.0.1:8080", WithLocationMapStyle(test.url, test.attribution))
		if (err == nil) != test.valid {
			t.Errorf("style %q: %v", test.url, err)
		}
	}
}

func TestLocationMapAssets(t *testing.T) {
	server, err := New("http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/ui/location_map.js", "/ui/location_map/maplibre-gl-worker.mjs", "/ui/location_map/paulista.geojson"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Body.Len() == 0 || response.Header().Get("ETag") == "" {
			t.Fatalf("%s: expected a local asset with an ETag, got %d", path, response.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/location_map/paulista.geojson", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST local map asset: %d", response.Code)
	}
}
