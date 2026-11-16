package application

import (
	"context"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type IncomingRetraction struct {
	Scope, RemoteID string
	At              time.Time
}

type messageRetractionRepository interface {
	RecordMessageRetraction(context.Context, domain.ID, string, string, IncomingRetraction) error
	MatchesMessageRetraction(context.Context, domain.Message, domain.Conversation, IncomingRetraction) (bool, error)
	RetractedOriginal(context.Context, domain.ID) (domain.Message, error)
}

type MessageRetractionProtocol interface {
	PrepareRetraction(domain.Account, domain.Conversation, domain.Message) (IncomingRetraction, bool)
	SendRetraction(context.Context, domain.Account, domain.Conversation, domain.Message, IncomingRetraction) error
}

func (e *Engine) receiveRetraction(ctx context.Context, in Incoming) error {
	repo, ok := e.repo.(messageRetractionRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	r := *in.Retraction
	if (in.Kind != "direct" && in.Kind != "room") || r.At.IsZero() ||
		!validText(r.Scope, 4096) || !validText(r.RemoteID, 4096) || strings.ContainsRune(r.RemoteID, 0) {
		return ErrInvalid
	}
	a, err := e.Account(ctx, in.AccountID)
	if err != nil {
		return err
	}
	address, err := e.canonical(a.Protocol, in.Address)
	if err != nil {
		return err
	}
	if err = repo.RecordMessageRetraction(ctx, a.ID, in.Kind, address, r); err != nil {
		return err
	}
	e.notifyEvents()
	return nil
}

func retractable(m domain.Message) bool {
	return m.IsOwn && !m.IsWithdrawn() && (m.SendState == Submitted || m.SendState == "received")
}

func (e *Engine) CanRetractMessage(ctx context.Context, a domain.Account, c domain.Conversation, m domain.Message) bool {
	p, ok := e.protocols.(MessageRetractionProtocol)
	repo, stored := e.repo.(messageRetractionRepository)
	if !ok || !stored || !retractable(m) || c.Request != "" || c.RemovedFromChats {
		return false
	}
	if c.Kind == "room" && len(m.References) == 0 {
		retained, err := e.Message(ctx, m.ID)
		if err != nil || retained.RevisionID != m.RevisionID {
			return false
		}
		m = retained
	}
	target, ok := p.PrepareRetraction(a, c, m)
	if !ok {
		return false
	}
	matches, err := repo.MatchesMessageRetraction(ctx, m, c, target)
	return err == nil && matches
}

func (e *Engine) RetractMessage(ctx context.Context, id, expected domain.ID) (domain.Message, error) {
	if !validID(id) || !validID(expected) {
		return domain.Message{}, ErrInvalid
	}
	repo, stored := e.repo.(messageRetractionRepository)
	p, supported := e.protocols.(MessageRetractionProtocol)
	if !stored || !supported {
		return domain.Message{}, ErrProtocolUnavailable
	}
	e.messageRetractionMu.Lock()
	defer e.messageRetractionMu.Unlock()

	m, err := e.Message(ctx, id)
	if err != nil {
		return m, err
	}
	if m.ID == id && m.RevisionID == expected && m.IsOwn && m.Retraction != nil && m.Moderation == nil {
		return m, nil
	}
	if m.ID != id || m.RevisionID != expected || !retractable(m) {
		return m, StateConflict{Current: m}
	}
	c, err := e.messageDestination(ctx, m.ConversationID, "")
	if err != nil {
		return m, err
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return m, err
	}
	target, ok := p.PrepareRetraction(a, c, m)
	if !ok {
		return m, ErrProtocolUnavailable
	}
	matches, err := repo.MatchesMessageRetraction(ctx, m, c, target)
	if err != nil {
		return m, err
	}
	if !matches {
		return m, ErrProtocolUnavailable
	}
	if err = p.SendRetraction(ctx, a, c, m, target); err != nil {
		return m, err
	}
	target.At = e.now().UTC()
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = repo.RecordMessageRetraction(storeCtx, a.ID, c.Kind, c.Address, target); err != nil {
		return m, err
	}
	e.notifyEvents()
	return e.Message(ctx, id)
}

func (e *Engine) RetractedOriginal(ctx context.Context, id domain.ID) (domain.Message, error) {
	repo, ok := e.repo.(messageRetractionRepository)
	if !ok {
		return domain.Message{}, ErrProtocolUnavailable
	}
	if !validID(id) {
		return domain.Message{}, ErrInvalid
	}
	return repo.RetractedOriginal(ctx, id)
}
