package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type retractionSupport struct {
	advertised bool
	maxAge     *time.Duration
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
		if result.maxAge == nil || age < *result.maxAge {
			result.maxAge = &age
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
		plan := application.IncomingRetraction{Scope: "xmpp:correct:sender:" + a.Address, RemoteID: target.ProviderID}
		if !e.session.StreamManagement().Enabled && featureEvidence(e.discovery, "account", mamNS) != "supported" {
			now := time.Now()
			for _, attempt := range e.retractionAttempts {
				if now.Before(attempt.expiresAt) && attempt.matches(target.ID, plan) && attempt.confirmedDirect() {
					return plan, true
				}
			}
			return empty, false
		}
		return plan, true
	}
	if c.Kind != "room" {
		return empty, false
	}
	e.historyMu.Lock()
	configuration := e.roomConfigurations[c.Address]
	e.historyMu.Unlock()
	if configuration.pending || !configuration.retraction.advertised ||
		configuration.retraction.maxAge != nil && time.Since(target.CreatedAt) > *configuration.retraction.maxAge {
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

type retractionAttempt struct {
	session    *mx.Session
	messageID  domain.ID
	kind       string
	target     jid.JID
	plan       application.IncomingRetraction
	last       *uint32
	expiresAt  time.Time
	result     chan struct{}
	resultOnce sync.Once
	err        error
}

func (a *retractionAttempt) complete(err error) {
	a.resultOnce.Do(func() {
		a.err = err
		close(a.result)
	})
}

func (a *retractionAttempt) confirmedDirect() bool {
	if a.kind == "direct" {
		select {
		case <-a.result:
			return a.err == nil
		default:
		}
	}
	return false
}

func (a *retractionAttempt) matches(id domain.ID, plan application.IncomingRetraction) bool {
	return a.messageID == id && a.plan.Scope == plan.Scope && a.plan.RemoteID == plan.RemoteID
}

const retractionRetryWindow = 2 * time.Minute

// The entry lock protects the attempt table. Concurrent retries also share
// the stream's send lock and cannot select a half-written attempt.
func (e *entry) previousRetractionLocked(targetID domain.ID, plan application.IncomingRetraction, now time.Time) (string, *retractionAttempt) {
	var id string
	var previous *retractionAttempt
	for key, attempt := range e.retractionAttempts {
		if !now.Before(attempt.expiresAt) || attempt.session != e.session && !attempt.confirmedDirect() {
			delete(e.retractionAttempts, key)
			continue
		}
		if attempt.matches(targetID, plan) {
			id, previous = key, attempt
		}
	}
	return id, previous
}

func (e *entry) resumeRetractionsLocked(previous, resumed *mx.Session, now time.Time) {
	for id, attempt := range e.retractionAttempts {
		if attempt.confirmedDirect() {
			if !now.Before(attempt.expiresAt) {
				delete(e.retractionAttempts, id)
			}
			continue
		}
		if attempt.session != previous {
			delete(e.retractionAttempts, id)
			continue
		}
		attempt.session = resumed
		attempt.expiresAt = now.Add(retractionRetryWindow)
	}
}

func (e *entry) retainRetractionProofsLocked(now time.Time) {
	for id, attempt := range e.retractionAttempts {
		if !now.Before(attempt.expiresAt) || !attempt.confirmedDirect() {
			delete(e.retractionAttempts, id)
			continue
		}
		attempt.session = nil
	}
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	e.sendMu.Lock()
	e.mu.Lock()
	current, ok := prepareRetractionLocked(e, a, c, target)
	session, raw := e.session, e.raw
	if !ok || current.Scope != plan.Scope || current.RemoteID != plan.RemoteID {
		e.mu.Unlock()
		e.sendMu.Unlock()
		return application.ErrProtocolUnavailable
	}
	now := time.Now()
	if id, previous := e.previousRetractionLocked(target.ID, plan, now); previous != nil {
		e.mu.Unlock()
		e.sendMu.Unlock()
		return m.confirmRetraction(ctx, e, session, id, previous)
	}
	id := randomQueryID()
	if id == "" || len(e.retractionAttempts) >= maxOwnRoomMessages {
		e.mu.Unlock()
		e.sendMu.Unlock()
		return application.ErrProtocolUnavailable
	}
	attempt := &retractionAttempt{
		session: session, messageID: target.ID, kind: c.Kind, target: to, plan: plan,
		expiresAt: now.Add(retractionRetryWindow), result: make(chan struct{}),
	}
	if e.retractionAttempts == nil {
		e.retractionAttempts = make(map[string]*retractionAttempt)
	}
	e.retractionAttempts[id] = attempt
	e.mu.Unlock()
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
		Message:    stanza.Message{To: to, Type: kind, ID: id},
		Body:       "A message was retracted.",
		Retraction: outboundRetractionElement{ID: plan.RemoteID},
		Fallback:   outboundFallbackElement{For: roomRetractionNS},
	}
	err = session.Encode(ctx, payload)
	status := session.StreamManagement()
	queuedForReplay := status.Resumable && errors.Is(err, mx.ErrStreamManagementOutcomeUnknown)
	if c.Kind == "direct" && status.Enabled && (err == nil || queuedForReplay) {
		last := status.Acknowledged + uint32(status.Unacknowledged)
		attempt.last = &last
	}
	if err != nil {
		if !queuedForReplay {
			e.forgetRetraction(id, attempt)
		}
		if !definitivelyUnsent(status.Enabled, err) {
			_ = raw.Close()
		}
		e.sendMu.Unlock()
		return application.ErrProtocolUnavailable
	}
	e.sendMu.Unlock()
	return m.confirmRetraction(ctx, e, session, id, attempt)
}

func (e *entry) forgetRetraction(id string, attempt *retractionAttempt) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.retractionAttempts[id] == attempt {
		delete(e.retractionAttempts, id)
	}
}

func (m *Manager) confirmRetraction(ctx context.Context, e *entry, session *mx.Session, id string, attempt *retractionAttempt) error {
	select {
	case <-attempt.result:
		return attempt.err
	default:
	}
	if attempt.kind == "room" {
		return awaitRetraction(ctx, session, attempt)
	}
	// Keep direct confirmation available for a retry of the local write.
	if attempt.last == nil {
		return m.awaitArchivedRetraction(ctx, e, session, attempt, id)
	}
	// Stream management proves submission to the account server. It cannot
	// confirm that the recipient has processed the retraction.
	if ephemeralAwaitingAck(attempt.last, session.StreamManagement().Acknowledged) {
		// A failed request may coincide with a final acknowledgement already
		// being read by the stream. The bounded wait checks both possibilities.
		_ = session.RequestStreamManagementAck(ctx)
	}
	return awaitRetraction(ctx, session, attempt)
}

func awaitRetraction(ctx context.Context, session *mx.Session, attempt *retractionAttempt) error {
	var poll <-chan time.Time
	if attempt.last != nil {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		poll = ticker.C
	}
	for {
		if attempt.last != nil && !ephemeralAwaitingAck(attempt.last, session.StreamManagement().Acknowledged) {
			// The read loop processes earlier errors before the acknowledgement.
			attempt.complete(nil)
			return attempt.err
		}
		select {
		case <-attempt.result:
			return attempt.err
		case <-ctx.Done():
			select {
			case <-attempt.result:
				return attempt.err
			default:
				return application.ErrProtocolUnavailable
			}
		case <-poll:
		}
	}
}

func (m *Manager) awaitArchivedRetraction(ctx context.Context, e *entry, session *mx.Session, attempt *retractionAttempt, id string) error {
	for range 2 {
		if release, err := m.acquireArchive(ctx, e.account.Address, archiveInteractive); err == nil {
			page, err := m.readArchivePage(ctx, e, session, e.account.Address, archivePageRequest{
				With: attempt.target.Bare().String(), Backward: true, Limit: 20,
			})
			release()
			select {
			case <-attempt.result:
				return attempt.err
			default:
			}
			if err == nil {
				for _, in := range page.Query.Messages {
					if in.IsOwn && in.Kind == "direct" && in.Address == attempt.target.Bare().String() &&
						in.ProviderID == id && in.Retraction != nil && in.Retraction.Scope == attempt.plan.Scope &&
						in.Retraction.RemoteID == attempt.plan.RemoteID {
						attempt.complete(nil)
						return attempt.err
					}
				}
			}
		}
		select {
		case <-attempt.result:
			return attempt.err
		case <-ctx.Done():
			return application.ErrProtocolUnavailable
		case <-time.After(200 * time.Millisecond):
		}
	}
	return awaitRetraction(ctx, session, attempt)
}

func rejectRetraction(e *entry, msg stanza.Message) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	attempt := e.retractionAttempts[msg.ID]
	if attempt == nil {
		return false
	}
	own, _ := jid.Parse(e.account.Address)
	if e.session != attempt.session || msg.To.String() != "" && !msg.To.Bare().Equal(own) {
		return true
	}
	fromPeer := msg.From.Bare().Equal(attempt.target.Bare()) && (attempt.kind != "room" || msg.From.Resourcepart() == "")
	if fromPeer || msg.From.Equal(attempt.target.Domain()) || msg.From.Equal(own.Domain()) {
		attempt.complete(application.ErrStateConflict)
		delete(e.retractionAttempts, msg.ID)
	}
	return true
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
	if err == nil && kind == "room" {
		e.mu.Lock()
		attempt := e.retractionAttempts[msg.ID]
		if attempt != nil && attempt.session == e.session && attempt.kind == "room" &&
			attempt.target.String() == address && attempt.plan.Scope == scope && attempt.plan.RemoteID == ref.ID {
			attempt.complete(nil)
			delete(e.retractionAttempts, msg.ID)
		}
		e.mu.Unlock()
	}
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
