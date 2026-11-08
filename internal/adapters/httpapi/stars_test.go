package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestStarHTTPContractReplaysConflictsAndPersists(t *testing.T) {
	dir := t.TempDir()
	protocol := &protocolStub{}
	e, store, server := newMessagingEngine(t, dir, protocol)
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: "https://example.test/one.png https://example.test/two.png", ProviderID: "source"}); err != nil {
		t.Fatal(err)
	}
	items, err := e.Messages(t.Context(), c.ID, "", 50)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	m := items[0]
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := client.GetMessageStarWithResponse(t.Context(), string(m.ID))
	if err != nil || initial.JSON200 == nil || initial.JSON200.Starred || initial.JSON200.RevisionId != nil || initial.JSON200.UpdatedAt != nil || initial.JSON200.ConversationId != string(c.ID) {
		t.Fatal("untouched star state", initial, err)
	}
	var raw map[string]any
	if err = json.Unmarshal(initial.Body, &raw); err != nil {
		t.Fatal(err)
	}
	if value, present := raw["revisionId"]; !present || value != nil {
		t.Fatal("initial precondition must be explicit null")
	}
	key := string(id())
	request := engineapi.SetMessageStarJSONRequestBody{Starred: true}
	params := &engineapi.SetMessageStarParams{IdempotencyKey: key}
	first, err := client.SetMessageStarWithResponse(t.Context(), string(m.ID), params, request)
	if err != nil || first.JSON200 == nil || !first.JSON200.Starred || first.JSON200.RevisionId == nil || first.JSON200.UpdatedAt == nil {
		t.Fatal("star acceptance", first, err)
	}
	stale, err := client.SetMessageStarWithResponse(t.Context(), string(m.ID), &engineapi.SetMessageStarParams{IdempotencyKey: string(id())}, engineapi.SetMessageStarJSONRequestBody{Starred: false})
	if err != nil || stale.StatusCode() != 409 || stale.ApplicationproblemJSON409 == nil || stale.ApplicationproblemJSON409.Code != "state_conflict" || stale.ApplicationproblemJSON409.CurrentStar == nil || !reflect.DeepEqual(stale.ApplicationproblemJSON409.CurrentStar, first.JSON200) {
		t.Fatal("stale state must return the current independent star", stale, err)
	}
	removed, err := client.SetMessageStarWithResponse(t.Context(), string(m.ID), &engineapi.SetMessageStarParams{IdempotencyKey: string(id())}, engineapi.SetMessageStarJSONRequestBody{Starred: false, ExpectedRevisionId: first.JSON200.RevisionId})
	if err != nil || removed.JSON200 == nil || removed.JSON200.Starred || *removed.JSON200.RevisionId == *first.JSON200.RevisionId {
		t.Fatal("unstar revision", removed, err)
	}
	replayed, err := client.SetMessageStarWithResponse(t.Context(), string(m.ID), params, request)
	if err != nil || !reflect.DeepEqual(replayed.JSON200, first.JSON200) {
		t.Fatal("retry must retain original snapshot", replayed, err)
	}
	current, err := client.GetMessageStarWithResponse(t.Context(), string(m.ID))
	if err != nil || !reflect.DeepEqual(current.JSON200, removed.JSON200) {
		t.Fatal("replay revived an old star", current, err)
	}
	changed, err := client.SetMessageStarWithResponse(t.Context(), string(m.ID), params, engineapi.SetMessageStarJSONRequestBody{Starred: true, ExpectedRevisionId: removed.JSON200.RevisionId})
	if err != nil || changed.StatusCode() != 409 || changed.ApplicationproblemJSON409 == nil || changed.ApplicationproblemJSON409.Code != "idempotency_conflict" {
		t.Fatal("operation reuse accepted different input", changed, err)
	}
	for _, body := range []string{`{"starred":true}`, `{"expectedRevisionId":null}`, `{"starred":true,"expectedRevisionId":"bad"}`, `{"starred":true,"expectedRevisionId":null,"foreign":true}`} {
		req, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/messages/"+string(m.ID)+"/star", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", string(id()))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid star body accepted: status=%d body=%s", res.StatusCode, body)
		}
	}
	if pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour)); err != nil || len(pending) != 0 {
		t.Fatal("star created protocol send work", err)
	}
	if protocol.count != 0 {
		t.Fatal("star sent provider traffic")
	}
	before, err := e.Message(t.Context(), m.ID)
	if err != nil || !reflect.DeepEqual(before, m) {
		t.Fatal("star edited the source", err)
	}
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	e2, _, server2 := newMessagingEngine(t, dir, &protocolStub{})
	client2, err := engineapi.NewClientWithResponses(server2.URL)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := client2.GetMessageStarWithResponse(t.Context(), string(m.ID))
	if err != nil || !reflect.DeepEqual(restored.JSON200, removed.JSON200) {
		t.Fatal("star lost on restart", restored, err)
	}
	events, err := e2.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	stars := 0
	for _, event := range events {
		if event.Type == "message.star_changed" {
			stars++
			if event.Star == nil || event.Star.MessageID != m.ID || event.Star.AccountID != c.AccountID {
				t.Fatal("star event lost source context")
			}
		}
	}
	if stars != 2 {
		t.Fatal("replay/conflict duplicated events", stars)
	}
}

func TestStarredMessagePagingAndBatchScope(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	a := openTestConversation(t, e, "alice@localhost")
	b := openTestConversation(t, e, "carol@localhost")
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	all := []domain.Message{}
	for i, offset := range []int{1, 5, 2, 5, 4, 3} {
		c := a
		if i >= 4 {
			c = b
		}
		m := domain.Message{ID: id(), RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob"}, OriginalText: fmt.Sprintf("retained-%d", i), CreatedAt: base.Add(time.Duration(offset) * time.Hour), SendState: "received", ProviderID: string(id()), Historical: true}
		if err = store.ReceiveMessage(t.Context(), c, m, id()); err != nil {
			t.Fatal(err)
		}
		if _, err = e.SetMessageStar(t.Context(), id(), m.ID, "", true); err != nil {
			t.Fatal(err)
		}
		all = append(all, m)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	limit := 2
	page, err := client.ListStarredMessagesWithResponse(t.Context(), &engineapi.ListStarredMessagesParams{Limit: &limit})
	if err != nil || page.JSON200 == nil || len(page.JSON200.Items) != 2 || page.JSON200.NextCursor == nil {
		t.Fatal("first starred page", page, err)
	}
	for i, item := range page.JSON200.Items {
		if item.Message.Id != string(all[i].ID) || item.Star.MessageId != item.Message.Id || item.Conversation.Id != item.Message.ConversationId {
			t.Fatal("star order/context follows source UTC and UUID, not star time")
		}
	}
	cursorItem := page.JSON200.Items[1]
	if _, err = e.SetMessageStar(t.Context(), id(), domain.ID(cursorItem.Message.Id), domain.ID(*cursorItem.Star.RevisionId), false); err != nil {
		t.Fatal(err)
	}
	position := 2
	cursor := page.JSON200.NextCursor
	for cursor != nil {
		next, err := client.ListStarredMessagesWithResponse(t.Context(), &engineapi.ListStarredMessagesParams{Limit: &limit, Cursor: cursor})
		if err != nil || next.JSON200 == nil {
			t.Fatal("unstarred cursor invalidated paging", next, err)
		}
		for _, item := range next.JSON200.Items {
			if position >= len(all) || item.Message.Id != string(all[position].ID) {
				t.Fatal("star paging skipped or duplicated a source")
			}
			position++
		}
		cursor = next.JSON200.NextCursor
	}
	if position != len(all) {
		t.Fatal("incomplete star pages", position)
	}
	account := string(b.AccountID)
	scoped, err := client.ListStarredMessagesWithResponse(t.Context(), &engineapi.ListStarredMessagesParams{AccountId: &account})
	if err != nil || scoped.JSON200 == nil || len(scoped.JSON200.Items) != 2 {
		t.Fatal("account filter", scoped, err)
	}
	for _, item := range scoped.JSON200.Items {
		if item.Message.AccountId != account {
			t.Fatal("account filter leaked another account")
		}
	}
	foreignCursor := string(all[0].ID)
	bad, err := client.ListStarredMessagesWithResponse(t.Context(), &engineapi.ListStarredMessagesParams{AccountId: &account, Cursor: &foreignCursor})
	if err != nil || bad.StatusCode() != 409 || bad.ApplicationproblemJSON409 == nil || bad.ApplicationproblemJSON409.Code != "invalid_cursor" {
		t.Fatal("foreign cursor accepted", bad, err)
	}
	wrongChat := string(a.ID)
	bad, err = client.ListStarredMessagesWithResponse(t.Context(), &engineapi.ListStarredMessagesParams{AccountId: &account, ConversationId: &wrongChat})
	if err != nil || bad.StatusCode() != 400 {
		t.Fatal("contradictory source filters accepted", bad, err)
	}
	ids := []engineapi.ID{string(all[0].ID), string(all[0].ID)}
	batch, err := client.ListConversationMessageStarsWithResponse(t.Context(), string(a.ID), &engineapi.ListConversationMessageStarsParams{MessageId: ids})
	if err != nil || batch.JSON200 == nil || len(batch.JSON200.Items) != 1 {
		t.Fatal("duplicate batch IDs", batch, err)
	}
	ids = append(ids, scoped.JSON200.Items[0].Message.Id)
	batch, err = client.ListConversationMessageStarsWithResponse(t.Context(), string(a.ID), &engineapi.ListConversationMessageStarsParams{MessageId: ids})
	if err != nil || batch.StatusCode() != 400 {
		t.Fatal("cross-conversation batch accepted", batch, err)
	}
}
