package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
)

func TestSetAccountLabelRetainsIdentityAndRejectsStaleChanges(t *testing.T) {
	ctx := t.Context()
	engine, _, server := newMessagingEngine(t, t.TempDir(), nil)
	registered, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Personal"})
	if err != nil {
		t.Fatal(err)
	}
	before := registered.Account
	initial, err := engine.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	rename := func(label string, status int) *engineapi.Account {
		t.Helper()
		response, err := client.SetAccountLabelWithResponse(ctx, string(before.ID), engineapi.SetAccountLabel{
			Label: label, ExpectedRevisionId: string(before.RevisionID),
		})
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode() != status {
			t.Fatalf("got %d, want %d: %s", response.StatusCode(), status, response.Body)
		}
		return response.JSON200
	}

	rename("  ", http.StatusBadRequest)
	rename(strings.Repeat("á", 121), http.StatusBadRequest)
	updated := rename("  Família  ", http.StatusOK)
	if updated == nil || updated.Label != "Família" || updated.RevisionId == string(before.RevisionID) ||
		updated.Id != string(before.ID) || updated.Address != before.Address || updated.Protocol != "xmpp" ||
		!updated.CreatedAt.Equal(before.CreatedAt) || updated.ConnectionState != engineapi.AccountConnectionStateNotConnected {
		t.Fatalf("unexpected account after rename: %+v", updated)
	}
	if replay := rename("Família", http.StatusOK); replay == nil || replay.RevisionId != updated.RevisionId {
		t.Fatalf("retry changed the account revision: %+v", replay)
	}
	rename("Stale update", http.StatusConflict)

	current, err := client.GetAccountWithResponse(ctx, updated.Id)
	if err != nil {
		t.Fatal(err)
	}
	if current.JSON200 == nil || current.JSON200.Label != updated.Label || current.JSON200.RevisionId != updated.RevisionId {
		t.Fatalf("current account differs after stale update: %+v", current.JSON200)
	}
	events, err := engine.Events(ctx, initial.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "account.updated" || events[0].Account == nil || events[0].Account.Label != updated.Label {
		t.Fatalf("expected a single durable rename event: %+v", events)
	}
}
