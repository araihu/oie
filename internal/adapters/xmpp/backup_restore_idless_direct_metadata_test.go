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

func TestBackupRestoreIdlessInheritedFutureStarCanBeExplicitlyUnstarred(t *testing.T) {
	t.Run("recent live", func(t *testing.T) { testBackupRestoreIdlessCausalStar(t, false) })
	t.Run("retained older archive", func(t *testing.T) { testBackupRestoreIdlessCausalStar(t, true) })
}

func testBackupRestoreIdlessCausalStar(t *testing.T, old bool) {
	t.Helper()
	ctx := t.Context()
	baseDir, liveDir, mamDir := t.TempDir(), t.TempDir(), t.TempDir()
	base, e, _ := openDirectEchoProfile(t, baseDir)
	account, chat := setupDirectEchoProfile(t, e)
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	seed := writeRoomEchoBackup(t, baseDir)
	for _, dir := range []string{liveDir, mamDir} {
		if _, err := sqlite.RestoreProfileBackup(ctx, dir, seed, mintDirectEchoID(t)); err != nil {
			t.Fatal(err)
		}
	}
	live, liveReceiver, manager := openDirectEchoProfile(t, liveDir)
	mam, _, _ := openDirectEchoProfile(t, mamDir)
	later := time.Now().Add(2 * time.Hour)
	sourceEngine, err := application.New(mam, "local", directEchoNewID, func() time.Time { return later })
	if err != nil {
		t.Fatal(err)
	}
	sourceEngine.SetProtocols(New(mamDir, nil))
	origin := mintDirectEchoID(t)
	packet := idlessDirectPacket(account, origin, "a verified independently observed direct message", "")
	observedAt := time.Now()
	if old {
		observedAt = observedAt.Add(-48 * time.Hour)
		receiveArchivedDirectEchoPacket(t, liveReceiver, account, origin, "other-observed", packet, observedAt)
	} else {
		receiveLiveDirectEchoPacket(t, manager, account, packet)
	}
	receiveArchivedDirectEchoPacket(t, sourceEngine, account, origin, "observed", packet, observedAt)
	liveMessages, err := live.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(liveMessages) != 1 {
		t.Fatal(liveMessages, err)
	}
	mamMessages, err := mam.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(mamMessages) != 1 {
		t.Fatal(mamMessages, err)
	}
	local := liveMessages[0]
	star, err := sourceEngine.SetMessageStar(ctx, mintDirectEchoID(t), mamMessages[0].ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mam.Close(); err != nil {
		t.Fatal(err)
	}
	archive := writeRoomEchoBackup(t, mamDir)
	if _, err := sqlite.RestoreProfileBackup(ctx, liveDir, archive, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	live, receiver, _ := openDirectEchoProfile(t, liveDir)
	if read, err := receiver.MessageStar(ctx, local.ID); err != nil || read.RevisionID != star.Star.RevisionID {
		t.Fatalf("did not inherit the exact source star: %+v %v", read, err)
	}
	cleared, err := receiver.SetMessageStar(ctx, mintDirectEchoID(t), local.ID, star.Star.RevisionID, false)
	if err != nil || cleared.Star.Starred {
		t.Fatalf("local unstar was not accepted: %+v %v", cleared, err)
	}
	current, err := receiver.MessageStar(ctx, local.ID)
	if err != nil || current.Starred || current.RevisionID != cleared.Star.RevisionID {
		t.Fatalf("clock-ahead archived alias defeated explicit local unstar: %+v %v", current, err)
	}

	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	relay := writeRoomEchoBackup(t, liveDir)
	fresh := t.TempDir()
	if _, err := sqlite.RestoreProfileBackup(ctx, fresh, relay, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{liveDir, fresh} {
		if _, err := sqlite.RestoreProfileBackup(ctx, dir, archive, mintDirectEchoID(t)); err != nil {
			t.Fatal(err)
		}
		store, engine, _ := openDirectEchoProfile(t, dir)
		current, err := engine.MessageStar(ctx, local.ID)
		if err != nil || current.Starred || current.RevisionID != cleared.Star.RevisionID {
			t.Fatalf("archived stars reversed the explicit unstar: %+v %v", current, err)
		}
		starred, err := engine.StarredMessages(ctx, application.StarQuery{ConversationID: chat.ID, Limit: 10})
		if err != nil || len(starred) != 0 {
			t.Fatalf("the unstarred message appeared in the starred list: %+v %v", starred, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// The original observer still has the exact ancestor. The relay's lower
	// wall-clock time cannot keep that ancestor selected over its descendant.
	if old {
		older, engine, _ := openDirectEchoProfile(t, mamDir)
		days := 1
		if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
			t.Fatal(err)
		}
		if removed, err := older.PurgeExpiredHistory(ctx, time.Now(), directEchoNewID); err != nil || removed != 0 {
			t.Fatalf("starred original was not retained past the cutoff: %d, %v", removed, err)
		}
		if err := older.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, filename := range []string{relay, relay, archive} {
		if result, err := sqlite.RestoreProfileBackup(ctx, mamDir, filename, mintDirectEchoID(t)); err != nil || result.Messages != 0 {
			t.Fatalf("reverse restore changed the logical message count: %+v, %v", result, err)
		}
	}
	mam, originalEngine, _ := openDirectEchoProfile(t, mamDir)
	if star, err := originalEngine.MessageStar(ctx, mamMessages[0].ID); err != nil || star.Starred || star.RevisionID != cleared.Star.RevisionID {
		t.Fatalf("the original observer kept an ancestor after restoring the causal unstar: %+v, %v", star, err)
	}
	setAgain, err := originalEngine.SetMessageStar(ctx, mintDirectEchoID(t), mamMessages[0].ID, cleared.Star.RevisionID, true)
	if err != nil || !setAgain.Star.Starred {
		t.Fatalf("the reverse observer could not extend the restored choice: %+v, %v", setAgain, err)
	}
	if err := mam.Close(); err != nil {
		t.Fatal(err)
	}
	secondRelay := writeRoomEchoBackup(t, mamDir)
	if result, err := sqlite.RestoreProfileBackup(ctx, liveDir, secondRelay, mintDirectEchoID(t)); err != nil || result.Messages != 0 {
		t.Fatalf("further relay changed the logical message count: %+v, %v", result, err)
	}
	live, receiver, _ = openDirectEchoProfile(t, liveDir)
	if chosen, err := receiver.MessageStar(ctx, local.ID); err != nil || !chosen.Starred || chosen.RevisionID != setAgain.Star.RevisionID {
		t.Fatalf("the star did not follow the second proven alias transition: %+v, %v", chosen, err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}

	concurrentDir := t.TempDir()
	if _, err := sqlite.RestoreProfileBackup(ctx, concurrentDir, archive, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	concurrent, conflicting, _ := openDirectEchoProfile(t, concurrentDir)
	independentClear, err := conflicting.SetMessageStar(ctx, mintDirectEchoID(t), mamMessages[0].ID, star.Star.RevisionID, false)
	if err != nil {
		t.Fatal(err)
	}
	independentSet, err := conflicting.SetMessageStar(ctx, mintDirectEchoID(t), mamMessages[0].ID, independentClear.Star.RevisionID, true)
	if err != nil || !independentSet.Star.Starred {
		t.Fatalf("could not establish a competing branch: %+v, %v", independentSet, err)
	}
	if err := concurrent.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.RestoreProfileBackup(ctx, concurrentDir, relay, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	_, conflicting, _ = openDirectEchoProfile(t, concurrentDir)
	if chosen, err := conflicting.MessageStar(ctx, mamMessages[0].ID); err != nil || !chosen.Starred || chosen.RevisionID != independentSet.Star.RevisionID {
		t.Fatalf("the archive replaced the destination's concurrent star choice: %+v, %v", chosen, err)
	}
}
