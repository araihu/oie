package xmpp

import (
	"bytes"
	"encoding/xml"
	"net"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func correctionFixture(t *testing.T, kind string) (*Manager, *entry, domain.Conversation, domain.Message) {
	t.Helper()
	m, e, slot, _ := roomFixture(t)
	e.account.Protocol = "xmpp"
	raw, peer := net.Pipe()
	t.Cleanup(func() { _ = raw.Close(); _ = peer.Close() })
	e.raw = raw
	e.correctionSession = "authenticated-session"
	slot.snapshot.State = "joined"
	c := slot.conversation
	if kind == "direct" {
		c.Kind = "direct"
		c.Address = "bob@example.test"
	}
	target := domain.Message{ID: "019a5df0-0000-7000-8000-000000000010", RevisionID: "019a5df0-0000-7000-8000-000000000011", AccountID: e.account.ID, ConversationID: c.ID, Sender: domain.Sender{ID: e.account.ID}, IsOwn: true, SendState: application.Submitted, OriginalText: "Original café 🧑🏽‍💻"}
	target.ProviderID = string(target.ID)
	out := application.Outgoing{ID: target.ID, Address: c.Address, Kind: c.Kind, OriginalText: target.OriginalText}
	if kind == "room" {
		out.RoomIntentRevisionID = slot.snapshot.IntentRevisionID
	}
	trackCorrectionSource(e, e.account, out)
	return m, e, c, target
}

func TestOutgoingCorrectionsUseOriginalClientIDAndCompleteNewBody(t *testing.T) {
	for _, kind := range []string{"direct", "room"} {
		t.Run(kind, func(t *testing.T) {
			m, e, c, target := correctionFixture(t, kind)
			if kind == "room" {
				if _, err := m.PrepareCorrection(t.Context(), e.account, c, target); err == nil {
					t.Fatal("room target accepted without authenticated own echo")
				}
				from, _ := jid.Parse(c.Address + "/Oie")
				proof := decodeCorrectionMetadata(correctionMetadata{Occupants: []wireID{{ID: "server-occupant"}}}, stanza.Message{From: from, ID: string(target.ID)}, true, true)
				observeOwnCorrectionSource(e, string(target.ID), proof, c.Address, target.OriginalText)
			}
			plan, err := m.PrepareCorrection(t.Context(), e.account, c, target)
			if err != nil {
				t.Fatal(err)
			}
			for _, command := range []domain.ID{"019a5df0-0000-7000-8000-000000000012", "019a5df0-0000-7000-8000-000000000013"} {
				out := application.Outgoing{ID: command, Address: c.Address, Kind: c.Kind, OriginalText: "Corrected <&> 😀", Correction: &plan, RoomIntentRevisionID: plan.RoomIntentRevisionID}
				payload, err := buildOutgoing(e.account, out)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := xml.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				wire = bytes.Replace(wire, []byte("<message"), []byte(`<message xmlns="jabber:client"`), 1)
				var received archiveMessage
				if err = xml.Unmarshal(wire, &received); err != nil {
					t.Fatal(err)
				}
				if received.ID != string(command) || received.Body != out.OriginalText || len(received.Replacements) != 1 || received.Replacements[0].ID != string(target.ID) || len(received.Origins) != 1 || received.Origins[0].ID != string(command) {
					t.Fatalf("wrong correction wire: %s", wire)
				}
				want := stanza.ChatMessage
				if kind == "room" {
					want = stanza.GroupChatMessage
				}
				if received.Type != want {
					t.Fatal("correction changed message nature")
				}
			}
		})
	}
}

func TestFrozenCorrectionNeverRebindsSessionRoomActorOrDestination(t *testing.T) {
	for _, kind := range []string{"direct", "room"} {
		t.Run(kind, func(t *testing.T) {
			m, e, c, target := correctionFixture(t, kind)
			if kind == "room" {
				from, _ := jid.Parse(c.Address + "/Oie")
				proof := decodeCorrectionMetadata(correctionMetadata{Occupants: []wireID{{ID: "occupant"}}}, stanza.Message{From: from, ID: string(target.ID)}, true, true)
				observeOwnCorrectionSource(e, string(target.ID), proof, c.Address, target.OriginalText)
			}
			plan, err := m.PrepareCorrection(t.Context(), e.account, c, target)
			if err != nil {
				t.Fatal(err)
			}
			base := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000012", Address: c.Address, Kind: c.Kind, OriginalText: "New text", Correction: &plan, RoomIntentRevisionID: plan.RoomIntentRevisionID}
			if !validFrozenCorrection(e, e.account, base) {
				t.Fatal("known same-session proof rejected")
			}
			wrong := base
			wrong.Address = "other@example.test"
			if got := m.Send(t.Context(), e.account, wrong); got != application.Failed {
				t.Fatal("cross-destination correction was not terminal", got)
			}
			wrong = base
			wrong.Mentions = []domain.MessageMention{{Begin: 0, End: 3}}
			if _, err = buildOutgoing(e.account, wrong); err == nil {
				t.Fatal("correction accepted unsupported extra payload")
			}
			e.correctionSession = "new-authenticated-session"
			if got := m.Send(t.Context(), e.account, base); got != application.Failed {
				t.Fatal("correction rebound after reconnect", got)
			}
			e.correctionSession = plan.SessionID
			if kind == "room" {
				slot := e.rooms[c.Address]
				slot.snapshot.Nickname = "Other actor"
				if got := m.Send(t.Context(), e.account, base); got != application.Failed {
					t.Fatal("correction rebound to changed room nickname", got)
				}
				slot.snapshot.Nickname = "Oie"
				slot.snapshot.IntentRevisionID = "019a5df0-0000-7000-8000-000000000099"
				if got := m.Send(t.Context(), e.account, base); got != application.Failed {
					t.Fatal("correction rebound to newer join intent", got)
				}
			}
			e.state.State = "disconnected"
			if got := m.Send(t.Context(), e.account, base); got != application.NotSent {
				t.Fatal("offline correction reported external write", got)
			}
		})
	}
}

func TestPrepareCorrectionRejectsUntrackedOrForeignOwnedMessages(t *testing.T) {
	m, e, c, target := correctionFixture(t, "direct")
	for _, change := range []func(*domain.Message){func(m *domain.Message) { m.IsOwn = false }, func(m *domain.Message) { m.Sender.ID = c.ID }, func(m *domain.Message) { m.ProviderID = "external-client-id" }, func(m *domain.Message) { m.SendState = application.Unknown }, func(m *domain.Message) { m.ID = "019a5df0-0000-7000-8000-000000000099"; m.ProviderID = string(m.ID) }} {
		copy := target
		change(&copy)
		if _, err := m.PrepareCorrection(t.Context(), e.account, c, copy); err == nil {
			t.Fatal("unverified source accepted")
		}
	}
}

func TestCorrectionLatestUsesDispatchOrderIncludingUnsupportedPayloads(t *testing.T) {
	for _, kind := range []string{"direct", "room"} {
		for _, payload := range []string{"text", "reply", "mentions"} {
			t.Run(kind+"/"+payload, func(t *testing.T) {
				m, e, c, first := correctionFixture(t, kind)
				prove := func(target domain.Message) {
					if kind != "room" {
						return
					}
					from, _ := jid.Parse(c.Address + "/Oie")
					proof := decodeCorrectionMetadata(correctionMetadata{Occupants: []wireID{{ID: "occupant"}}}, stanza.Message{From: from, ID: string(target.ID)}, true, true)
					observeOwnCorrectionSource(e, string(target.ID), proof, c.Address, target.OriginalText)
				}
				prove(first)
				first.CreatedAt = time.Now().Add(time.Hour)
				plan, err := m.PrepareCorrection(t.Context(), e.account, c, first)
				if err != nil {
					t.Fatal(err)
				}
				correction := application.Outgoing{ID: "019a5df0-0000-7000-8000-000000000012", Address: c.Address, Kind: c.Kind, OriginalText: "Corrected first", RoomIntentRevisionID: plan.RoomIntentRevisionID, Correction: &plan}
				trackCorrectionSource(e, e.account, correction)
				if !validFrozenCorrection(e, e.account, correction) {
					t.Fatal("a correction incorrectly advanced the original-message marker")
				}
				second := first
				second.ID = "019a5df0-0000-7000-8000-000000000020"
				second.ProviderID = string(second.ID)
				second.RevisionID = "019a5df0-0000-7000-8000-000000000021"
				second.CreatedAt = first.CreatedAt.Add(-2 * time.Hour)
				second.OriginalText = "Later dispatch with an older wall-clock timestamp"
				out := application.Outgoing{ID: second.ID, Address: c.Address, Kind: c.Kind, OriginalText: second.OriginalText, RoomIntentRevisionID: plan.RoomIntentRevisionID}
				if payload == "reply" {
					out.Reply = &domain.ReplyReference{}
				}
				if payload == "mentions" {
					out.Mentions = []domain.MessageMention{{Begin: 0, End: 5}}
				}
				trackCorrectionSource(e, e.account, out)
				prove(second)
				if _, err = m.PrepareCorrection(t.Context(), e.account, c, first); err == nil {
					t.Fatal("older original remained editable after a newer wire attempt")
				}
				if outcome := m.Send(t.Context(), e.account, correction); outcome != application.Failed {
					t.Fatal("accepted correction was rebound across a newer original", outcome)
				}
				_, err = m.PrepareCorrection(t.Context(), e.account, c, second)
				if payload == "text" && err != nil {
					t.Fatal("latest plain text lost eligibility because its wall-clock timestamp receded", err)
				}
				if payload != "text" && err == nil {
					t.Fatal("latest unsupported payload became editable")
				}
			})
		}
	}
}
