package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

type MessageReplayRepository interface {
	ReplayAcceptedMessage(context.Context, domain.ID, string) (*domain.Message, error)
}
type OutgoingReplyProvider interface {
	SelectReplyReference(domain.Account, domain.Conversation, domain.Message) (domain.MessageReference, bool)
}

func boundedReplyText(text string, n int) string {
	r := []rune(text)
	if len(r) > n {
		return string(r[:n])
	}
	return text
}
func outboundReply(a domain.Account, c domain.Conversation, target domain.Message, text string, protocols Protocols) *domain.OutboundReply {
	sum := sha256.Sum256([]byte(target.OriginalText))
	plan := &domain.OutboundReply{TargetRevisionID: target.RevisionID, TargetBodyHash: hex.EncodeToString(sum[:]), TargetID: target.ID, SenderName: boundedReplyText(target.Sender.DisplayName, 300), Excerpt: boundedReplyText(domain.DisplayBody(target), 512), BodyText: text}
	if provider, ok := protocols.(OutgoingReplyProvider); ok {
		if ref, ok := provider.SelectReplyReference(a, c, target); ok {
			plan.Reference = &ref
		}
	}
	prefix := quotedReplyPrefix(plan)
	plan.Fallback = []domain.TextRange{{Start: 0, End: utf8.RuneCountInString(prefix)}}
	return plan
}
func quotedReplyPrefix(plan *domain.OutboundReply) string {
	name := strings.NewReplacer("\r", " ", "\n", " ").Replace(plan.SenderName)
	var b strings.Builder
	b.WriteString("> ")
	b.WriteString(name)
	b.WriteString(":\n")
	for _, line := range strings.Split(plan.Excerpt, "\n") {
		b.WriteString("> ")
		b.WriteString(strings.TrimSuffix(line, "\r"))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.String()
}
func quotedReplyBody(plan *domain.OutboundReply) string {
	return quotedReplyPrefix(plan) + plan.BodyText
}
func outgoingMessage(m domain.Message, c domain.Conversation) Outgoing {
	out := Outgoing{ID: m.ID, Address: c.Address, OriginalText: m.OriginalText, Kind: c.Kind}
	if m.OutboundReply != nil && m.OutboundReply.Reference != nil {
		out.Reply = &domain.ReplyReference{Target: *m.OutboundReply.Reference, Fallback: append([]domain.TextRange(nil), m.OutboundReply.Fallback...)}
	}
	return out
}
