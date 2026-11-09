package application

import (
	"context"
	"github.com/araihu/oie/internal/domain"
	"strings"
	"unicode"
)

// ProfileProvider may read a published profile only for an address the
// application established from a direct conversation or retained author evidence.
type ProfileProvider interface {
	PublishedProfile(context.Context, domain.Account, string) (domain.SenderProfile, error)
}

func (e *Engine) MessageSenderProfile(ctx context.Context, id domain.ID) (domain.SenderProfile, error) {
	m, err := e.Message(ctx, id)
	if err != nil {
		return domain.SenderProfile{}, err
	}
	c, err := e.Conversation(ctx, m.ConversationID)
	if err != nil {
		return domain.SenderProfile{}, err
	}
	a, err := e.Account(ctx, m.AccountID)
	if err != nil {
		return domain.SenderProfile{}, err
	}
	out := domain.SenderProfile{AccountID: a.ID, ConversationID: c.ID, MessageID: m.ID, State: "not_disclosed"}
	if c.AccountID != a.ID {
		return domain.SenderProfile{}, ErrNotFound
	}
	address := m.Sender.PersonalAddress
	if m.IsOwn {
		address = a.Address
	} else if c.Kind == "direct" {
		address = c.Address
	}
	if address == "" {
		return out, nil
	}
	if e.protocols == nil {
		out.PersonalAddress = address
		out.State = "unsupported"
		return out, nil
	}
	normalized, err := e.protocols.NormalizeAddress(a.Protocol, address)
	if err != nil || normalized != address {
		return out, nil
	}
	out.PersonalAddress = address
	out.State = "unsupported"
	provider, ok := e.protocols.(ProfileProvider)
	if !ok {
		return out, nil
	}
	profile, err := provider.PublishedProfile(ctx, a, address)
	if err != nil {
		out.State = "unavailable"
		return out, nil
	}
	switch profile.State {
	case "known", "unavailable", "not_connected", "unsupported":
		out.State = profile.State
	default:
		out.State = "unavailable"
	}
	if out.State == "known" {
		name := strings.TrimSpace(profile.FullName)
		if len([]rune(name)) <= 300 && strings.IndexFunc(name, unicode.IsControl) < 0 {
			out.FullName = name
		}
	}
	return out, nil
}
