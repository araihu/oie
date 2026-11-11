package apphost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/google/uuid"
)

func TestResetDataThroughBFF(t *testing.T) {
	dir := t.TempDir()
	engine := testServer(t, Config{Role: "hosted", Listen: "127.0.0.1:0", DataDir: dir})
	bff := testServer(t, Config{Role: "bff", Listen: "127.0.0.1:0", EngineURL: engine.URL()})
	presentation := httptest.NewServer(bff.Handler())
	defer presentation.Close()

	client := engineClient(t, engine)
	id := engineIdentity(t, engine)
	registered, err := client.RegisterAccountWithResponse(t.Context(), &engineapi.RegisterAccountParams{IdempotencyKey: uuid.Must(uuid.NewV7()).String()}, engineapi.RegisterAccount{Address: "alice@localhost", Label: "Remove me", Protocol: "xmpp"})
	if err != nil || registered.JSON201 == nil {
		t.Fatalf("register account: %v", err)
	}
	owned := []string{"private/connections/old.json", "media/images/original.jpg", "media/avatars/avatar.png"}
	for _, name := range append(owned, "unrelated") {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	page, err := presentation.Client().Get(presentation.URL + "/?open=settings&lang=pt-BR")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil || !bytes.Contains(body, []byte("Apagar todos os dados")) {
		t.Fatalf("missing reset dialog: %v", err)
	}
	var csrf *http.Cookie
	for _, cookie := range page.Cookies() {
		if cookie.Name == "oie_csrf" {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("missing CSRF cookie")
	}
	post := func(engineID, confirmation, token string, want int) {
		t.Helper()
		form := url.Values{"engineId": {engineID}, "confirmation": {confirmation}, "csrf": {token}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, presentation.URL+"/ui/engine/reset", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(csrf)
		res, err := presentation.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != want {
			t.Fatalf("reset status %d, want %d", res.StatusCode, want)
		}
		if want == 204 && res.Header.Get("Clear-Site-Data") != `"cache"` {
			t.Fatal("reset did not ask the browser to clear its HTTP cache")
		}
	}
	post(id, "delete", "", 403)
	post(id, "Delete", csrf.Value, 400)
	post(uuid.Must(uuid.NewV7()).String(), "delete", csrf.Value, 409)
	if got := engineIdentity(t, engine); got != id {
		t.Fatalf("rejected requests changed the engine: %s", got)
	}

	events := stream(t, presentation.URL+"/ui/events")
	post(id, "delete", csrf.Value, 204)
	if _, err := io.ReadAll(events.Body); err != nil {
		t.Fatalf("previous event stream did not end: %v", err)
	}
	if got := engineIdentity(t, engine); got == id {
		t.Fatal("reset kept the previous engine identity")
	}
	accounts, err := client.ListAccountsWithResponse(t.Context(), nil)
	if err != nil || accounts.JSON200 == nil || len(accounts.JSON200.Items) != 0 {
		t.Fatalf("reset retained accounts: %v", err)
	}
	post(id, "delete", csrf.Value, 409)
	for _, name := range owned {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("managed file survived reset: %s (%v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated")); err != nil {
		t.Fatalf("reset touched an unmanaged file: %v", err)
	}
}

func TestResetInvalidConfirmationAtEngine(t *testing.T) {
	engine := testServer(t, Config{Listen: "127.0.0.1:0", DataDir: t.TempDir()})
	id := engineIdentity(t, engine)
	input, err := json.Marshal(engineapi.ResetEngineDataJSONRequestBody{EngineId: id, Confirmation: "delete "})
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(engine.URL()+resetPath, "application/json", bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest || engineIdentity(t, engine) != id {
		t.Fatalf("invalid confirmation was not rejected: %d", res.StatusCode)
	}
}
