package httpapi_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func timelineHTTPID(n int) domain.ID {
	return domain.ID(fmt.Sprintf("019a5df0-2222-7000-8000-%012d", n))
}
func TestTimelineHTTPWalksNearestBothDirectionsWithoutEffects(t *testing.T) {
	protocol := &protocolStub{}
	e, store, server := newMessagingEngine(t, t.TempDir(), protocol)
	c := openTestConversation(t, e, "bob@localhost")
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	for i, n := range []int{9, 1, 8, 2, 7, 3, 6, 4, 5} {
		m := domain.Message{ID: timelineHTTPID(n), RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, OriginalText: fmt.Sprint(n), ProviderID: fmt.Sprint(n), CreatedAt: at.Add(time.Duration(i/3) * time.Millisecond), SendState: "received", Historical: true}
		if err := store.ReceiveMessage(t.Context(), c, m, id()); err != nil {
			t.Fatal(err)
		}
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	limit := 2
	read := func(cursor string, direction *engineapi.ListMessagesParamsDirection) *engineapi.MessagePage {
		t.Helper()
		params := &engineapi.ListMessagesParams{Limit: &limit, Direction: direction}
		if cursor != "" {
			params.Cursor = &cursor
		}
		response, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), params)
		if err != nil || response.JSON200 == nil {
			t.Fatal(response, err)
		}
		return response.JSON200
	}
	_, before, _ := store.Snapshot(t.Context())
	original, _ := e.Conversation(t.Context(), c.ID)
	var older []domain.ID
	cursor := ""
	for page := 0; page < 10; page++ {
		result := read(cursor, nil)
		for _, m := range result.Items {
			older = append(older, domain.ID(m.Id))
		}
		if result.NextCursor == nil {
			break
		}
		if len(result.Items) != limit || *result.NextCursor != result.Items[len(result.Items)-1].Id || *result.NextCursor == cursor {
			t.Fatal("older cursor did not advance by oldest edge", result)
		}
		cursor = *result.NextCursor
	}
	wantOlder := []domain.ID{timelineHTTPID(6), timelineHTTPID(5), timelineHTTPID(4), timelineHTTPID(7), timelineHTTPID(3), timelineHTTPID(2), timelineHTTPID(9), timelineHTTPID(8), timelineHTTPID(1)}
	if !reflect.DeepEqual(older, wantOlder) {
		t.Fatal("older chronology changed", older)
	}
	direction := engineapi.ListMessagesParamsDirectionNewer
	cursor = string(timelineHTTPID(1))
	var newer []domain.ID
	for page := 0; page < 10; page++ {
		result := read(cursor, &direction)
		for i := len(result.Items) - 1; i >= 0; i-- {
			newer = append(newer, domain.ID(result.Items[i].Id))
		}
		if result.NextCursor == nil {
			break
		}
		if len(result.Items) != limit || *result.NextCursor != result.Items[0].Id || *result.NextCursor == cursor {
			t.Fatal("newer cursor did not advance by newest edge", result)
		}
		cursor = *result.NextCursor
	}
	wantNewer := []domain.ID{timelineHTTPID(8), timelineHTTPID(9), timelineHTTPID(2), timelineHTTPID(3), timelineHTTPID(7), timelineHTTPID(4), timelineHTTPID(5), timelineHTTPID(6)}
	if !reflect.DeepEqual(newer, wantNewer) {
		t.Fatal("newer skipped nearest window or used UUID alone", newer)
	}
	if result := read(string(timelineHTTPID(6)), &direction); len(result.Items) != 0 || result.NextCursor != nil {
		t.Fatal("nonterminal empty page", result)
	}
	for _, params := range []*engineapi.ListMessagesParams{{Direction: &direction}, {Direction: &direction, Cursor: new(string)}} {
		response, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), params)
		if err != nil || response.StatusCode() != 400 {
			t.Fatal("newer accepted missing anchor", response, err)
		}
	}
	contextLimit := 3
	contextPage, contextErr := client.GetMessageContextWithResponse(t.Context(), string(c.ID), string(timelineHTTPID(3)), &engineapi.GetMessageContextParams{Limit: &contextLimit})
	if contextErr != nil || contextPage.JSON200 == nil || !contextPage.JSON200.HasOlder || !contextPage.JSON200.HasNewer || contextPage.JSON200.Items[0].Id != string(timelineHTTPID(7)) {
		t.Fatal("context boundary", contextPage, contextErr)
	}
	newest := contextPage.JSON200.Items[0].Id
	if page := read(newest, &direction); len(page.Items) != 2 || page.Items[1].Id != string(timelineHTTPID(4)) {
		t.Fatal("context newer edge skipped retained neighbors", page)
	}
	invalidDirection := engineapi.ListMessagesParamsDirection("sideways")
	zero := 0
	large := 101
	validCursor := string(timelineHTTPID(1))
	for _, params := range []*engineapi.ListMessagesParams{{Direction: &invalidDirection, Cursor: &validCursor}, {Direction: &direction, Cursor: &validCursor, Limit: &zero}, {Direction: &direction, Cursor: &validCursor, Limit: &large}} {
		response, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), params)
		if err != nil || response.StatusCode() != 400 {
			t.Fatal("invalid pagination accepted", response, err)
		}
	}
	_, afterWalk, _ := store.Snapshot(t.Context())
	if before != afterWalk {
		t.Fatal("timeline walk emitted events")
	}
	foreign := openTestConversation(t, e, "foreign@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: foreign.AccountID, Address: foreign.Address, ProviderID: "foreign", OriginalText: "private", Historical: true}); err != nil {
		t.Fatal(err)
	}
	messages, err := e.Messages(t.Context(), foreign.ID, "", 100)
	if err != nil || len(messages) != 1 {
		t.Fatal(messages, err)
	}
	foreignID := string(messages[0].ID)
	response, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), &engineapi.ListMessagesParams{Direction: &direction, Cursor: &foreignID})
	if err != nil || response.StatusCode() != 404 {
		t.Fatal("foreign anchor leaked", response, err)
	}
	// Foreign setup has its own event; repeated reads must add none.
	_, before, _ = store.Snapshot(t.Context())
	_ = read(string(timelineHTTPID(9)), &direction)
	_, after, _ := store.Snapshot(t.Context())
	current, _ := e.Conversation(t.Context(), c.ID)
	protocol.mu.Lock()
	sends := protocol.count
	protocol.mu.Unlock()
	if before != after || sends != 0 || !reflect.DeepEqual(original, current) {
		t.Fatal("read changed app/protocol state", before, after, sends)
	}
}
func TestTimelineHTTPNewerResolvesCorrectionAlias(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "bob@localhost")
	at := time.Now().Add(-time.Hour).UTC()
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "original", OriginalText: "original", Historical: true, CreatedAt: at, Correction: &domain.MessageCorrection{Scope: "proof:bob", Author: "bob", ID: "original"}}); err != nil {
		t.Fatal(err)
	}
	aliasID := id()
	alias := domain.Message{ID: aliasID, RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, ProviderID: "edit", OriginalText: "corrected", CreatedAt: at.Add(time.Second), SendState: "received", Historical: true, Correction: &domain.MessageCorrection{Scope: "proof:bob", Author: "bob", ID: "edit", TargetID: "original"}}
	if err := store.ReceiveMessage(t.Context(), c, alias, id()); err != nil {
		t.Fatal(err)
	}
	neighborID := id()
	neighbor := domain.Message{ID: neighborID, RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, ProviderID: "neighbor", OriginalText: "next", CreatedAt: at.Add(2 * time.Second), SendState: "received", Historical: true}
	if err := store.ReceiveMessage(t.Context(), c, neighbor, id()); err != nil {
		t.Fatal(err)
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	direction := engineapi.ListMessagesParamsDirectionNewer
	cursor := string(aliasID)
	response, err := client.ListMessagesWithResponse(t.Context(), string(c.ID), &engineapi.ListMessagesParams{Direction: &direction, Cursor: &cursor})
	if err != nil || response.JSON200 == nil || len(response.JSON200.Items) != 1 || response.JSON200.Items[0].Id != string(neighborID) {
		t.Fatal("alias cursor exposed correction row", response, err)
	}
}
