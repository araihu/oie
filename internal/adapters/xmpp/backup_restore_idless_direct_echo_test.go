package xmpp

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestBackupRestoreDirectEchoWithoutStanzaIDAfterQuoteExpiry(t *testing.T) {
	ctx := t.Context()
	source, sourceEngine, manager := openDirectEchoProfile(t, t.TempDir())
	account, chat := setupDirectEchoProfile(t, sourceEngine)
	now := time.Now().UTC()
	const secret = "the old quoted text"
	const answer = "the authored reply"
	const quotedWireID = "old-self-message"
	target := domain.MessageReference{Scope: "sender:" + account.Address, Author: account.Address, ID: quotedWireID}
	if err := sourceEngine.Receive(ctx, application.Incoming{
		AccountID: account.ID, Kind: "direct", Address: account.Address, IsOwn: true,
		ProviderID: quotedWireID, OriginalText: secret, CreatedAt: now.Add(-48 * time.Hour), Historical: true,
		References: []domain.MessageReference{target},
	}); err != nil {
		t.Fatal(err)
	}
	origin := mintDirectEchoID(t)
	quote := "> " + secret + "\n"
	replyXML := func(target string) string {
		return fmt.Sprintf(`<reply xmlns="urn:xmpp:reply:0" id="%s" to="%s"/><fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0"><body start="0" end="%d"/></fallback>`,
			target, account.Address, len([]rune(quote)))
	}
	packet := idlessDirectPacket(account, origin, quote+answer, replyXML(quotedWireID))
	receiveLiveDirectEchoPacket(t, manager, account, packet)
	before, err := source.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(before) != 2 || before[0].OwnOriginID != origin || before[0].Reply == nil || !before[0].Reply.Available {
		t.Fatalf("live quote fixture: %+v, %v", before, err)
	}
	days := 1
	if _, err := sourceEngine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if n, err := source.PurgeExpiredHistory(ctx, now.Add(time.Second), directEchoNewID); err != nil || n != 1 {
		t.Fatalf("expire quoted message: %d, %v", n, err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	sourceBackup := writeRoomEchoBackup(t, manager.dir)
	destinationDir := t.TempDir()
	if _, err := sqlite.RestoreProfileBackup(ctx, destinationDir, sourceBackup, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	destination, engine, _ := openDirectEchoProfile(t, destinationDir)
	receiveArchivedDirectEchoPacket(t, engine, account, origin, "archived-expired-quote", packet, now)
	after, err := destination.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID || after[0].RevisionID != before[0].RevisionID ||
		after[0].OriginalText != answer || after[0].Reply == nil || !after[0].Reply.OriginalExpired || after[0].Reply.Excerpt != "" {
		t.Fatalf("archive replay changed the restored redacted reply: %d rows, %v", len(after), err)
	}
	assertDirectEchoArchiveOwner(t, destinationDir, account, "archived-expired-quote", before[0].ID)

	changed := idlessDirectPacket(account, origin, quote+answer, replyXML("independent-target"))
	receiveArchivedDirectEchoPacket(t, engine, account, origin, "archived-different-quote", changed, now)
	after, err = destination.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(after) != 2 {
		t.Fatalf("distinct typed reply was merged with the retained reply: %d rows, %v", len(after), err)
	}
}

func TestBackupRestoreMergesIndependentDirectEchoWithoutStanzaID(t *testing.T) {
	for _, tc := range []struct {
		name            string
		archiveRetained bool
		redactLive      bool
		mismatch        string
	}{
		{name: "live-retained"},
		{name: "archive-retained", archiveRetained: true},
		{name: "redacted-live-retained", redactLive: true},
		{name: "redacted-live-imported", redactLive: true, archiveRetained: true},
		{name: "different-structured-mentions", mismatch: "mention"},
		{name: "different-typed-reply", mismatch: "reply"},
		{name: "different-verified-origin", mismatch: "origin", archiveRetained: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			baseDir := t.TempDir()
			base, baseEngine, _ := openDirectEchoProfile(t, baseDir)
			account, chat := setupDirectEchoProfile(t, baseEngine)
			now := time.Now().UTC()
			const answer = "text from another signed-in resource"
			const quotedID = "old-self-message"
			const quote = "> an old retained original\n"
			body, commonOptions := answer, ""
			if tc.redactLive {
				target := domain.MessageReference{Scope: "sender:" + account.Address, Author: account.Address, ID: quotedID}
				if err := baseEngine.Receive(ctx, application.Incoming{
					AccountID: account.ID, Kind: "direct", Address: account.Address, IsOwn: true,
					ProviderID: quotedID, OriginalText: "an old retained original", CreatedAt: now.Add(-48 * time.Hour),
					Historical: true, References: []domain.MessageReference{target},
				}); err != nil {
					t.Fatal(err)
				}
				body = quote + answer
				commonOptions = fmt.Sprintf(`<reply xmlns="urn:xmpp:reply:0" id="%s" to="%s"/><fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0"><body start="0" end="%d"/></fallback>`,
					quotedID, account.Address, len(quote))
			}
			if err := base.Close(); err != nil {
				t.Fatal(err)
			}
			seed := writeRoomEchoBackup(t, baseDir)
			liveDir, mamDir := t.TempDir(), t.TempDir()
			for _, dir := range []string{liveDir, mamDir} {
				if _, err := sqlite.RestoreProfileBackup(ctx, dir, seed, mintDirectEchoID(t)); err != nil {
					t.Fatal(err)
				}
			}

			live, liveEngine, manager := openDirectEchoProfile(t, liveDir)
			mam, engine, _ := openDirectEchoProfile(t, mamDir)
			origin := mintDirectEchoID(t)
			const archiveKey = "independent-direct-observation"
			receiveLiveDirectEchoPacket(t, manager, account, idlessDirectPacket(account, origin, body, commonOptions))
			archivedOrigin, options := origin, commonOptions
			switch tc.mismatch {
			case "mention":
				options = `<reference xmlns="urn:xmpp:reference:0" type="mention" uri="xmpp:juliet@localhost" begin="0" end="4"/>`
			case "reply":
				options = fmt.Sprintf(`<reply xmlns="urn:xmpp:reply:0" id="other-target" to="%s"/>`, account.Address)
			case "origin":
				archivedOrigin = mintDirectEchoID(t)
			}
			receiveArchivedDirectEchoPacket(t, engine, account, archivedOrigin, archiveKey, idlessDirectPacket(account, archivedOrigin, body, options), now)
			if tc.redactLive {
				days := 1
				if _, err := liveEngine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
					t.Fatal(err)
				}
				if n, err := live.PurgeExpiredHistory(ctx, now.Add(time.Second), directEchoNewID); n != 1 || err != nil {
					t.Fatalf("expire original on the live profile: %d, %v", n, err)
				}
			}
			sourceDir, destinationDir, retained := mamDir, liveDir, live
			beforeCount := 1
			if tc.archiveRetained {
				sourceDir, destinationDir, retained = liveDir, mamDir, mam
				if tc.redactLive {
					beforeCount++
				}
			}
			before, err := retained.Messages(ctx, chat.ID, "", 10)
			if err != nil || len(before) != beforeCount || (tc.redactLive && !tc.archiveRetained && before[0].OriginalText != answer) {
				t.Fatalf("independent destination fixture: %+v, %v", before, err)
			}
			for _, store := range []*sqlite.Store{live, mam} {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			sourceBackup := writeRoomEchoBackup(t, sourceDir)
			wantCount := beforeCount
			if tc.mismatch != "" {
				wantCount++
			}
			for pass := range 2 {
				report, err := sqlite.RestoreProfileBackup(ctx, destinationDir, sourceBackup, mintDirectEchoID(t))
				wantAdded := int64(0)
				if pass == 0 && tc.mismatch != "" {
					wantAdded = 1
				}
				if err != nil || report.Messages != wantAdded || report.PausedSends != 0 {
					t.Fatalf("apply backup pass %d: %+v, %v", pass+1, report, err)
				}
			}
			merged, mergedEngine, _ := openDirectEchoProfile(t, destinationDir)
			archived := idlessDirectPacket(account, archivedOrigin, body, options)
			receiveArchivedDirectEchoPacket(t, mergedEngine, account, archivedOrigin, archiveKey, archived, time.Now().UTC())
			after, err := merged.Messages(ctx, chat.ID, "", 10)
			if err != nil || len(after) != wantCount {
				t.Fatalf("restore changed direct echo count: got %d, want %d, %v", len(after), wantCount, err)
			}
			var retainedRevision bool
			for _, message := range after {
				retainedRevision = retainedRevision || (message.ID == before[0].ID && message.RevisionID == before[0].RevisionID)
			}
			if !retainedRevision {
				t.Fatal("restore replaced the destination observation or revision")
			}
			if tc.mismatch == "" || tc.archiveRetained {
				assertDirectEchoArchiveOwner(t, destinationDir, account, archiveKey, before[0].ID)
			}
		})
	}
}

func directEchoNewID() (domain.ID, error) {
	id, err := uuid.NewV7()
	return domain.ID(id.String()), err
}

func mintDirectEchoID(t *testing.T) domain.ID {
	t.Helper()
	id, err := directEchoNewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func openDirectEchoProfile(t *testing.T, dir string) (*sqlite.Store, *application.Engine, *Manager) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), dir, mintDirectEchoID(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine, err := application.New(store, "local", directEchoNewID, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := New(dir, nil)
	manager.host = engine
	engine.SetProtocols(manager)
	return store, engine, manager
}

func setupDirectEchoProfile(t *testing.T, engine *application.Engine) (domain.Account, domain.Conversation) {
	t.Helper()
	registered, err := engine.Register(t.Context(), mintDirectEchoID(t), application.RegisterInput{
		Protocol: "xmpp", Address: "alice@localhost", Label: "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := engine.OpenConversation(t.Context(), mintDirectEchoID(t), registered.Account.ID, registered.Account.Address)
	if err != nil {
		t.Fatal(err)
	}
	return registered.Account, chat.Conversation
}

func idlessDirectPacket(account domain.Account, origin domain.ID, body, options string) string {
	return fmt.Sprintf(`<message xmlns="jabber:client" from="%s/other-client" to="%s" type="chat"><body>%s</body><origin-id xmlns="urn:xmpp:sid:0" id="%s"/>%s</message>`,
		account.Address, account.Address, escaped(body), origin, options)
}

func receiveLiveDirectEchoPacket(t *testing.T, manager *Manager, account domain.Account, packet string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(packet))
	token, err := decoder.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := token.(xml.StartElement)
	if err := manager.handle(t.Context(), &entry{account: account}, tokens{decoder, xml.NewEncoder(&bytes.Buffer{})}, &start); err != nil {
		t.Fatal(err)
	}
}

func receiveArchivedDirectEchoPacket(t *testing.T, engine *application.Engine, account domain.Account, origin domain.ID, key, packet string, at time.Time) {
	t.Helper()
	raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/>%s</forwarded></result>`,
		key, at.Format(time.RFC3339Nano), packet)
	var result archiveResult
	if err := xml.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	incoming, ok, err := archivedIncoming(account, account.Address, &result, archiveTrust{})
	if err != nil || !ok || !incoming.IsOwn || incoming.CanonicalID != origin {
		t.Fatalf("archive direct fixture: %+v, %v, %v", incoming, ok, err)
	}
	if err := engine.Receive(t.Context(), incoming); err != nil {
		t.Fatal(err)
	}
}

func assertDirectEchoArchiveOwner(t *testing.T, dir string, account domain.Account, archiveKey string, want domain.ID) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var got domain.ID
	err = db.QueryRowContext(t.Context(), `SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?`,
		account.ID, account.Address, archiveKey).Scan(&got)
	if err != nil || got != want {
		t.Fatalf("archive key owner %s, want %s, %v", got, want, err)
	}
}
