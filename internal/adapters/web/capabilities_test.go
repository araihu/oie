package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestCapabilityEvidenceRequiresScopedExplicitRows(t *testing.T) {
	for _, tc := range []struct {
		name, account string
		legacy, fail  bool
	}{
		{name: "explicit rows", account: testAccount},
		{name: "foreign account", account: testRevision},
		{name: "old boolean projection", account: testAccount, legacy: true},
		{name: "failed read", account: testAccount, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/accounts/"+testAccount+"/capabilities" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.fail {
					w.WriteHeader(503)
					return
				}
				if tc.legacy {
					_, _ = w.Write([]byte(`{"accountId":"` + tc.account + `","directText":false,"state":"known","reason":"adapter_connected"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(engineapi.Capabilities{AccountId: tc.account, Features: []engineapi.CapabilityFeature{{Id: "direct_text", AppSupport: "supported", ProtocolSupport: "supported", ServerSupport: "unknown", Scope: "account"}}})
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			v := view{AccountPage: "capabilities", ManagedAccount: testAccount, Accounts: []engineapi.Account{{Id: testAccount, Address: "alice@example.test"}}}
			ui.loadCapabilities(context.Background(), &v)
			if (v.CapabilityEvidence != nil) != (tc.name == "explicit rows") {
				t.Fatalf("unscoped or inferred evidence accepted: %#v", v.CapabilityEvidence)
			}
		})
	}
}
func TestCapabilityLabelsAndNavigationKeepEvidenceDistinct(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	v := view{Locale: "pt-BR", Theme: "dark", Text: catalogs["pt-BR"], AccountPage: "capabilities", ManagedAccount: testAccount, ChatOpen: true, ChatID: testChat, AccountFilter: testAccount, FocusMessage: testMessage, Query: "Busca 😀", Filter: "unread", Section: "chats"}
	if v.supportLabel("unknown", "server") == v.supportLabel("unsupported", "server") || v.supportLabel("not_advertised", "server") == v.supportLabel("unsupported", "server") {
		t.Fatal("unknown or missing advertisement became unsupported")
	}
	if v.supportLabel("unsupported", "app") == v.supportLabel("unsupported", "protocol") {
		t.Fatal("app implementation state became protocol denial")
	}
	if !strings.Contains(v.capabilitiesHref(testAccount), "message="+testMessage) || !strings.Contains(v.capabilitiesHref(testAccount), "lang=pt-BR") || !strings.Contains(v.capabilityBackHref(), "accountFocus=capabilities") {
		t.Fatal("capability navigation lost selected context or focus destination")
	}
}

func TestUnknownAccountDoesNotReadCapabilityEvidence(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unknown account triggered read %s", r.URL)
		w.WriteHeader(500)
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	v := view{AccountPage: "capabilities", ManagedAccount: testAccount}
	ui.loadCapabilities(context.Background(), &v)
	if v.CapabilityEvidence != nil {
		t.Fatal("unknown account has evidence")
	}
}
