package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type retractionSupport struct {
	advertised bool
	maxAge     time.Duration
}

func readRetractionSupport(info disco.Info) retractionSupport {
	result := retractionSupport{}
	for _, feature := range info.Features {
		result.advertised = result.advertised || feature.Var == roomRetractionNS
	}
	for _, f := range info.Form {
		types, _ := f.Raw("FORM_TYPE")
		if !slices.Contains(types, roomRetractionNS) {
			continue
		}
		raw, ok := f.Raw("max-age")
		if !ok {
			continue
		}
		if len(raw) != 1 {
			return retractionSupport{}
		}
		seconds, err := strconv.ParseInt(raw[0], 10, 32)
		if err != nil || seconds < 0 {
			return retractionSupport{}
		}
		age := time.Duration(seconds) * time.Second
		if result.maxAge == 0 || age < result.maxAge {
			result.maxAge = age
		}
	}
	return result
}

func (m *Manager) PrepareRetraction(a domain.Account, c domain.Conversation, target domain.Message) (application.IncomingRetraction, bool) {
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return application.IncomingRetraction{}, false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return prepareRetractionLocked(e, a, c, target)
}

func prepareRetractionLocked(e *entry, a domain.Account, c domain.Conversation, target domain.Message) (application.IncomingRetraction, bool) {
	empty := application.IncomingRetraction{}
	if e.session == nil || e.raw == nil || e.state.State != "connected" || a.Protocol != "xmpp" ||
		a.ID != c.AccountID || target.AccountID != a.ID || target.ConversationID != c.ID ||
		!target.IsOwn || target.Sender.ID != a.ID || target.IsWithdrawn() {
		return empty, false
	}
	if c.Kind == "direct" && usableID(target.ProviderID) {
		return application.IncomingRetraction{Scope: "xmpp:correct:sender:" + a.Address, RemoteID: target.ProviderID}, true
	}
	if c.Kind != "room" {
		return empty, false
	}
	e.historyMu.Lock()
	configuration := e.roomConfigurations[c.Address]
	e.historyMu.Unlock()
	if configuration.pending || !configuration.retraction.advertised ||
		configuration.retraction.maxAge > 0 && time.Since(target.CreatedAt) > configuration.retraction.maxAge {
		return empty, false
	}
	slot := e.rooms[c.Address]
	if slot == nil || slot.session != e.session || slot.snapshot.State != "joined" || slot.leaving || slot.ownOccupantID == "" {
		return empty, false
	}
	ref, ok := selectReplyReference(a, c, target)
	author := c.Address + "/" + slot.snapshot.Nickname
	if !ok || ref.Author != author {
		return empty, false
	}
	return application.IncomingRetraction{Scope: roomCorrectionScope(c.Address, author, slot.ownOccupantID), RemoteID: ref.ID}, true
}

type outboundRetractionElement struct {
	XMLName xml.Name `xml:"urn:xmpp:message-retract:1 retract"`
	ID      string   `xml:"id,attr"`
}

func (m *Manager) SendRetraction(ctx context.Context, a domain.Account, c domain.Conversation, target domain.Message, plan application.IncomingRetraction) error {
	to, err := jid.Parse(c.Address)
	if err != nil {
		return application.ErrInvalid
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return application.ErrProtocolUnavailable
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	e.mu.RLock()
	current, ok := prepareRetractionLocked(e, a, c, target)
	session, raw := e.session, e.raw
	e.mu.RUnlock()
	if !ok || current.Scope != plan.Scope || current.RemoteID != plan.RemoteID {
		return application.ErrProtocolUnavailable
	}
	kind := stanza.ChatMessage
	if c.Kind == "room" {
		kind = stanza.GroupChatMessage
	}
	payload := struct {
		stanza.Message
		Body       string `xml:"body"`
		Retraction outboundRetractionElement
		Fallback   outboundFallbackElement
		Store      storeHint
	}{
		Message:    stanza.Message{To: to, Type: kind, ID: "oie-retraction-" + string(target.ID)},
		Body:       "A message was retracted.",
		Retraction: outboundRetractionElement{ID: plan.RemoteID},
		Fallback:   outboundFallbackElement{For: roomRetractionNS},
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := session.Encode(ctx, payload); err != nil {
		if !definitivelyUnsent(session.StreamManagement().Enabled, err) {
			_ = raw.Close()
		}
		return application.ErrProtocolUnavailable
	}
	return nil
}

func (m *Manager) receiveSenderRetraction(ctx context.Context, e *entry, msg stanza.Message, meta roomModerationMetadata, correction correctionMetadata, stamp, delayFrom string) error {
	if len(meta.Retractions) != 1 || len(meta.Tombstones) != 0 || msg.From.Localpart() == "" {
		return nil
	}
	ref := meta.Retractions[0]
	if !validSenderRetraction(ref) || ref.ID == msg.ID {
		return nil
	}
	own, _ := jid.Parse(e.account.Address)
	if msg.To.String() != "" && !msg.To.Bare().Equal(own) {
		return nil
	}
	kind, address, scope := "direct", msg.From.Bare().String(), "xmpp:correct:sender:"+msg.From.Bare().String()
	at := time.Now().UTC()
	if msg.Type == stanza.GroupChatMessage {
		kind = "room"
		e.mu.RLock()
		slot := e.rooms[address]
		joined := slot != nil && slot.session == e.session && e.session != nil && slot.snapshot.State == "joined" && !slot.leaving
		e.mu.RUnlock()
		e.historyMu.Lock()
		trusted := e.occupantScopes[address] && e.sidScopes[address]
		e.historyMu.Unlock()
		identity := decodeCorrectionMetadata(correction, msg, true, trusted)
		if !joined || identity == nil {
			return nil
		}
		scope = identity.Scope
		if delayFrom == address {
			if delayed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				at = delayed.UTC()
			}
		}
	} else if msg.Type != "" && msg.Type != stanza.ChatMessage && msg.Type != stanza.NormalMessage || address == e.account.Address {
		return nil
	}
	err := m.host.Receive(ctx, application.Incoming{AccountID: e.account.ID, Kind: kind, Address: address,
		Retraction: &application.IncomingRetraction{RemoteID: ref.ID, Scope: scope, At: at}})
	if errors.Is(err, application.ErrInvalid) {
		return nil
	}
	return err
}

func validSenderRetraction(ref roomRetraction) bool {
	return usableID(ref.ID) && len(ref.Moderated) == 0 && len(ref.Reasons) == 0 && len(ref.Other) == 0 && ref.Text == ""
}

func archivedSenderRetraction(in application.Incoming, r *archiveResult, roomVerified bool) (application.Incoming, bool, error) {
	msg := r.Forwarded.Message
	if in.Correction == nil || (in.Kind == "room" && !roomVerified) {
		return in, false, nil
	}
	result := &application.IncomingRetraction{Scope: in.Correction.Scope, At: in.CreatedAt}
	switch {
	case len(msg.Retractions) == 1 && len(msg.Tombstones) == 0 && validSenderRetraction(msg.Retractions[0]):
		result.RemoteID = msg.Retractions[0].ID
		if result.RemoteID == msg.ID {
			return in, false, nil
		}
	case len(msg.Tombstones) == 1 && len(msg.Retractions) == 0 && validSenderRetraction(msg.Tombstones[0]):
		tombstone := msg.Tombstones[0]
		result.RemoteID = msg.ID
		if in.Kind == "room" {
			result.RemoteID = r.ID
		}
		if at, err := time.Parse(time.RFC3339Nano, tombstone.Stamp); err == nil {
			result.At = at.UTC()
		}
	default:
		return in, false, nil
	}
	in.Retraction = result
	return in, true, nil
}
