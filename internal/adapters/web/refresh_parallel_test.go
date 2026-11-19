package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestRefreshUnreadReadsOverlapAndDeduplicateAcrossFilters(t *testing.T) {
	active, archived := make(chan struct{}), make(chan struct{})
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The archived response includes a conversation that moved during paging.
		// Match the original active-first count regardless of response ordering.
		c := engineapi.Conversation{Id: testChat, AccountId: testAccount, UnreadCount: 3}
		var other chan struct{}
		if r.URL.Query().Get("archived") == "true" {
			close(archived)
			other = active
			c.UnreadCount = 7
		} else {
			close(active)
			other = archived
		}
		select {
		case <-other:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []engineapi.Conversation{c}})
		case <-r.Context().Done():
		}
	}))
	defer engine.Close()
	server, err := New(engine.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if totals := server.accountUnread(ctx); totals[testAccount] != 3 {
		t.Fatalf("overlapping reads must count the conversation once, got %v", totals)
	}
}

func TestRefreshAccountSummariesPublishInAccountOrder(t *testing.T) {
	secondAvatar := make(chan struct{})
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/accounts/" + testAccount + "/avatars":
			select {
			case <-secondAvatar:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/v1/accounts/" + testEngine + "/avatars":
			close(secondAvatar)
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/api/v1/accounts/" + testAccount + "/connection":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/api/v1/accounts/" + testEngine + "/connection":
			_ = json.NewEncoder(w).Encode(engineapi.Connection{AccountId: testEngine, State: "connected"})
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer engine.Close()
	server, err := New(engine.URL)
	if err != nil {
		t.Fatal(err)
	}
	v := view{Accounts: []engineapi.Account{{Id: testAccount}, {Id: testEngine}}, Avatars: map[avatarIdentity]string{}, Connections: map[string]connectionView{}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server.loadAccountSummaries(ctx, &v, true, true, false, func(context.Context, *http.Request) error { return nil })
	if !slices.Equal(v.AvatarAccounts, []string{testAccount, testEngine}) || v.Connections[testAccount].Known || !v.Connections[testEngine].Known {
		t.Fatalf("unexpected avatar order or connection availability: %v, %v", v.AvatarAccounts, v.Connections)
	}
}
