package xmpp

import (
	"fmt"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreIdlessDirectEchoRetainsStarAndRevision(t *testing.T) {
	ctx := t.Context()
	baseDir := t.TempDir()
	base, baseEngine, _ := openDirectEchoProfile(t, baseDir)
	account, chat := setupDirectEchoProfile(t, baseEngine)
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	seed := writeRoomEchoBackup(t, baseDir)
	liveDir, mamDir, unstarredDir := t.TempDir(), t.TempDir(), t.TempDir()
	apply := func(dir, filename string, wantAdded int64) {
		t.Helper()
		if result, err := sqlite.RestoreProfileBackup(ctx, dir, filename, mintDirectEchoID(t)); err != nil || result.Messages != wantAdded {
			t.Fatalf("restore did not retain the expected logical messages: %+v, %v", result, err)
		}
	}
	closeStore := func(store *sqlite.Store) {
		t.Helper()
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{liveDir, mamDir, unstarredDir} {
		apply(dir, seed, 0)
	}
	live, _, manager := openDirectEchoProfile(t, liveDir)
	mam, mamEngine, _ := openDirectEchoProfile(t, mamDir)
	unstarred, unstarredEngine, unstarredManager := openDirectEchoProfile(t, unstarredDir)
	origin := mintDirectEchoID(t)
	packet := idlessDirectPacket(account, origin, "the same independently observed direct packet", "")
	receiveLiveDirectEchoPacket(t, manager, account, packet)
	receiveLiveDirectEchoPacket(t, unstarredManager, account, packet)
	const archiveKey = "observed"
	receiveArchivedDirectEchoPacket(t, mamEngine, account, origin, archiveKey, packet, time.Now().UTC())
	onlyMessage := func(store *sqlite.Store) domain.Message {
		t.Helper()
		rows, err := store.Messages(ctx, chat.ID, "", 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("expected one logical message: %+v, %v", rows, err)
		}
		return rows[0]
	}
	dest, source, explicit := onlyMessage(live), onlyMessage(mam), onlyMessage(unstarred)
	if source.ID == dest.ID || source.ID == explicit.ID {
		t.Fatal("fixtures did not retain independent observations")
	}
	selected, err := unstarredEngine.SetMessageStar(ctx, mintDirectEchoID(t), explicit.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := unstarredEngine.SetMessageStar(ctx, mintDirectEchoID(t), explicit.ID, selected.Star.RevisionID, false)
	if err != nil || cleared.Star.Starred || cleared.Star.RevisionID == "" {
		t.Fatalf("explicit destination unstar fixture: %+v, %v", cleared, err)
	}
	star, err := mamEngine.SetMessageStar(ctx, mintDirectEchoID(t), source.ID, "", true)
	if err != nil || !star.Star.Starred {
		t.Fatalf("source star fixture: %+v, %v", star, err)
	}
	for _, store := range []*sqlite.Store{live, mam, unstarred} {
		closeStore(store)
	}
	archive := writeRoomEchoBackup(t, mamDir)
	check := func(dir string, retained, alternative domain.Message, wantStar domain.MessageStar) {
		t.Helper()
		store, engine, _ := openDirectEchoProfile(t, dir)
		defer closeStore(store)
		if got := onlyMessage(store); got.ID != retained.ID || got.RevisionID != retained.RevisionID {
			t.Fatalf("restore replaced the retained observation: %+v", got)
		}
		if raw, err := store.MessageSource(ctx, alternative.ID); err != nil || raw.RevisionID != alternative.RevisionID || raw.ID != alternative.ID {
			t.Fatalf("restore did not preserve the independent source revision: %+v, %v", raw, err)
		}
		if got, err := engine.MessageStar(ctx, retained.ID); err != nil || got.Starred != wantStar.Starred || got.RevisionID != wantStar.RevisionID {
			t.Fatalf("restore chose a different star revision: %+v, %v", got, err)
		}
		assertDirectEchoArchiveOwner(t, dir, account, archiveKey, retained.ID)
	}
	apply(liveDir, archive, 0)
	apply(liveDir, archive, 0)
	check(liveDir, dest, source, star.Star)
	apply(unstarredDir, archive, 0)
	check(unstarredDir, explicit, source, cleared.Star)

	relay := writeRoomEchoBackup(t, liveDir)
	freshDir := t.TempDir()
	apply(freshDir, relay, 1)
	apply(freshDir, archive, 0)
	check(freshDir, dest, source, star.Star)
	apply(mamDir, relay, 0)
	check(mamDir, source, dest, star.Star)
}

func TestBackupRestoreIdlessDirectEchoPrefersKnownArchiveOwner(t *testing.T) {
	ctx := t.Context()
	baseDir := t.TempDir()
	base, baseEngine, _ := openDirectEchoProfile(t, baseDir)
	account, chat := setupDirectEchoProfile(t, baseEngine)
	now := time.Now().UTC()
	const secret = "the original old text"
	target := domain.MessageReference{Scope: "sender:" + account.Address, Author: account.Address, ID: "quoted-self"}
	if err := baseEngine.Receive(ctx, application.Incoming{AccountID: account.ID, Kind: "direct", Address: account.Address, IsOwn: true, ProviderID: target.ID, OriginalText: secret, CreatedAt: now.Add(-48 * time.Hour), Historical: true, References: []domain.MessageReference{target}}); err != nil {
		t.Fatal(err)
	}
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	seed := writeRoomEchoBackup(t, baseDir)
	olderDir, mergedDir := t.TempDir(), t.TempDir()
	for _, d := range []string{olderDir, mergedDir} {
		if _, err := sqlite.RestoreProfileBackup(ctx, d, seed, mintDirectEchoID(t)); err != nil {
			t.Fatal(err)
		}
	}
	old, oldEngine, _ := openDirectEchoProfile(t, olderDir)
	merged, mergedEngine, manager := openDirectEchoProfile(t, mergedDir)
	origin := mintDirectEchoID(t)
	quote := "> " + secret + "\n"
	const answer = "same visible reply text"
	reply := fmt.Sprintf(`<reply xmlns="urn:xmpp:reply:0" id="%s" to="%s"/>`, target.ID, account.Address)
	fallback := func(spans string) string {
		return reply + `<fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0">` + spans + "</fallback>"
	}
	live := idlessDirectPacket(account, origin, quote+answer, fallback(fmt.Sprintf(`<body start="0" end="3"/><body start="3" end="%d"/>`, len(quote))))
	mam := idlessDirectPacket(account, origin, quote+answer, fallback(fmt.Sprintf(`<body start="0" end="%d"/>`, len(quote))))
	const archiveKey = "same-archive-stanza"
	receiveLiveDirectEchoPacket(t, manager, account, live)
	receiveArchivedDirectEchoPacket(t, mergedEngine, account, origin, archiveKey, mam, now)
	receiveArchivedDirectEchoPacket(t, oldEngine, account, origin, archiveKey, mam, now)
	if rows, err := merged.Messages(ctx, chat.ID, "", 10); err != nil || len(rows) != 3 {
		t.Fatalf("review requires two legitimately distinct reply fallbacks: %d %v", len(rows), err)
	}
	days := 1
	if _, err := mergedEngine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if n, err := merged.PurgeExpiredHistory(ctx, now.Add(time.Second), directEchoNewID); err != nil || n != 1 {
		t.Fatalf("expire quote: %d %v", n, err)
	}
	redacted, err := merged.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(redacted) != 2 || redacted[0].OriginalText != answer || redacted[1].OriginalText != answer {
		t.Fatalf("fixture didn't redact independent fallback shapes: %+v %v", redacted, err)
	}
	var archiveOwner domain.ID
	for _, row := range redacted {
		if row.ArchiveID == archiveKey {
			archiveOwner = row.ID
		}
	}
	if archiveOwner == "" {
		t.Fatal("the original archive observation lost its key during quote expiry")
	}
	for _, s := range []*sqlite.Store{old, merged} {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	archive := writeRoomEchoBackup(t, olderDir)
	if result, err := sqlite.RestoreProfileBackup(ctx, mergedDir, archive, mintDirectEchoID(t)); err != nil || result.Messages != 0 {
		t.Fatalf("exact existing archive key should retain its owner even with another compositionally matching row: %+v %v", result, err)
	}
	assertDirectEchoArchiveOwner(t, mergedDir, account, archiveKey, archiveOwner)
	merged, _, _ = openDirectEchoProfile(t, mergedDir)
	if rows, err := merged.Messages(ctx, chat.ID, "", 10); err != nil || len(rows) != 2 || rows[0].ID != redacted[0].ID || rows[1].ID != redacted[1].ID {
		t.Fatalf("the established archive key changed two earlier logical observations: %+v, %v", rows, err)
	}
}
