package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestOmemoVerificationLinksBindPeerAndProtocol(t *testing.T) {
	d := engineapi.OmemoDevice{Id: 12, Active: true, Fingerprint: strings.Repeat("ab", 32)}
	s := engineapi.OmemoDevices{Address: "alice@example.org", Devices: []engineapi.OmemoDevice{d}}
	for _, legacy := range []bool{true, false} {
		uri := trustURI(s.Address, d, legacy)
		if got, ok := candidateDevice(uri, s); !ok || got.Id != d.Id {
			t.Fatal("correct link rejected", uri)
		}
		for _, bad := range []string{strings.Replace(uri, "alice", "bob", 1), uri + ";distrust=" + d.Fingerprint, strings.Replace(uri, d.Fingerprint, strings.Repeat("cd", 32), 1)} {
			if _, ok := candidateDevice(bad, s); ok {
				t.Fatal("accepted", bad)
			}
		}
	}
	if _, ok := candidateDevice("xmpp:alice@example.org?trust-message;encryption=urn:xmpp:omemo:2;trust="+d.Fingerprint, s); ok {
		t.Fatal("cross-protocol verification")
	}
}

func TestOmemoSettingsRendersShareableQRImages(t *testing.T) {
	state := engineapi.OmemoDevices{Address: "alice@example.org", Devices: []engineapi.OmemoDevice{{Id: 3, Own: true, Active: true, Fingerprint: strings.Repeat("ab", 32)}}}
	var body bytes.Buffer
	if err := omemoSettingsPage(view{}, state, "account", "", nil).Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	if strings.Count(body.String(), `src="data:image/png;base64,`) != 2 {
		t.Fatal("missing verification QR image")
	}
}

func TestRoomOmemoConflictKeepsTheSettingsFormAvailable(t *testing.T) {
	const account = "019a5df0-0000-7000-8000-000000000001"
	const room = "019a5df0-0000-7000-8000-000000000002"
	const engine = "019a5df0-0000-7000-8000-000000000003"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/conversations/"+room+"/omemo" {
			t.Errorf("unexpected engine route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprint(w, `{}`)
		case http.MethodGet:
			_, _ = fmt.Fprint(w, `{"address":"private@rooms.example.org","enabled":false,"eligible":true,"members":["alice@example.org"]}`)
		default:
			t.Errorf("unexpected engine method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer api.Close()
	s, err := New(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	initial := httptest.NewRecorder()
	token := csrfToken(initial, httptest.NewRequest(http.MethodGet, "/ui/omemo", nil))
	form := url.Values{"engineId": {engine}, "csrf": {token}, "action": {"encrypt"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/omemo?lang=pt-BR", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	v := s.baseView(req)
	v.EngineID, v.CSRF = engine, token
	response := httptest.NewRecorder()
	s.roomOmemoPage(response, req, v, account, room)
	body := response.Body.String()
	if response.Code != http.StatusConflict || !strings.Contains(body, v.Text.SharedActionFailed) ||
		!strings.Contains(body, "Ativar OMEMO na sala") || strings.Contains(body, v.Text.OmemoRoomUnavailable) {
		t.Fatalf("conflict did not keep the room action available (%d): %s", response.Code, body)
	}
}
