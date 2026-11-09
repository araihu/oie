package application

import (
	"github.com/araihu/oie/internal/domain"
	"testing"
	"unicode/utf8"
)

func TestMentionRangesFollowReplyPrefixWithoutChangingAcceptedRanges(t *testing.T) {
	authored := "😀 @Juliet"
	prefix := "> Old 🍰\n\n"
	mention := domain.MessageMention{MentionSelection: domain.MentionSelection{ParticipantID: "opaque", Begin: 2, End: 9}, ReferenceMode: "structured", URI: "xmpp:juliet@example.test"}
	m := domain.Message{OriginalText: prefix + authored, Mentions: []domain.MessageMention{mention}, OutboundReply: &domain.OutboundReply{BodyText: authored}}
	outgoing := outgoingMessage(m, domain.Conversation{Kind: "room"})
	if outgoing.Mentions[0].Begin != 2+utf8.RuneCountInString(prefix) || m.Mentions[0].Begin != 2 {
		t.Fatal("wire offset mutated stored authored ranges", outgoing, m)
	}
	for _, selections := range [][]domain.MentionSelection{
		{{ParticipantID: "x", Begin: -1, End: 2}}, {{ParticipantID: "x", Begin: 2, End: 20}}, {{ParticipantID: "x", Begin: 2, End: 4}, {ParticipantID: "x", Begin: 3, End: 5}}, {{ParticipantID: "", Begin: 0, End: 1}},
	} {
		if validateMentionSelections(authored, selections) == nil {
			t.Fatal("invalid range accepted", selections)
		}
	}
}
