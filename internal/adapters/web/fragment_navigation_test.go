package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNavigationResponsesKeepFragmentAndDocumentBoundaries(t *testing.T) {
	fixture := &liveFixture{}
	ui, client, _ := liveUI(t, fixture)
	for _, tc := range []struct {
		name            string
		hx, kind, query string
		want, absent    []string
	}{
		{name: "native", want: []string{"<!doctype html>", `id="oie-app"`, `id="workspace"`, `src="/ui/navigation.js"`}},
		{name: "shell fragment", hx: "true", kind: "app", want: []string{`id="oie-app"`, `id="oie-route"`, `id="workspace"`, `hx-target="#oie-route"`, `data-engine-id="` + testEngine + `"`, `data-account-id="` + testAccount + `"`}, absent: []string{"<!doctype", "<head>", `src="/ui/navigation.js"`}},
		{name: "drawer fragment", hx: "true", kind: "info", want: []string{`id="chat-info-content"`, `data-details-conversation="` + testChat + `"`}, absent: []string{"<!doctype", "<head>", `id="oie-app"`, `id="workspace"`}},
		{name: "account workspace", hx: "true", kind: "accounts", query: "open=accounts", want: []string{`id="account-workspace"`, `id="account-overview"`}, absent: []string{`id="oie-app"`, `id="message-composer"`}},
		{name: "appearance", hx: "true", kind: "appearance", query: "theme=dark", want: []string{`id="appearance-settings"`, `data-mode="dark"`}, absent: []string{`id="oie-app"`, `id="message-composer"`}},
		{name: "catalog", hx: "true", kind: "shared", query: "section=media", want: []string{`data-shared-fragment`, `id="shared-search-results"`}, absent: []string{`id="oie-app"`, `id="shared-content-search"`}},
		{name: "timeline", hx: "true", kind: "timeline", want: []string{`id="message-timeline"`, `data-conversation-id="` + testChat + `"`}, absent: []string{`id="oie-app"`, `id="message-composer"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := tc.query
			if query == "" {
				query = "chat=" + testChat + "&open=details"
			}
			req, err := http.NewRequest("GET", ui.URL+"/?"+query, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.hx != "" {
				req.Header.Set("HX-Request", tc.hx)
			}
			if tc.kind != "" {
				req.Header.Set("X-Oie-Fragment", tc.kind)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			html := string(raw)
			if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("response status/cache %d %q", res.StatusCode, res.Header.Get("Cache-Control"))
			}
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(html, absent) {
					t.Errorf("fragment included %q", absent)
				}
			}
			vary := res.Header.Get("Vary")
			if !strings.Contains(vary, "HX-Request") || !strings.Contains(vary, "X-Oie-Fragment") {
				t.Errorf("cache did not distinguish fragment representation: %q", vary)
			}
		})
	}
	for _, hx := range []string{"", "true"} {
		req, _ := http.NewRequest("GET", ui.URL+"/missing", nil)
		req.Header.Set("HX-Request", hx)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 404 || !strings.Contains(string(raw), `id="oie-app"`) || !strings.Contains(string(raw), "Page not found") {
			t.Fatal("missing route lost in-shell recovery")
		}
	}
	if fixture.effectCount() != 0 {
		t.Fatal("presentation navigation created a protocol effect")
	}
}
