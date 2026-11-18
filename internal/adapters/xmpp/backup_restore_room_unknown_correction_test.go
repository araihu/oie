package xmpp

import (
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreSettlesRoomCorrectionWithoutOriginID(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	dir := t.TempDir()
	store, engine, manager := openDirectEchoProfile(t, dir)
	protocol := &restoredRoomEditFixtureProtocol{Manager: manager}
	engine.SetProtocols(protocol)
	account, _ := setupDirectEchoProfile(t, engine)
	const occupantID = "verified-room-occupant"
	opened, err := engine.OpenRoom(ctx, mintDirectEchoID(t), account.ID, "room@conference.localhost", "Alice", "", false)
	if err != nil {
		t.Fatal(err)
	}
	room := opened.Conversation
	intent, err := store.RoomIntent(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	protocol.intent, protocol.author = intent.IntentRevisionID, room.Address+"/Alice"
	protocol.scope = roomCorrectionScope(room.Address, protocol.author, occupantID)
	root, err := engine.SendMessage(ctx, mintDirectEchoID(t), room.ID, "native room original")
	if err != nil {
		t.Fatal(err)
	}
	for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
		if _, err := store.TransitionMessage(ctx, root.Message.ID, transition[0], transition[1], mintDirectEchoID(t), now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	previousArchive := writeRoomEchoBackup(t, dir)
	store, engine, manager = openDirectEchoProfile(t, dir)
	protocol.Manager = manager
	engine.SetProtocols(protocol)
	target, err := engine.MessageEditTarget(ctx, root.Message.ID)
	if err != nil || !target.Eligible {
		t.Fatalf("room edit fixture: %+v, %v", target, err)
	}
	key := mintDirectEchoID(t)
	const body = "Alice preserves the plain correction"
	if _, err := engine.EditMessage(ctx, key, root.Message.ID, target.CurrentRevisionID, body); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimMessageEdit(ctx, key, mintDirectEchoID(t), now); err != nil {
		t.Fatal(err)
	}
	unknown, err := store.FinishMessageEdit(ctx, key, "unknown", mintDirectEchoID(t), now, now, "unknown_outcome")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	localProfile, err := os.ReadFile(filepath.Join(dir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	observe := func(t *testing.T, receiver *application.Engine, archiveKey, author, occupant, text, metadata string) string {
		t.Helper()
		// This archive carries the ordinary correction ID and the trusted
		// occupant, with no separate XEP-0359 origin ID.
		raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s" type="groupchat" id="%s"><body>%s</body><replace xmlns="urn:xmpp:message-correct:0" id="%s"/><occupant-id xmlns="urn:xmpp:occupant-id:0" id="%s"/><x xmlns="http://jabber.org/protocol/muc#user"><item jid="%s/other-client"/></x>%s</message></forwarded></result>`,
			archiveKey, now.Format(time.RFC3339Nano), author, key, escaped(text), root.Message.ID, occupant, account.Address, metadata)
		var result archiveResult
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		in, ok, err := archivedIncoming(account, room.Address, &result, archiveTrust{SIDVerified: true, OccupantVerified: true})
		if err != nil || !ok || !in.IsOwn || in.CanonicalID != "" || in.ProviderID == string(key) || in.Correction == nil ||
			in.Correction.ID != string(key) || in.Correction.Scope != roomCorrectionScope(room.Address, author, occupant) {
			t.Fatalf("wrong room archive fixture: %+v, %v, %v", in, ok, err)
		}
		if err := receiver.Receive(t.Context(), in); err != nil {
			t.Fatal(err)
		}
		return in.ProviderID
	}

	for _, tc := range []struct {
		name, author, occupant, text, metadata string
		settles                                bool
	}{
		{name: "exact correction", author: protocol.author, occupant: occupantID, text: body, settles: true},
		{name: "different body", author: protocol.author, occupant: occupantID, text: "a different correction"},
		{name: "extra structured mention", author: protocol.author, occupant: occupantID, text: body,
			metadata: `<reference xmlns="urn:xmpp:reference:0" type="mention" uri="xmpp:alice@localhost" begin="0" end="5"/>`},
		{name: "another verified occupant", author: room.Address + "/AnotherNickname", occupant: "other-occupant", text: body},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			dest := t.TempDir()
			if err := os.WriteFile(filepath.Join(dest, "oie.sqlite"), localProfile, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := sqlite.RestoreProfileBackup(ctx, dest, previousArchive, mintDirectEchoID(t)); err != nil {
				t.Fatal(err)
			}
			store, receiver, _ := openDirectEchoProfile(t, dest)
			if prior, err := receiver.MessageEdit(ctx, key); err != nil || prior != unknown {
				t.Fatalf("earlier backup changed the unknown local outcome: %+v, %v", prior, err)
			}
			const archiveKey = "room-observed-correction"
			provider := observe(t, receiver, archiveKey, tc.author, tc.occupant, tc.text, tc.metadata)
			command, err := receiver.MessageEdit(ctx, key)
			if !tc.settles {
				if err != nil || command != unknown {
					t.Fatalf("different room evidence changed the local unknown correction: %+v, %v", command, err)
				}
				return
			}
			if err != nil || command.State != "submitted" || command.RevisionID == unknown.RevisionID {
				t.Fatalf("exact authenticated MAM correction left the command unresolved: %+v, %v", command, err)
			}
			current, err := store.Message(ctx, root.Message.ID)
			if err != nil || !current.Edited || domain.DisplayBody(current) != body || current.ProjectionSourceID == "" || current.ProjectionSourceID == key {
				t.Fatalf("settlement did not project the physical archive source: %+v, %v", current, err)
			}
			if witness, err := store.MessageSource(ctx, current.ProjectionSourceID); err != nil || witness.ProviderID != provider || witness.RevisionID != current.RevisionID {
				t.Fatalf("settlement replaced the authenticated physical source: %+v, %v", witness, err)
			}
			if _, err := store.MessageSource(ctx, key); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("settlement created a second native source: %v", err)
			}
			const repeatKey = "room-observed-correction-again"
			observe(t, receiver, repeatKey, tc.author, tc.occupant, tc.text, tc.metadata)
			db, err := sql.Open("sqlite", filepath.Join(dest, "oie.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, alias := range []string{archiveKey, repeatKey} {
				var owner domain.ID
				if err := db.QueryRowContext(ctx, `SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?`,
					account.ID, room.Address, alias).Scan(&owner); err != nil || owner != current.ProjectionSourceID {
					t.Fatalf("room archive key moved from its physical witness: %s, %v", owner, err)
				}
			}
			rows, err := store.Messages(ctx, room.ID, "", 10)
			if err != nil || len(rows) != 1 || rows[0].RevisionID != current.RevisionID {
				t.Fatalf("repeated ingress duplicated or revised the correction: %+v, %v", rows, err)
			}
			if replay, err := receiver.EditMessage(ctx, key, root.Message.ID, target.CurrentRevisionID, body); err != nil || !replay.Replayed || replay.Command != command {
				t.Fatalf("the original operation key no longer replays the verified outcome: %+v, %v", replay, err)
			}
		})
	}
}
