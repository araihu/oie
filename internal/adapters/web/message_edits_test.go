package web

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"golang.org/x/net/html"
)

func TestEditedEvidenceAndLateMetadataInvalidateTimeline(t *testing.T) {
	yes, no := true, false
	at := time.Date(2026, 11, 8, 12, 30, 21, 670000000, time.UTC)
	message := messageView{Message: engineapi.Message{Id: testMessage, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat, OriginalText: "Current retained text", CreatedAt: at.Add(-time.Minute)}}
	v := view{EngineID: testEngine, ChatID: testChat, Locale: "en", Messages: []messageView{message}, Text: copy{Edited: "Edited", ViewEditHistory: "View edit history"}}
	before := v.timelineVersion()
	v.Messages[0].EditedAt = &at
	if messageEdited(v.Messages[0]) {
		t.Fatal("timestamp alone inferred an edit")
	}
	v.Messages[0].Edited = &no
	if messageEdited(v.Messages[0]) {
		t.Fatal("explicit false inferred an edit")
	}
	v.Messages[0].Edited = &yes
	if v.timelineVersion() == before {
		t.Fatal("late edit metadata failed to refresh existing canonical row")
	}
	if messageEditedAt(v.Messages[0]) != "2026-11-08T12:30:21.67Z" {
		t.Fatal("UTC edit evidence changed")
	}
	var out bytes.Buffer
	if err := messageBody(v, v.Messages[0]).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "data-message-edited") != 1 {
		t.Fatal("missing or duplicated edited label")
	}
	document, err := html.Parse(strings.NewReader(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	var edited *html.Node
	var find func(*html.Node)
	find = func(node *html.Node) {
		for _, attr := range node.Attr {
			if attr.Key == "data-message-edited" {
				edited = node
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(document)
	attrs := map[string]string{}
	for _, attr := range edited.Attr {
		attrs[attr.Key] = attr.Val
	}
	if edited.Data != "a" || attrs["aria-label"] != v.Text.ViewEditHistory || attrs["data-edited-at"] != messageEditedAt(v.Messages[0]) {
		t.Fatal("edited evidence lost its accessible history link or authored date")
	}
	if _, ok := attrs["data-open-message-history"]; !ok {
		t.Fatal("edited evidence cannot open history without reloading the conversation")
	}
	href, err := url.Parse(attrs["href"])
	if err != nil {
		t.Fatal(err)
	}
	q := href.Query()
	if href.Path != "/ui/message-history" || q.Get("engineId") != testEngine || q.Get("accountId") != testAccount || q.Get("conversationId") != testChat || q.Get("messageId") != testMessage || q.Get("lang") != "en" {
		t.Fatal("edited history link lost canonical source scope")
	}
	var text strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(edited)
	if text.String() != v.Text.Edited {
		t.Fatal("edited evidence label changed")
	}
	if !v.Messages[0].CreatedAt.Equal(message.CreatedAt) || v.Messages[0].OriginalText != message.OriginalText {
		t.Fatal("presentation changed original creation time/content")
	}
	newer := at.Add(time.Second)
	version := v.timelineVersion()
	v.Messages[0].EditedAt = &newer
	if v.timelineVersion() == version {
		t.Fatal("changed correction timestamp failed to refresh label metadata")
	}
}
