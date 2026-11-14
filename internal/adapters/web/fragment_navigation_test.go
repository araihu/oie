package web

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
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

func TestSourceNavigationUsesShortContextAndKeepsPaging(t *testing.T) {
	fixture := &liveFixture{}
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/conversations/"+testChat+"/messages/"+testMessage+"/context" {
			fixture.ServeHTTP(w, r)
			return
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		items := make([]engineapi.Message, limit)
		for i := range items {
			m := fixture.message()
			m.SendState = "received"
			m.Id = fmt.Sprintf("019a5df0-0000-7000-8000-%012d", i+300)
			m.CreatedAt = m.CreatedAt.Add(-time.Duration(i) * time.Second)
			items[i] = m
		}
		items[limit/2].Id = testMessage
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(engineapi.MessageContext{Items: items, TargetId: testMessage, HasOlder: true, HasNewer: true})
	})
	ui, client, _ := liveUI(t, backend)
	for _, fragment := range []string{"app", "source", "source-pane"} {
		t.Run(fragment, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ui.URL+"/?chat="+testChat+"&message="+testMessage, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("HX-Request", "true")
			req.Header.Set("X-Oie-Fragment", fragment)
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			limit := timelinePageSize
			if fragment != "app" {
				limit = sourceContextPageSize
			}
			if res.StatusCode != http.StatusOK || strings.Count(body, "data-message-id=") != limit ||
				!strings.Contains(body, `data-focus-message="`+testMessage+`"`) ||
				!strings.Contains(body, `data-has-older="true" data-has-newer="true"`) {
				t.Fatalf("source window for %s returned HTTP %d and %d rows", fragment, res.StatusCode, strings.Count(body, "data-message-id="))
			}
			if fragment == "source-pane" && strings.Contains(body, `id="conversation-list"`) {
				t.Fatal("conversation fragment included the inbox")
			}
		})
	}
}
