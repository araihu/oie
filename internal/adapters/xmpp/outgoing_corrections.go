package xmpp

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
)

// A source is eligible only in the authenticated connection which emitted it.
// This bounded memory is deliberately lost on reconnect or process restart.
type ownCorrectionSource struct {
	address, kind, sessionID, scope, author string
	bodyHash                                [32]byte
	roomIntent                              domain.ID
}

func trackCorrectionSource(e *entry, a domain.Account, out application.Outgoing) {
	if out.Correction != nil || e.correctionSession == "" {
		return
	}
	kind := out.Kind
	if kind == "" {
		kind = "direct"
	}
	key := correctionDestination(kind, out.Address)
	if e.correctionLatest == nil {
		e.correctionLatest = map[string]domain.ID{}
	}
	if _, known := e.correctionLatest[key]; !known && len(e.correctionLatest) >= maxOwnRoomMessages {
		for previous := range e.correctionLatest {
			delete(e.correctionLatest, previous)
			break
		}
	}
	// Dispatch order is authoritative even when wall-clock timestamps move back.
	// An unsupported latest payload must still make an earlier text ineligible.
	e.correctionLatest[key] = out.ID
	if out.Reply != nil || len(out.Mentions) != 0 || len(out.Attachments) != 0 {
		return
	}
	source := ownCorrectionSource{address: out.Address, kind: kind, bodyHash: sha256.Sum256([]byte(out.OriginalText)), sessionID: e.correctionSession}
	if kind == "room" {
		slot := e.rooms[out.Address]
		if slot == nil || slot.session != e.session || slot.snapshot.State != "joined" || slot.snapshot.IntentRevisionID != out.RoomIntentRevisionID {
			return
		}
		source.author = out.Address + "/" + slot.snapshot.Nickname
		source.roomIntent = out.RoomIntentRevisionID
	} else {
		source.scope, source.author = "xmpp:correct:sender:"+a.Address, a.Address
	}
	if e.correctionSources == nil {
		e.correctionSources = map[string]ownCorrectionSource{}
	}
	if len(e.correctionSources) >= maxOwnRoomMessages {
		for key := range e.correctionSources {
			delete(e.correctionSources, key)
			break
		}
	}
	e.correctionSources[string(out.ID)] = source
}

func correctionDestination(kind, address string) string { return kind + "\x00" + address }

// Own room echoes are the only source of a room correction authority proof.
// A display nickname, room-issued stanza ID or copied origin ID is insufficient.
func observeOwnCorrectionSource(e *entry, origin string, correction *domain.MessageCorrection, address, body string) {
	if correction == nil || correction.ID != origin || correction.TargetID != "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	source, ok := e.correctionSources[origin]
	if !ok || source.kind != "room" || source.address != address || source.bodyHash != sha256.Sum256([]byte(body)) || source.author != correction.Author || source.sessionID != e.correctionSession {
		return
	}
	slot := e.rooms[address]
	if slot == nil || slot.session != e.session || slot.snapshot.State != "joined" || slot.snapshot.IntentRevisionID != source.roomIntent {
		return
	}
	source.scope = correction.Scope
	e.correctionSources[origin] = source
}

func (m *Manager) PrepareCorrection(ctx context.Context, a domain.Account, c domain.Conversation, target domain.Message) (domain.OutgoingCorrection, error) {
	if ctx.Err() != nil {
		return domain.OutgoingCorrection{}, ctx.Err()
	}
	if a.Protocol != "xmpp" || c.AccountID != a.ID || target.AccountID != a.ID || target.ConversationID != c.ID || !target.IsOwn || target.Sender.ID != a.ID || target.ProviderID != string(target.ID) || target.SendState != application.Submitted || target.OutboundReply != nil || len(target.Mentions) != 0 || len(target.Attachments) != 0 {
		return domain.OutgoingCorrection{}, application.ErrProtocolUnavailable
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return domain.OutgoingCorrection{}, application.ErrProtocolUnavailable
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.session == nil || e.raw == nil || e.state.State != "connected" || e.correctionSession == "" {
		return domain.OutgoingCorrection{}, application.ErrProtocolUnavailable
	}
	source, ok := e.correctionSources[string(target.ID)]
	if !ok || source.address != c.Address || source.kind != c.Kind || source.sessionID != e.correctionSession || source.scope == "" {
		return domain.OutgoingCorrection{}, application.ErrProtocolUnavailable
	}
	plan := domain.OutgoingCorrection{TargetMessageID: target.ID, OriginalWireID: string(target.ID), Scope: source.scope, Author: source.author, SessionID: source.sessionID, RoomIntentRevisionID: source.roomIntent}
	out := application.Outgoing{ID: target.RevisionID, Address: c.Address, Kind: c.Kind, OriginalText: target.OriginalText, RoomIntentRevisionID: source.roomIntent, Correction: &plan}
	if !validFrozenCorrection(e, a, out) {
		return domain.OutgoingCorrection{}, application.ErrProtocolUnavailable
	}
	return plan, nil
}

func validCorrectionPayload(a domain.Account, out application.Outgoing) bool {
	p := out.Correction
	if p == nil || p.TargetMessageID == "" || p.OriginalWireID != string(p.TargetMessageID) || !usableID(p.OriginalWireID) || out.ID == p.TargetMessageID || p.SessionID == "" || out.Reply != nil || len(out.Mentions) != 0 {
		return false
	}
	if _, err := domain.ParseID(string(p.TargetMessageID)); err != nil {
		return false
	}
	if _, err := domain.ParseID(string(out.ID)); err != nil {
		return false
	}
	if out.Kind == "room" {
		author, err := jid.Parse(p.Author)
		return err == nil && author.Bare().String() == out.Address && author.Resourcepart() != "" && p.RoomIntentRevisionID != "" && p.RoomIntentRevisionID == out.RoomIntentRevisionID && strings.HasPrefix(p.Scope, "xmpp:correct:room:"+out.Address+":")
	}
	return (out.Kind == "direct" || out.Kind == "") && p.RoomIntentRevisionID == "" && p.Author == a.Address && p.Scope == "xmpp:correct:sender:"+a.Address
}

// Called with the entry lock held, after acquiring the same send lock used by
// original sends and room operations. A frozen plan is never rebound silently.
func validFrozenCorrection(e *entry, a domain.Account, out application.Outgoing) bool {
	if !validCorrectionPayload(a, out) {
		return false
	}
	p := out.Correction
	source, ok := e.correctionSources[string(p.TargetMessageID)]
	kind := out.Kind
	if kind == "" {
		kind = "direct"
	}
	if !ok || e.correctionLatest[correctionDestination(kind, out.Address)] != p.TargetMessageID || e.correctionSession != p.SessionID || source.sessionID != p.SessionID || source.address != out.Address || source.kind != kind || source.scope != p.Scope || source.author != p.Author || source.roomIntent != p.RoomIntentRevisionID {
		return false
	}
	if kind == "room" {
		slot := e.rooms[out.Address]
		return slot != nil && slot.session == e.session && slot.snapshot.State == "joined" && !slot.leaving && slot.snapshot.IntentRevisionID == p.RoomIntentRevisionID && p.Author == out.Address+"/"+slot.snapshot.Nickname
	}
	return true
}
