package application

import (
	"github.com/araihu/oie/internal/domain"
	"testing"
	"unicode/utf8"
)

func TestIncomingRoomMentionMatchesNicknameBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"Ana", true},
		{"@ANA", true},
		{"Oi Ana!", true},
		{"Ana: tudo bem?", true},
		{"Anabela e Mariana", false},
		{"https://ana.example", false},
		{"Ana\u0301", false},
		{"Anabela, mas Ana pode responder", true},
	} {
		if got := incomingRoomMention(Incoming{Kind: "room", OwnRoomNickname: "Ana", OriginalText: tc.text}); got != tc.want {
			t.Errorf("incomingRoomMention(%q) = %t, want %t", tc.text, got, tc.want)
		}
	}
	quote := "> @Ana\n"
	in := Incoming{Kind: "room", OwnRoomNickname: "Ana", OriginalText: quote + "resposta", Reply: &domain.ReplyReference{Fallback: []domain.TextRange{{Start: 0, End: len(quote)}}}}
	if incomingRoomMention(in) {
		t.Fatal("quoted nickname counted as a new mention")
	}
}

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
