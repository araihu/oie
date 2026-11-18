package xmpp

import (
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRoomCorrectionReplayPreservesScopedSource(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	sourceDir := t.TempDir()
	source, sender, manager := openDirectEchoProfile(t, sourceDir)
	protocol := &restoredRoomEditFixtureProtocol{Manager: manager}
	sender.SetProtocols(protocol)
	account, _ := setupDirectEchoProfile(t, sender)
	opened, err := sender.OpenRoom(ctx, mintDirectEchoID(t), account.ID, "room@conference.localhost", "Alice", "", false)
	if err != nil {
		t.Fatal(err)
	}
	room := opened.Conversation
	intent, err := source.RoomIntent(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	const occupant = "verified-first-occupant"
	protocol.intent, protocol.author = intent.IntentRevisionID, room.Address+"/Alice"
	protocol.scope = roomCorrectionScope(room.Address, protocol.author, occupant)
	sent, err := sender.SendMessage(ctx, mintDirectEchoID(t), room.ID, "native room original")
	if err != nil {
		t.Fatal(err)
	}
	original := sent.Message.ID
	for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
		if _, err := source.TransitionMessage(ctx, original, transition[0], transition[1], mintDirectEchoID(t), now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	seed := writeRoomEchoBackup(t, sourceDir)

	source, sender, manager = openDirectEchoProfile(t, sourceDir)
	protocol.Manager = manager
	sender.SetProtocols(protocol)
	key := mintDirectEchoID(t)
	const body = "verified corrected message"
	target, err := sender.MessageEditTarget(ctx, original)
	if err != nil || !target.Eligible {
		t.Fatalf("expected a locally accepted original: %+v, %v", target, err)
	}
	if _, err := sender.EditMessage(ctx, key, original, target.CurrentRevisionID, body); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ClaimMessageEdit(ctx, key, mintDirectEchoID(t), now); err != nil {
		t.Fatal(err)
	}
	terminal, err := source.FinishMessageEdit(ctx, key, "submitted", mintDirectEchoID(t), now, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	archive := writeRoomEchoBackup(t, sourceDir)

	restore := func(dir, file string) {
		t.Helper()
		if _, err := sqlite.RestoreProfileBackup(ctx, dir, file, mintDirectEchoID(t)); err != nil {
			t.Fatal(err)
		}
	}
	observe := func(engine *application.Engine, author, occupantID string, id, target domain.ID, text, archiveKey string) {
		t.Helper()
		var replacement string
		if target != "" {
			replacement = fmt.Sprintf(`<replace xmlns="urn:xmpp:message-correct:0" id="%s"/>`, target)
		}
		raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s" type="groupchat" id="%s"><body>%s</body><origin-id xmlns="urn:xmpp:sid:0" id="%s"/>%s<occupant-id xmlns="urn:xmpp:occupant-id:0" id="%s"/><x xmlns="http://jabber.org/protocol/muc#user"><item jid="%s/other-client"/></x></message></forwarded></result>`,
			archiveKey, now.Format(time.RFC3339Nano), author, id, text, id, replacement, occupantID, account.Address)
		var result archiveResult
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		incoming, ok, err := archivedIncoming(account, room.Address, &result, archiveTrust{SIDVerified: true, OccupantVerified: true})
		if err != nil || !ok || !incoming.IsOwn || incoming.CanonicalID != id || incoming.ProviderID == string(id) ||
			incoming.Correction == nil || incoming.Correction.Scope != roomCorrectionScope(room.Address, author, occupantID) {
			t.Fatalf("fixture did not decode the scoped MAM observation: %+v, %v, %v", incoming, ok, err)
		}
		if err := engine.Receive(ctx, incoming); err != nil {
			t.Fatal(err)
		}
	}
	owner := func(dir, archiveKey string) domain.ID {
		t.Helper()
		db, err := sql.Open("sqlite", filepath.Join(dir, "oie.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var id domain.ID
		if err := db.QueryRowContext(ctx, `SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?`,
			account.ID, room.Address, archiveKey).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	checkReceipt := func(engine *application.Engine) {
		t.Helper()
		replay, err := engine.EditMessage(ctx, key, original, target.CurrentRevisionID, body)
		if err != nil || !replay.Replayed || replay.Command.State != "submitted" || replay.Command.RevisionID != terminal.RevisionID {
			t.Fatalf("archived correction changed the original acceptance: %+v, %v", replay, err)
		}
	}

	t.Run("replay reuses independent archived witness", func(t *testing.T) {
		dir := t.TempDir()
		restore(dir, seed)
		store, receiver, _ := openDirectEchoProfile(t, dir)
		observe(receiver, protocol.author, occupant, original, "", sent.Message.OriginalText, "archive-original")
		observe(receiver, protocol.author, occupant, key, original, body, "archive-first-witness")
		before, err := store.Message(ctx, original)
		if err != nil || !before.Edited || before.ProjectionSourceID == "" || before.ProjectionSourceID == key {
			t.Fatalf("expected an independent room witness before importing its receipt: %+v, %v", before, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		restore(dir, archive)
		restore(dir, archive)
		store, receiver, _ = openDirectEchoProfile(t, dir)
		for range 2 {
			observe(receiver, protocol.author, occupant, key, original, body, "archive-second-witness")
		}
		history, err := store.Messages(ctx, room.ID, "", 10)
		if err != nil || len(history) != 1 || !history[0].Edited || history[0].ProjectionSourceID != before.ProjectionSourceID ||
			history[0].RevisionID != before.RevisionID || domain.DisplayBody(history[0]) != body {
			t.Fatalf("replaying the exact packet forked the retained correction: %+v, %v", history, err)
		}
		for _, archiveKey := range []string{"archive-first-witness", "archive-second-witness"} {
			if got := owner(dir, archiveKey); got != before.ProjectionSourceID {
				t.Fatalf("archive key moved from physical witness %s to %s", before.ProjectionSourceID, got)
			}
		}
		if _, err := store.MessageSource(ctx, key); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("replay enrolled a second native source: %v", err)
		}
		checkReceipt(receiver)
	})

	t.Run("same client ID from another verified occupant", func(t *testing.T) {
		dir := t.TempDir()
		restore(dir, archive)
		store, receiver, _ := openDirectEchoProfile(t, dir)
		otherAuthor := room.Address + "/AnotherNickname"
		const otherOccupant = "verified-second-occupant"
		otherOriginal := mintDirectEchoID(t)
		observe(receiver, otherAuthor, otherOccupant, otherOriginal, "", "independent room original", "archive-other-original")
		for range 2 {
			observe(receiver, otherAuthor, otherOccupant, key, otherOriginal, body, "archive-other-correction")
		}
		history, err := store.Messages(ctx, room.ID, "", 10)
		if err != nil || len(history) != 2 {
			t.Fatalf("another occupant's correction changed the room history: %+v, %v", history, err)
		}
		for _, item := range history {
			if !item.Edited || domain.DisplayBody(item) != body {
				t.Fatalf("the two independently verified corrections did not both survive: %+v", history)
			}
		}
		otherSource := owner(dir, "archive-other-correction")
		if raw, err := store.MessageSource(ctx, otherSource); err != nil || raw.ProviderID == string(key) || otherSource == key {
			t.Fatalf("the independent occupant lost its physical wire identity: %+v, %v", raw, err)
		}
		checkReceipt(receiver)
	})
}
