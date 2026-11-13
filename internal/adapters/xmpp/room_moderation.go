package xmpp

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const roomRetractionNS = "urn:xmpp:message-retract:1"

type roomRetraction struct {
	ID        string     `xml:"id,attr"`
	Stamp     string     `xml:"stamp,attr"`
	Moderated []struct{} `xml:"urn:xmpp:message-moderate:1 moderated"`
	Reasons   []string   `xml:"urn:xmpp:message-retract:1 reason"`
}

type legacyRoomModeration struct {
	Stamp *struct {
		At string `xml:"stamp,attr"`
	} `xml:"urn:xmpp:message-retract:0 retracted"`
	Reason string `xml:"urn:xmpp:message-moderate:0 reason"`
}

type roomModerationMetadata struct {
	LegacyTombstones []legacyRoomModeration `xml:"urn:xmpp:message-moderate:0 moderated"`
	Retractions      []roomRetraction       `xml:"urn:xmpp:message-retract:1 retract"`
	Tombstones       []roomRetraction       `xml:"urn:xmpp:message-retract:1 retracted"`
}

func moderationEvent(ref roomRetraction, id string, at time.Time) *application.IncomingRoomModeration {
	if len(ref.Moderated) != 1 || len(ref.Reasons) > 1 || !usableID(id) || at.IsZero() {
		return nil
	}
	event := &application.IncomingRoomModeration{StanzaID: id, MessageModeration: domain.MessageModeration{At: at}}
	if len(ref.Reasons) == 1 {
		event.Reason = cleanEvidence(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, ref.Reasons[0]), 500)
	}
	return event
}

func (m *Manager) receiveRoomModeration(ctx context.Context, e *entry, msg stanza.Message, meta roomModerationMetadata, stamp, delayFrom string) error {
	if msg.Type != stanza.GroupChatMessage || msg.From.Localpart() == "" || msg.From.Resourcepart() != "" ||
		len(meta.Retractions) != 1 || len(meta.Tombstones) != 0 {
		return nil
	}
	own, _ := jid.Parse(e.account.Address)
	if msg.To.String() != "" && !msg.To.Bare().Equal(own) {
		return nil
	}
	address := msg.From.String()
	e.mu.RLock()
	slot := e.rooms[address]
	joined := slot != nil && slot.session == e.session && e.session != nil && slot.snapshot.State == "joined" && !slot.leaving
	e.mu.RUnlock()
	if !joined {
		return nil
	}
	at := time.Now().UTC()
	if delayFrom == address {
		if value, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			at = value.UTC()
		}
	}
	ref := meta.Retractions[0]
	if event := moderationEvent(ref, ref.ID, at); event != nil {
		err := m.host.Receive(ctx, application.Incoming{AccountID: e.account.ID, Kind: "room", Address: address, Moderation: event})
		if !errors.Is(err, application.ErrInvalid) {
			return err
		}
	}
	return nil
}

func archivedRoomModeration(account domain.Account, scope string, r *archiveResult) (application.Incoming, bool) {
	msg := r.Forwarded.Message
	room, _ := jid.Parse(scope)
	in := application.Incoming{AccountID: account.ID, Kind: "room", Address: scope}
	if scope == account.Address || msg.Type != stanza.GroupChatMessage || !msg.From.Bare().Equal(room) {
		return in, false
	}
	at, err := time.Parse(time.RFC3339Nano, r.Forwarded.Delay.Stamp)
	if err != nil {
		return in, false
	}
	if len(msg.Retractions) == 1 && len(msg.Tombstones) == 0 && msg.From.Resourcepart() == "" {
		ref := msg.Retractions[0]
		in.Moderation = moderationEvent(ref, ref.ID, at.UTC())
		return in, in.Moderation != nil
	}
	if len(msg.Retractions) != 0 || msg.From.Resourcepart() == "" {
		return in, false
	}
	var ref roomRetraction
	switch {
	case len(msg.Tombstones) == 1 && len(msg.LegacyTombstones) == 0:
		ref = msg.Tombstones[0]
	case len(msg.Tombstones) == 0 && len(msg.LegacyTombstones) == 1 && msg.LegacyTombstones[0].Stamp != nil:
		legacy := msg.LegacyTombstones[0]
		ref.Stamp = legacy.Stamp.At
		ref.Moderated = []struct{}{{}}
		ref.Reasons = []string{legacy.Reason}
	default:
		return in, false
	}
	moderated, err := time.Parse(time.RFC3339Nano, ref.Stamp)
	if err != nil {
		return in, false
	}
	in.Moderation = moderationEvent(ref, r.ID, moderated.UTC())
	if in.Moderation == nil {
		return in, false
	}
	in.CreatedAt, in.Historical = at.UTC(), true
	in.ArchiveID, in.ArchiveScope = r.ID, scope
	in.SenderName = msg.From.Resourcepart()
	in.References = []domain.MessageReference{{Scope: "room:" + scope, ID: r.ID, Author: msg.From.String()}}
	in.ProviderID = roomProviderID(msg.From, msg.ID, in.References)
	in.Correction = decodeCorrectionMetadata(msg.correctionMetadata, msg.Message, true, false)
	in.ModerationTombstone = true
	return in, true
}
