package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func senderSeed(t *testing.T, s *Store, c domain.Conversation, personal string) domain.Message {
	t.Helper()
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{DisplayName: "room nickname", PersonalAddress: personal}, OriginalText: "retained source", ProviderID: string(migrationID()), CreatedAt: time.Now().UTC(), Historical: true, SendState: "received"}
	if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSenderIdentityEnrichmentReplayConflictAndReopen(t *testing.T) {
	s, _, c := outboxFixture(t)
	unknown := senderSeed(t, s, c, "")
	before, err := s.Message(t.Context(), unknown.ID)
	if err != nil || before.Sender.PersonalAddress != "" {
		t.Fatal(before, err)
	}
	var payload, revision string
	if err = s.db.QueryRow("SELECT payload,revision_id FROM messages WHERE id=?", unknown.ID).Scan(&payload, &revision); err != nil {
		t.Fatal(err)
	}
	unknown.Sender.PersonalAddress = "author@example.test"
	unknown.ArchiveScope = c.Address
	unknown.ArchiveID = "archive-source"
	if err = s.ReceiveMessage(t.Context(), c, unknown, migrationID()); err != nil {
		t.Fatal(err)
	}
	first, err := s.Message(t.Context(), unknown.ID)
	if err != nil || first.Sender.PersonalAddress != unknown.Sender.PersonalAddress {
		t.Fatal(first, err)
	}
	_, seq, _ := s.Snapshot(t.Context())
	if err = s.ReceiveMessage(t.Context(), c, unknown, migrationID()); err != nil {
		t.Fatal(err)
	}
	_, after, _ := s.Snapshot(t.Context())
	if seq != after {
		t.Fatal("identity replay emitted another event")
	}
	var currentPayload, currentRevision string
	if err = s.db.QueryRow("SELECT payload,revision_id FROM messages WHERE id=?", unknown.ID).Scan(&currentPayload, &currentRevision); err != nil || currentPayload != payload || currentRevision != revision {
		t.Fatal("identity enrichment rewrote message content/revision", err)
	}
	var aliases int
	if err = s.db.QueryRow("SELECT count(*) FROM message_sender_identities WHERE message_id=?", unknown.ID).Scan(&aliases); err != nil || aliases != 1 {
		t.Fatal(aliases, err)
	}
	// Exercise the archive-alias fast replay path with no caller conversation ID.
	unknown.ID = migrationID()
	unknown.ConversationID = ""
	unknown.Sender.PersonalAddress = "different@example.test"
	if err = s.ReceiveMessage(t.Context(), c, unknown, migrationID()); err != nil {
		t.Fatal(err)
	}
	disputed, err := s.Message(t.Context(), first.ID)
	if err != nil || disputed.Sender.PersonalAddress != "" {
		t.Fatal("conflicting authenticated addresses selected a profile", disputed, err)
	}
	// A later replay of the first address cannot erase conflict evidence.
	unknown.Sender.PersonalAddress = "author@example.test"
	if err = s.ReceiveMessage(t.Context(), c, unknown, migrationID()); err != nil {
		t.Fatal(err)
	}
	disputed, err = s.Message(t.Context(), first.ID)
	if err != nil || disputed.Sender.PersonalAddress != "" {
		t.Fatal(disputed, err)
	}
	retained := senderSeed(t, s, c, "retained@example.test")
	var sequence int
	var name, path string
	if err = s.db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), filepath.Dir(path), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	got, err := reopened.Message(t.Context(), retained.ID)
	if err != nil || got.Sender.PersonalAddress != "retained@example.test" {
		t.Fatal("author evidence did not survive restart", got, err)
	}
	got, err = reopened.Message(t.Context(), first.ID)
	if err != nil || got.Sender.PersonalAddress != "" {
		t.Fatal("restart discarded ambiguity", got, err)
	}
}

func TestSenderIdentityScopeAndBodyCollisionAreFailClosed(t *testing.T) {
	s, _, c := outboxFixture(t)
	m := senderSeed(t, s, c, "author@example.test")
	// A scope mismatch cannot write evidence for a different owner's source.
	row, err := s.queries.GetMessage(t.Context(), string(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	bad := m
	bad.AccountID = migrationID()
	bad.Sender.PersonalAddress = "forged@example.test"
	if err = recordReplyMetadata(t.Context(), s.queries, row.ID, bad, true); err == nil {
		t.Fatal("foreign account metadata accepted")
	}
	bad = m
	bad.ConversationID = migrationID()
	bad.Sender.PersonalAddress = "forged@example.test"
	if err = recordReplyMetadata(t.Context(), s.queries, row.ID, bad, true); err == nil {
		t.Fatal("foreign conversation metadata accepted")
	}
	got, err := s.Message(t.Context(), m.ID)
	if err != nil || got.Sender.PersonalAddress != "author@example.test" {
		t.Fatal(got, err)
	}
	// Reusing a wire identity for another body must not attach a wrong profile.
	collision := m
	collision.OriginalText = "colliding content"
	collision.Sender.PersonalAddress = "other@example.test"
	if err = s.ReceiveMessage(t.Context(), c, collision, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err = s.Message(t.Context(), m.ID)
	if err != nil || got.Sender.PersonalAddress != "" {
		t.Fatal("body collision selected a profile", got, err)
	}
}

func TestSenderIdentityCanonicalAliasesRequireOneConsistentAddress(t *testing.T) {
	s, _, c := outboxFixture(t)
	first := senderSeed(t, s, c, "")
	alias := senderSeed(t, s, c, "author@example.test")
	if err := s.queries.PutCanonicalAlias(t.Context(), dbgen.PutCanonicalAliasParams{AliasID: string(alias.ID), CanonicalID: string(first.ID)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Message(t.Context(), alias.ID)
	if err != nil || got.ID != first.ID || got.Sender.PersonalAddress != "author@example.test" {
		t.Fatal("canonical alias lost source evidence", got, err)
	}
	conflict := senderSeed(t, s, c, "other@example.test")
	if err = s.queries.PutCanonicalAlias(t.Context(), dbgen.PutCanonicalAliasParams{AliasID: string(conflict.ID), CanonicalID: string(first.ID)}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Message(t.Context(), first.ID)
	if err != nil || got.Sender.PersonalAddress != "" {
		t.Fatal("canonical conflict chose one author", got, err)
	}
}

func TestSenderIdentityMigrationPreservesVersionTwelveSources(t *testing.T) {
	s, _, c := outboxFixture(t)
	m := senderSeed(t, s, c, "")
	var payload string
	if err := s.db.QueryRow("SELECT payload FROM messages WHERE id=?", m.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	engine, seq, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrations.ReadFile("migrations/000013_sender_identities.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE schema_migrations SET version=12,dirty=0"); err != nil {
		t.Fatal(err)
	}
	var databaseIndex int
	var databaseName, path string
	if err = s.db.QueryRow("PRAGMA database_list").Scan(&databaseIndex, &databaseName, &path); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), filepath.Dir(path), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()
	got, err := upgraded.Message(t.Context(), m.ID)
	if err != nil || got.Sender.PersonalAddress != "" || got.ID != m.ID || got.RevisionID != m.RevisionID {
		t.Fatal("migration guessed identity or changed source", got, err)
	}
	var afterPayload string
	if err = upgraded.db.QueryRow("SELECT payload FROM messages WHERE id=?", m.ID).Scan(&afterPayload); err != nil || afterPayload != payload {
		t.Fatal("upgrade rewrote source payload", err)
	}
	afterEngine, afterSeq, err := upgraded.Snapshot(t.Context())
	if err != nil || afterEngine != engine || afterSeq != seq {
		t.Fatal("upgrade changed engine or events", err)
	}
}
