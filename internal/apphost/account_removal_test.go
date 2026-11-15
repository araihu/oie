package apphost

import (
	"bufio"
	"bytes"
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

func TestRemoveAccountThroughBFF(t *testing.T) {
	dir := t.TempDir()
	engine := testServer(t, Config{Role: "hosted", Listen: "127.0.0.1:0", DataDir: dir})
	bff := testServer(t, Config{Role: "bff", Listen: "127.0.0.1:0", EngineURL: engine.URL()})
	presentation := httptest.NewServer(bff.Handler())
	defer presentation.Close()

	client := engineClient(t, engine)
	id := engineIdentity(t, engine)
	key := func() string { return uuid.Must(uuid.NewV7()).String() }
	register := func(address, op string, want int) string {
		t.Helper()
		res, err := client.RegisterAccountWithResponse(t.Context(), &engineapi.RegisterAccountParams{IdempotencyKey: op},
			engineapi.RegisterAccount{Address: address, Label: "Account " + address, Protocol: "xmpp"})
		if err != nil || res.StatusCode() != want {
			t.Fatalf("register %s: %#v %v", address, res, err)
		}
		if res.JSON201 != nil {
			return res.JSON201.Id
		}
		return ""
	}
	registerKey := key()
	a := register("alice@localhost", registerKey, 201)
	b := register("bob@localhost", key(), 201)
	seed := func(account string) (chat, message, credential string) {
		t.Helper()
		c, err := client.OpenConversationWithResponse(t.Context(), &engineapi.OpenConversationParams{IdempotencyKey: key()},
			engineapi.OpenConversation{AccountId: account, Address: "friend@localhost"})
		if err != nil || c.JSON201 == nil {
			t.Fatalf("open chat: %#v %v", c, err)
		}
		msg, err := client.SendMessageWithResponse(t.Context(), c.JSON201.Id, &engineapi.SendMessageParams{IdempotencyKey: key()},
			engineapi.SendMessage{OriginalText: "A pending message"})
		if err != nil || msg.JSON202 == nil {
			t.Fatalf("queue send: %#v %v", msg, err)
		}
		password, endpoint := "local-fixture-password", "127.0.0.1:1"
		configured, err := client.ConfigureAccountConnectionWithResponse(t.Context(), account,
			engineapi.ConfigureConnection{Password: &password, Endpoint: &endpoint})
		if err != nil || configured.JSON202 == nil {
			t.Fatalf("save connection: %#v %v", configured, err)
		}
		return c.JSON201.Id, msg.JSON202.Id, filepath.Join(dir, "private", "connections", account+".json")
	}
	chat, message, credential := seed(a)
	keptChat, keptMessage, keptCredential := seed(b)
	returnTo := "/?open=accounts&accountPage=remove&managedAccount=" + a + "&chat=" + chat + "&lang=pt-BR"
	page, err := presentation.Client().Get(presentation.URL + returnTo)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil || !bytes.Contains(body, []byte("Remover conta")) || !bytes.Contains(body, []byte(a)) {
		t.Fatalf("missing account confirmation: %v", err)
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

	post := func(engineID, account, confirmation, token string, native bool, want int) string {
		t.Helper()
		form := url.Values{"engineId": {engineID}, "accountId": {account}, "confirmation": {confirmation}, "csrf": {token},
			"returnTo": {returnTo}, "viewAccount": {a}, "lang": {"pt-BR"}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, presentation.URL+"/ui/accounts/remove", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if !native {
			req.Header.Set("Accept", "application/json")
		}
		req.AddCookie(csrf)
		browser := *presentation.Client()
		browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, err := browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != want {
			text, _ := io.ReadAll(res.Body)
			t.Fatalf("remove status %d, want %d: %s", res.StatusCode, want, text)
		}
		return res.Header.Get("Location")
	}

	post(id, a, "remove", "", false, 403)
	post(id, a, "", csrf.Value, false, 422)
	post(id, "invalid", "remove", csrf.Value, false, 422)
	post(key(), a, "remove", csrf.Value, false, 409)
	for _, path := range []string{credential, keptCredential} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("denied request removed credentials: %v", err)
		}
	}

	events := stream(t, presentation.URL+"/ui/events")
	defer func() { _ = events.Body.Close() }()
	dest := post(id, a, "remove", csrf.Value, true, 303)
	if dest != "/?inbox=1&lang=pt-BR&open=accounts" {
		t.Fatalf("wrong native destination: %s", dest)
	}
	event := bufio.NewScanner(events.Body)
	for event.Scan() && !strings.Contains(event.Text(), "event: account.removed") {
	}
	if !event.Scan() || !strings.Contains(event.Text(), a) {
		t.Fatalf("missing removal event: %v %s", event.Err(), event.Text())
	}

	if _, err := os.Stat(credential); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed account credential still present: %v", err)
	}
	if _, err := os.Stat(keptCredential); err != nil {
		t.Fatalf("other account credential removed: %v", err)
	}
	for chatID, want := range map[string]int{chat: 404, keptChat: 200} {
		res, err := client.GetConversationWithResponse(t.Context(), chatID)
		if err != nil || res.StatusCode() != want {
			t.Fatalf("chat %s: %#v %v", chatID, res, err)
		}
	}

	for messageID, want := range map[string]int{message: 404, keptMessage: 200} {
		res, err := client.GetMessageWithResponse(t.Context(), messageID)
		if err != nil || res.StatusCode() != want {
			t.Fatalf("message %s: %#v %v", messageID, res, err)
		}
	}

	post(id, a, "remove", csrf.Value, false, 200)
	register("alice@localhost", registerKey, 404)
	readded := register("alice@localhost", key(), 201)
	if readded == a {
		t.Fatal("re-adding the address reused the old account ID")
	}
	for event.Scan() && !strings.Contains(event.Text(), readded) {
		if strings.Contains(event.Text(), "event: account.removed") {
			t.Fatal("repeat emitted another removal event")
		}
	}
	if err := event.Err(); err != nil {
		t.Fatal(err)
	}
	post(id, a, "remove", csrf.Value, false, 200)
	res, err := client.GetAccountWithResponse(t.Context(), readded)
	if err != nil || res.JSON200 == nil {
		t.Fatalf("old confirmation removed newly added account: %#v %v", res, err)
	}
}
