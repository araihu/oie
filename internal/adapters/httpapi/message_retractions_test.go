package httpapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestRetractionHidesContentUntilExplicitlyRequested(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	ctx := t.Context()
	c := openTestConversation(t, e, "alice@localhost")
	other := openTestConversation(t, e, "carol@localhost")
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	const wireID, secret = "wire-id", "telescope https://example.test/telescope.pdf"
	const recordingURL, recordingName = "https://example.test/voice", "recording.ogg"
	const author = "bob@localhost"
	scope := "xmpp:correct:sender:" + author
	at := time.Now().UTC()

	retract := func(scope string) {
		t.Helper()
		err := e.Receive(ctx, application.Incoming{
			AccountID: c.AccountID, Kind: "direct", Address: c.Address,
			Retraction: &application.IncomingRetraction{Scope: scope, RemoteID: wireID, At: at.Add(time.Second)},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	retract("xmpp:correct:sender:forged@localhost")
	for _, conversation := range []domain.Conversation{c, other} {
		err = e.Receive(ctx, application.Incoming{
			AccountID: conversation.AccountID, Kind: "direct", Address: conversation.Address, CreatedAt: at,
			OriginalText: secret, ProviderID: wireID,
			FileReferences: []domain.SharedReference{{Kind: "audio", URL: recordingURL, FileName: recordingName}},
			AudioURLs:      []string{recordingURL},
			Correction:     &domain.MessageCorrection{Scope: scope, Author: author, ID: wireID},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	messages, err := e.Messages(ctx, c.ID, "", 10)
	if err != nil || len(messages) != 1 || messages[0].OriginalText != secret {
		t.Fatalf("wrong sender suppressed a message: %+v, %v", messages, err)
	}
	m := messages[0]
	err = store.IndexSharedContent(ctx, []domain.SharedContentMutation{{
		Message: m, ParserVersion: application.SharedContentVersion,
		References: []domain.SharedReference{{ID: id(), RevisionID: id(), Kind: "document", URL: "https://example.test/telescope.pdf", FileName: "telescope.pdf"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	retract(scope)
	retract(scope)
	messages, err = e.Messages(ctx, other.ID, "", 10)
	if err != nil || len(messages) != 1 || messages[0].OriginalText != secret {
		t.Fatalf("retraction crossed a conversation: %+v, %v", messages, err)
	}
	projected, err := client.GetMessageWithResponse(ctx, string(m.ID))
	if err != nil || projected.JSON200 == nil || projected.JSON200.Retraction == nil || projected.JSON200.OriginalText != "" || projected.JSON200.AudioAttachmentUrls != nil {
		t.Fatalf("retracted timeline projection: %+v, %v", projected, err)
	}
	revealed, err := client.GetRetractedOriginalWithResponse(ctx, string(m.ID))
	if err != nil || revealed.JSON200 == nil || revealed.JSON200.OriginalText != secret {
		t.Fatalf("retained original: %+v, %v", revealed, err)
	}
	hiddenHistory, err := client.GetMessageEditHistoryWithResponse(ctx, string(m.ID), nil)
	if err != nil || hiddenHistory.StatusCode() != 404 {
		t.Fatalf("withdrawn history without explicit opt-in: %+v, %v", hiddenHistory, err)
	}
	include := true
	history, err := client.GetMessageEditHistoryWithResponse(ctx, string(m.ID), &engineapi.GetMessageEditHistoryParams{IncludeRetracted: &include})
	if err != nil || history.JSON200 == nil || history.JSON200.Retraction == nil || len(history.JSON200.Items) != 1 || history.JSON200.Items[0].OriginalText != secret {
		t.Fatalf("retained revision and retraction status: %+v, %v", history, err)
	}
	if links := history.JSON200.Items[0].AttachmentLinks; links == nil || len(*links) != 1 || (*links)[0].Url != recordingURL || (*links)[0].FileName != recordingName {
		t.Fatalf("retained structured attachment links: %+v", links)
	}
	conversation := string(c.ID)
	for _, includeEditHistory := range []bool{false, true} {
		for _, includeRetracted := range []bool{false, true} {
			results, err := client.SearchMessagesWithResponse(ctx, &engineapi.SearchMessagesParams{
				Q: "telescope", ConversationId: &conversation, IncludeEditHistory: &includeEditHistory, IncludeRetracted: &includeRetracted,
			})
			if err != nil || results.JSON200 == nil || (len(results.JSON200.Items) == 1) != includeRetracted {
				t.Fatalf("search with history=%t retracted=%t: %+v, %v", includeEditHistory, includeRetracted, results, err)
			}
			if includeRetracted && (!strings.Contains(results.JSON200.Items[0].Excerpt, "telescope") || results.JSON200.Items[0].Message.OriginalText != "") {
				t.Fatalf("explicit search excerpt: %+v", results.JSON200.Items[0])
			}
		}
	}
	for _, includeRetracted := range []bool{false, true} {
		kind, counts := engineapi.ListSharedContentParamsTypeDocuments, true
		result, err := client.ListSharedContentWithResponse(ctx, &engineapi.ListSharedContentParams{
			Type: &kind, ConversationId: &conversation, IncludeRetracted: &includeRetracted, IncludeFirstPageCounts: &counts,
		})
		if err != nil || result.JSON200 == nil || (len(result.JSON200.Items) == 1) != includeRetracted ||
			result.JSON200.FirstPageCounts == nil || (result.JSON200.FirstPageCounts.Documents == 1) != includeRetracted {
			t.Fatalf("catalog with retracted=%t: %+v, %v", includeRetracted, result, err)
		}
	}

}
