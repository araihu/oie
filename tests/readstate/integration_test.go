package readstate_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestReadStateBFFAndRealSQLiteEngine(t *testing.T) {
	next := func() domain.ID {
		v, err := uuid.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		return domain.ID(v.String())
	}
	engineID := next()
	store, err := sqlite.Open(t.Context(), t.TempDir(), engineID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "local", func() (domain.ID, error) { return next(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	account, err := engine.Register(t.Context(), next(), application.RegisterInput{Protocol: "xmpp", Address: "alice@fixture", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	c := domain.Conversation{ID: next(), RevisionID: next(), AccountID: account.Account.ID, PeerID: next(), Kind: "direct", Address: "bob@fixture", Title: "Bob", UpdatedAt: time.Now().UTC()}
	opened, err := store.OpenConversation(t.Context(), application.ConversationMutation{Operation: application.Operation{ID: next(), ResourceID: c.ID, Kind: "open_conversation", CreatedAt: time.Now().UTC()}, Fingerprint: "read-integration", Conversation: c})
	if err != nil {
		t.Fatal(err)
	}
	c = opened.Conversation
	var ids []string
	for range 3 {
		m := domain.Message{ID: next(), RevisionID: next(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob"}, ProviderID: string(next()), OriginalText: "Incoming retained text", SendState: "received", CreatedAt: time.Now().UTC()}
		if err = store.ReceiveMessage(t.Context(), c, m, next()); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, string(m.ID))
	}
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(handler)
	defer api.Close()
	ui, err := web.New(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	initial := httptest.NewRecorder()
	ui.ServeHTTP(initial, httptest.NewRequest("GET", "/?chat="+string(c.ID)+"&lang=en", nil))
	if initial.Code != 200 {
		t.Fatal(initial.Code, initial.Body.String())
	}
	var cookie *http.Cookie
	for _, candidate := range initial.Result().Cookies() {
		if candidate.Name == "oie_csrf" {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("missing browser CSRF cookie")
	}
	body, _ := json.Marshal(map[string]any{"messageIds": ids[:2]})
	request := httptest.NewRequest("POST", "/ui/api/v1/conversations/"+string(c.ID)+"/read-state", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", string(next()))
	request.Header.Set("X-Oie-Engine-ID", string(engineID))
	request.Header.Set("X-Oie-Account-ID", string(c.AccountID))
	request.Header.Set("X-CSRF-Token", cookie.Value)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	ui.ServeHTTP(result, request)
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body.String())
	}
	actual, err := engine.ConversationReadState(t.Context(), c.ID)
	if err != nil || actual.UnreadCount != 1 || actual.RevisionID == "" {
		t.Fatal(actual, err)
	}
	updated := httptest.NewRecorder()
	ui.ServeHTTP(updated, httptest.NewRequest("GET", "/?chat="+string(c.ID)+"&lang=en", nil))
	if updated.Code != 200 || !strings.Contains(updated.Body.String(), `aria-label="Unread: 1"`) {
		t.Fatal("BFF badge did not expose committed engine state", updated.Code)
	}
}
