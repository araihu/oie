package application

import (
	"context"
	"slices"
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
	plan := &domain.OutboundReply{TargetRevisionID: target.RevisionID, TargetBodyHash: target.SourceBodyHash(), TargetID: target.ID, SenderName: boundedReplyText(target.Sender.DisplayName, 300), Excerpt: domain.ReplyExcerpt(domain.DisplayBody(target)), BodyText: text}
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

func outgoingReplyContent(m domain.Message) (string, *domain.ReplyReference) {
	if m.Reply != nil && m.Reply.OriginalExpired {
		return m.Reply.BodyText, nil
	}
	if m.DirectEncryption != "omemo" && m.OutboundReply != nil && m.OutboundReply.Reference != nil {
		return m.OriginalText, &domain.ReplyReference{Target: *m.OutboundReply.Reference, Fallback: slices.Clone(m.OutboundReply.Fallback)}
	}
	return m.OriginalText, nil
}

// MatchesOriginalContent checks the text and reply bound at the dispatch claim.
func (out Outgoing) MatchesOriginalContent(m domain.Message) bool {
	body, reply := outgoingReplyContent(m)
	if out.OriginalText != body {
		return false
	}
	if out.Reply == nil || reply == nil {
		return out.Reply == reply
	}
	return out.Reply.Target == reply.Target && slices.Equal(out.Reply.Fallback, reply.Fallback)
}

func outgoingMessage(m domain.Message, c domain.Conversation) Outgoing {
	body, reply := outgoingReplyContent(m)
	out := Outgoing{DirectEncryption: m.DirectEncryption, ID: m.ID, Address: c.Address, OriginalText: body, Kind: c.Kind, Reply: reply}
	// Older queued OMEMO compositions can still contain native references.
	if m.DirectEncryption == "omemo" {
		return out
	}
	out.Mentions = slices.Clone(m.Mentions)
	if m.Reply != nil && m.Reply.OriginalExpired {
		return out
	}
	if m.OutboundReply != nil {
		offset := utf8.RuneCountInString(m.OriginalText) - utf8.RuneCountInString(m.OutboundReply.BodyText)
		for i := range out.Mentions {
			out.Mentions[i].Begin += offset
			out.Mentions[i].End += offset
		}
	}
	return out
}
