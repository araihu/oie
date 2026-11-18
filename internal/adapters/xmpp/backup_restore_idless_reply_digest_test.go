package xmpp

import (
	"fmt"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreIdlessReplyUsesExactFallbackDigest(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		expireSource, sameSpans bool
	}{
		{name: "different redacted spans", expireSource: true},
		{name: "different raw and redacted spans"},
		{name: "matching redacted spans", expireSource: true, sameSpans: true},
		{name: "matching raw and redacted spans", sameSpans: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			baseDir := t.TempDir()
			base, engine, _ := openDirectEchoProfile(t, baseDir)
			account, chat := setupDirectEchoProfile(t, engine)
			now := time.Now().UTC()
			const secret, answer = "the expired quotation text", "identical retained answer"
			target := domain.MessageReference{Scope: "sender:" + account.Address, Author: account.Address, ID: "old-quoted-message"}
			if err := engine.Receive(ctx, application.Incoming{
				AccountID: account.ID, Kind: "direct", Address: account.Address, IsOwn: true, Historical: true,
				ProviderID: target.ID, OriginalText: secret, CreatedAt: now.Add(-48 * time.Hour), References: []domain.MessageReference{target},
			}); err != nil {
				t.Fatal(err)
			}
			if err := base.Close(); err != nil {
				t.Fatal(err)
			}
			seed := writeRoomEchoBackup(t, baseDir)
			sourceDir, destinationDir := t.TempDir(), t.TempDir()
			for _, dir := range []string{sourceDir, destinationDir} {
				if _, err := sqlite.RestoreProfileBackup(ctx, dir, seed, mintDirectEchoID(t)); err != nil {
					t.Fatal(err)
				}
			}
			source, sourceEngine, _ := openDirectEchoProfile(t, sourceDir)
			destination, receiver, manager := openDirectEchoProfile(t, destinationDir)
			origin := mintDirectEchoID(t)
			quote := "> " + secret + "\n"
			reply := fmt.Sprintf(`<reply xmlns="urn:xmpp:reply:0" id="%s" to="%s"/>`, target.ID, account.Address)
			packet := func(spans string) string {
				return idlessDirectPacket(account, origin, quote+answer, reply+`<fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0">`+spans+"</fallback>")
			}
			spans := fmt.Sprintf(`<body start="0" end="%d"/>`, len(quote))
			observedSpans := spans
			if !tc.sameSpans {
				observedSpans = fmt.Sprintf(`<body start="0" end="3"/><body start="3" end="%d"/>`, len(quote))
			}
			receiveLiveDirectEchoPacket(t, manager, account, packet(spans))
			receiveArchivedDirectEchoPacket(t, sourceEngine, account, origin, "archived-reply", packet(observedSpans), now)
			expire := func(store *sqlite.Store, engine *application.Engine) {
				t.Helper()
				days := 1
				if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
					t.Fatal(err)
				}
				if count, err := store.PurgeExpiredHistory(ctx, now.Add(time.Second), directEchoNewID); err != nil || count != 1 {
					t.Fatalf("expected to purge only the quoted message: %d, %v", count, err)
				}
			}
			expire(destination, receiver)
			if tc.expireSource {
				expire(source, sourceEngine)
			}
			for _, store := range []*sqlite.Store{source, destination} {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			archive := writeRoomEchoBackup(t, sourceDir)
			added := int64(1)
			if tc.sameSpans {
				added = 0
			}
			for _, want := range []int64{added, 0} {
				if result, err := sqlite.RestoreProfileBackup(ctx, destinationDir, archive, mintDirectEchoID(t)); err != nil || result.Messages != want {
					t.Fatalf("restore chose the wrong fallback identity: %+v, %v", result, err)
				}
			}
			destination, _, _ = openDirectEchoProfile(t, destinationDir)
			rows, err := destination.Messages(ctx, chat.ID, "", 10)
			if err != nil || int64(len(rows)) != 1+added {
				t.Fatalf("wrong number of independently observed replies: %+v, %v", rows, err)
			}
			for _, row := range rows {
				if row.OriginalText != answer {
					t.Fatalf("restore recovered the expired fallback: %+v", row)
				}
			}
		})
	}
}
