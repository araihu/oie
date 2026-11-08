package web

import (
	"bytes"
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"strings"
	"testing"
	"time"
)

func TestEditedEvidenceAndLateMetadataInvalidateTimeline(t *testing.T) {
	yes, no := true, false
	at := time.Date(2026, 11, 8, 12, 30, 21, 670000000, time.UTC)
	message := messageView{Message: engineapi.Message{Id: testMessage, RevisionId: testRevision, OriginalText: "Current retained text", CreatedAt: at.Add(-time.Minute)}}
	v := view{ChatID: testChat, Messages: []messageView{message}, Text: copy{Edited: "Edited"}}
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
	if strings.Count(out.String(), "data-message-edited") != 1 || !strings.Contains(out.String(), ">Edited</small>") {
		t.Fatal("missing or duplicated edited label")
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
