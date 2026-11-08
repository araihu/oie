package xmpp

import (
	"crypto/sha256"
	"encoding/hex"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const correctionNS = "urn:xmpp:message-correct:0"
const occupantIDNS = "urn:xmpp:occupant-id:0"

type correctionMetadata struct {
	Replacements []wireID `xml:"urn:xmpp:message-correct:0 replace"`
	Occupants    []wireID `xml:"urn:xmpp:occupant-id:0 occupant-id"`
}

// Correction identifiers have sender authority, unlike XEP-0461 room replies.
// An empty TargetID registers the original client's ID without editing a source.
func decodeCorrectionMetadata(meta correctionMetadata, msg stanza.Message, room, occupantVerified bool) *domain.MessageCorrection {
	if !usableID(msg.ID) || msg.From.Localpart() == "" || msg.From.Domainpart() == "" {
		return nil
	}
	author := msg.From.Bare().String()
	scope := "xmpp:correct:sender:" + author
	if room {
		if msg.From.Resourcepart() == "" || !occupantVerified || len(meta.Occupants) != 1 {
			return nil
		}
		occupant := meta.Occupants[0]
		if !usableID(occupant.ID) || utf8.RuneCountInString(occupant.ID) > 128 || occupant.Text != "" || occupant.By != "" || len(occupant.Children) != 0 {
			return nil
		}
		author = msg.From.String()
		sum := sha256.Sum256([]byte(author + "\x00" + occupant.ID))
		scope = "xmpp:correct:room:" + msg.From.Bare().String() + ":" + hex.EncodeToString(sum[:])
	}
	result := &domain.MessageCorrection{Scope: scope, Author: author, ID: msg.ID}
	if len(meta.Replacements) != 1 {
		return result
	}
	replace := meta.Replacements[0]
	if usableID(replace.ID) && replace.ID != msg.ID && replace.Text == "" && replace.By == "" && len(replace.Children) == 0 {
		result.TargetID = replace.ID
	}
	return result
}

// A matching tag is not proof: rooms must advertise XEP-0421, otherwise a
// participant could inject a pseudonym and correct another occupant's text.
func recordOccupantSupport(e *entry, s *mx.Session, address string, info disco.Info) {
	room, err := jid.Parse(address)
	if err != nil || room.Localpart() == "" || room.Resourcepart() != "" || address == e.account.Address {
		return
	}
	conference, advertised := false, false
	for _, identity := range info.Identity {
		conference = conference || (identity.Category == "conference" && identity.Type == "text")
	}
	for _, feature := range info.Features {
		advertised = advertised || feature.Var == occupantIDNS
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.session != s || e.state.State != "connected" {
		return
	}
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	if e.occupantScopes == nil {
		e.occupantScopes = map[string]bool{}
	}
	if _, exists := e.occupantScopes[address]; len(e.occupantScopes) >= maxRoomSessions && !exists {
		return
	}
	e.occupantScopes[address] = conference && advertised
}
