package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

func TestAvatarRevisionsIsolationAndExplicitRemoval(t *testing.T) {
	store, _, c := outboxFixture(t)
	makeAvatar := func(hash string) domain.Avatar {
		return domain.Avatar{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, Address: "bob@localhost", ImageHash: hash, SourceToken: "fixture", UpdatedAt: time.Now().UTC()}
	}
	first := makeAvatar("hash-one")
	if err := store.SaveAvatar(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	unchanged := makeAvatar("hash-one")
	if err := store.SaveAvatar(t.Context(), unchanged); err != nil {
		t.Fatal(err)
	}
	got, err := store.Avatar(t.Context(), c.AccountID, first.Address)
	if err != nil || got.ID != first.ID || got.RevisionID != first.RevisionID {
		t.Fatal("unchanged profile created revision", got, err)
	}
	second := makeAvatar("hash-two")
	if err = store.SaveAvatar(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	got, err = store.Avatar(t.Context(), c.AccountID, first.Address)
	if err != nil || got.ID != first.ID || got.RevisionID != second.RevisionID {
		t.Fatal("profile identity/revision changed incorrectly", got, err)
	}
	if err = store.SaveAvatar(t.Context(), makeAvatar("")); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Avatars(t.Context(), c.AccountID, "", 100)
	if err != nil || len(rows) != 1 || rows[0].ImageHash != "" {
		t.Fatal("explicit clear missing", rows, err)
	}
	other, err := store.Avatars(t.Context(), migrationID(), "", 100)
	if err != nil || len(other) != 0 {
		t.Fatal("cross-account profile leak", err)
	}
	events, err := store.Events(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, event := range events {
		if event.Type == "profile.avatar_changed" {
			n++
			if event.Avatar == nil || event.Avatar.ID != first.ID {
				t.Fatal("invalid avatar event")
			}
		}
	}
	if n != 3 {
		t.Fatal("unexpected avatar change count", n)
	}
	var count int
	if err = store.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM avatar_revisions WHERE entity_id=?", first.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal("revision history lost", count, err)
	}
}
