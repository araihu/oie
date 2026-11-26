package sqlite

import (
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"testing"
	"time"
)

func TestHistoryReplayChronologyUnreadAndOutboxIsolation(t *testing.T) {
	store, e, c := outboxFixture(t)
	now := time.Now().UTC()
	live := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "latest live", CreatedAt: now, ProviderID: "live-id", SendState: "received"}
	if err := store.ReceiveMessage(t.Context(), c, live, migrationID()); err != nil {
		t.Fatal(err)
	}
	first, err := store.Conversation(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		old := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "old archive", CreatedAt: now.Add(-time.Duration(i+1) * time.Hour), ProviderID: "historical-" + string(rune('a'+i)), SendState: "received", Historical: true, ArchiveScope: "alice@localhost", ArchiveID: string(rune('a' + i))}
		for attempt := 0; attempt < 2; attempt++ {
			if err = store.ReceiveMessage(t.Context(), c, old, migrationID()); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, _ := store.Conversation(t.Context(), c.ID)
	if got.LastMessage != "latest live" || got.UnreadCount != 1 || got.RevisionID != first.RevisionID {
		t.Fatalf("history changed latest preview/unread: %+v", got)
	}
	page, err := store.Messages(t.Context(), c.ID, "", 2)
	if err != nil || len(page) != 2 || page[0].OriginalText != "latest live" {
		t.Fatalf("chronology: %+v %v", page, err)
	}
	next, err := store.Messages(t.Context(), c.ID, page[1].ID, 2)
	if err != nil || len(next) != 2 || !next[0].CreatedAt.After(next[1].CreatedAt) {
		t.Fatalf("chronological paging: %+v %v", next, err)
	}
	alias := live
	alias.ID = migrationID()
	alias.RevisionID = migrationID()
	alias.Historical = true
	alias.ArchiveScope = "alice@localhost"
	alias.ArchiveID = "live-archive"
	if err = store.ReceiveMessage(t.Context(), c, alias, migrationID()); err != nil {
		t.Fatal(err)
	}
	all, _ := store.Messages(t.Context(), c.ID, "", 100)
	if len(all) != 4 {
		t.Fatalf("live/archive duplicate: %d", len(all))
	}
	pending, _ := store.PendingMessages(t.Context(), now.Add(time.Hour))
	if len(pending) != 0 {
		t.Fatal("archive created send work")
	}
	if err = e.CommitHistoryCheckpoint(t.Context(), c.AccountID, "alice@localhost", "opaque-last"); err != nil {
		t.Fatal(err)
	}
	if pos, _ := e.HistoryCheckpoint(t.Context(), c.AccountID, "alice@localhost"); pos != "opaque-last" {
		t.Fatal("checkpoint lost")
	}
	if err = e.HistoryChanged(t.Context(), domain.HistoryStatus{AccountID: c.AccountID, State: "complete"}); err != nil {
		t.Fatal(err)
	}
	h, err := e.History(t.Context(), c.AccountID)
	if err != nil || h.RetainedMessages != 4 || h.Conversations != 1 || h.State != "complete" {
		t.Fatalf("history progress: %+v %v", h, err)
	}
	// Importing an observed own message must never revive a cancelled intent.
	own, err := e.SendMessage(t.Context(), migrationID(), c.ID, "cancelled own")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := e.CancelMessage(t.Context(), migrationID(), own.Message.ID, own.Message.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	copy := cancelled.Message
	copy.ID = migrationID()
	copy.RevisionID = migrationID()
	copy.Historical = true
	copy.SendState = "submitted"
	copy.ArchiveScope = "alice@localhost"
	copy.ArchiveID = "cancelled-archive"
	if err = store.ReceiveMessage(t.Context(), c, copy, migrationID()); err != nil {
		t.Fatal(err)
	}
	original, _ := e.Message(t.Context(), own.Message.ID)
	if original.SendState != "cancelled" {
		t.Fatal("archive revived cancelled send")
	}
}
func TestRestoredRoomIsIdempotentAndRejectsSend(t *testing.T) {
	store, e, c := outboxFixture(t)
	room := c
	room.ID = migrationID()
	room.RevisionID = migrationID()
	room.PeerID = migrationID()
	room.Address = "room@conference.localhost"
	room.Title = "Saved room"
	room.Kind = "room"
	for i := 0; i < 2; i++ {
		if err := store.RestoreConversation(t.Context(), room); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.Conversations(t.Context(), application.ConversationQuery{Cursor: 1 << 62, Limit: 100})
	if err != nil || len(rows) != 2 {
		t.Fatalf("room duplicated: %v %+v", err, rows)
	}
	if _, err = e.SendMessage(t.Context(), migrationID(), room.ID, "must not send"); err != application.ErrProtocolUnavailable {
		t.Fatalf("read-only room accepted send: %v", err)
	}
}

func TestSavedRoomTitleFollowsDiscoveryWithoutChangingActivity(t *testing.T) {
	store, engine, direct := outboxFixture(t)
	engine.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	address := "room@conference.localhost"
	restore := func(title string) {
		t.Helper()
		if err := engine.RestoreConversation(ctx, direct.AccountID, address, "room", title); err != nil {
			t.Fatal(err)
		}
	}
	room := func(want string) domain.Conversation {
		t.Helper()
		c, err := store.RestoredRoom(ctx, direct.AccountID, address)
		if err != nil || c.Title != want {
			t.Fatalf("want title %q, got %q: %v", want, c.Title, err)
		}
		return c
	}
	observe := func(title string) {
		t.Helper()
		if err := engine.ObserveRoomTitle(ctx, direct.AccountID, address, title); err != nil {
			t.Fatal(err)
		}
	}
	restore("")
	initial := room(address)
	restore("Bookmark name")
	room("Bookmark name")
	observe("  Garden & Books  ")
	named := room("Garden & Books")
	if named.ID != initial.ID || named.RevisionID == initial.RevisionID || named.ActivitySequence != initial.ActivitySequence || !named.UpdatedAt.Equal(initial.UpdatedAt) {
		t.Fatalf("room identity, revision or activity after discovery: %+v", named)
	}
	observe("")
	observe(address)
	observe("Garden & Books")
	if err := engine.ObserveRoomTitleFallback(ctx, direct.AccountID, address, "Old profile"); err != nil {
		t.Fatal(err)
	}
	// The writer rechecks precedence even if discovery won after the initial read.
	if err := store.UpdateRoomTitle(ctx, named.ID, "Old profile", true, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	restore("Old bookmark")
	if room("Garden & Books").RevisionID != named.RevisionID {
		t.Fatal("repeated discovery or bookmark restore churned the room revision")
	}
	observe("Reading club")
	room("Reading club")

	address = "profile-only@conference.localhost"
	restore("")
	if err := engine.ObserveRoomTitleFallback(ctx, direct.AccountID, address, "Room profile"); err != nil {
		t.Fatal(err)
	}
	room("Room profile")
	observe("Advertised name")
	room("Advertised name")
}

func TestHistoryMigrationDownUpPreservesExistingMessagesAndOutbox(t *testing.T) {
	store, e, c := outboxFixture(t)
	accepted, err := e.SendMessage(t.Context(), migrationID(), c.ID, "retained through migration")
	if err != nil {
		t.Fatal(err)
	}
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	driver, err := migratesqlite.WithInstance(store.db, &migratesqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Migrate(2); err != nil {
		t.Fatal(err)
	}
	if err = runner.Up(); err != nil {
		t.Fatal(err)
	}
	messages, err := store.Messages(t.Context(), c.ID, "", 100)
	if err != nil || len(messages) != 1 || messages[0].ID != accepted.Message.ID || messages[0].OriginalText != accepted.Message.OriginalText || !messages[0].CreatedAt.Equal(accepted.Message.CreatedAt) {
		t.Fatalf("migration lost retained data: %+v %v", messages, err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 1 || pending[0].SendState != "queued" {
		t.Fatal("migration lost pending intent")
	}
}

func TestVerifiedRoomAuthorReplayKeepsMessageIdentityAndOutboxEmpty(t *testing.T) {
	store, _, base := outboxFixture(t)
	c := base
	c.ID = migrationID()
	c.RevisionID = migrationID()
	c.PeerID = migrationID()
	c.Kind = "room"
	c.Address = "room@conference.localhost"
	if err := store.RestoreConversation(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "historical own content", CreatedAt: time.Now().UTC().Add(-time.Hour), ProviderID: "room-provider", ArchiveScope: c.Address, ArchiveID: "archive-own", Historical: true, SendState: "received", Sender: domain.Sender{DisplayName: "observed nickname"}}
	if err := store.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	observed := m
	observed.ID = migrationID()
	observed.RevisionID = migrationID()
	observed.IsOwn = true
	observed.Sender.ID = c.AccountID
	observed.OriginalText = "a replay must not replace stored content"
	for n := 0; n < 2; n++ {
		if err := store.ReceiveMessage(t.Context(), c, observed, migrationID()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Message(t.Context(), m.ID)
	if err != nil || !got.IsOwn || got.Sender.ID != c.AccountID || got.OriginalText != m.OriginalText || got.RevisionID == m.RevisionID {
		t.Fatal("incorrect reconciliation", got, err)
	}
	rows, err := store.Messages(t.Context(), c.ID, "", 100)
	if err != nil || len(rows) != 1 {
		t.Fatal("replay duplicated message", len(rows), err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("identity repair created sends", err)
	}
	var revisions int
	if err = store.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM message_revisions WHERE entity_id=?", m.ID).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatal("replay created extra revisions", revisions, err)
	}
}

func TestRoomArchiveWithoutInnerStanzaIDKeepsOwnLiveMessage(t *testing.T) {
	store, _, chat := outboxFixture(t)
	engine, chat := promoteBackupRoomFixture(t, store, chat)
	sent := queueBackupFixture(t, engine, chat.ID, "Mensagem da sala", nil)
	receive := func(in application.Incoming) {
		t.Helper()
		if err := engine.Receive(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	const roomID = "room-issued-42"
	packet := application.Incoming{
		AccountID: chat.AccountID, Kind: "room", Address: chat.Address, SenderName: "Alice",
		OriginalText: sent.OriginalText, CreatedAt: time.Now().UTC(), IsOwn: true,
		CanonicalID: sent.ID, ProviderID: string(sent.ID),
		References: []domain.MessageReference{{Scope: "room:" + chat.Address, Author: chat.Address + "/Alice", ID: roomID}},
	}
	receive(packet)
	// A semianonymous room returns only the outer MAM ID and occupant nickname.
	packet.Historical, packet.IsOwn = true, false
	packet.CanonicalID, packet.ProviderID = "", "room:archive-client-id"
	// The adapter validates the outer room UID as its reflected stanza ID.
	packet.ArchiveScope, packet.ArchiveID = chat.Address, roomID
	receive(packet)
	receive(packet)
	history, err := store.Messages(t.Context(), chat.ID, "", 10)
	if err != nil || len(history) != 1 || !history[0].IsOwn || history[0].ID != sent.ID {
		t.Fatalf("room archive replay duplicated the sent message: %d rows, %v", len(history), err)
	}
	// A different occurrence of the same text is a separate message.
	packet.ArchiveID, packet.ProviderID, packet.SenderName = "room-issued-43", "room:other-client-id", "Bob"
	packet.References = []domain.MessageReference{{Scope: "room:" + chat.Address, Author: chat.Address + "/Bob", ID: packet.ArchiveID}}
	receive(packet)
	// The personal archive has a separate ID namespace.
	packet.ArchiveScope, packet.ArchiveID, packet.ProviderID = "alice@localhost", roomID, "room:personal-archive-id"
	packet.References = nil
	receive(packet)
	history, err = store.Messages(t.Context(), chat.ID, "", 10)
	if err != nil || len(history) != 3 {
		t.Fatalf("distinct messages lost after replay: %d rows, %v", len(history), err)
	}
}
