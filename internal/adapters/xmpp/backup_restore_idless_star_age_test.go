package xmpp

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRelayedCausalUnstarAcrossCutoff(t *testing.T) {
	for _, tc := range []struct {
		name              string
		multiHop, sibling bool
	}{
		{name: "re-exported alias owns the unstar"},
		{name: "an older intermediary has no current star", multiHop: true},
		{name: "repeat excludes an unrelated revision of the same carrier", sibling: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			baseDir := t.TempDir()
			base, engine, _ := openDirectEchoProfile(t, baseDir)
			account, chat := setupDirectEchoProfile(t, engine)
			closeStore := func(store *sqlite.Store) {
				t.Helper()
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			closeStore(base)
			seed := writeRoomEchoBackup(t, baseDir)

			apply := func(dir, file string, messages int64) {
				t.Helper()
				if result, err := sqlite.RestoreProfileBackup(ctx, dir, file, mintDirectEchoID(t)); err != nil || result.Messages != messages {
					t.Fatalf("restore with %d new logical messages: %+v, %v", messages, result, err)
				}
			}
			type observation struct {
				dir     string
				message domain.Message
			}
			origin := mintDirectEchoID(t)
			packet := idlessDirectPacket(account, origin, "the same old direct message", "")
			observedAt := time.Now().Add(-48 * time.Hour)
			observe := func(key string) observation {
				t.Helper()
				dir := t.TempDir()
				apply(dir, seed, 0)
				store, engine, _ := openDirectEchoProfile(t, dir)
				defer closeStore(store)
				receiveArchivedDirectEchoPacket(t, engine, account, origin, key, packet, observedAt)
				history, err := store.Messages(ctx, chat.ID, "", 10)
				if err != nil || len(history) != 1 || history[0].OwnOriginID != origin {
					t.Fatalf("independent old observation: %+v, %v", history, err)
				}
				return observation{dir: dir, message: history[0]}
			}
			set := func(dir string, id, previous domain.ID, starred bool) domain.MessageStar {
				t.Helper()
				store, engine, _ := openDirectEchoProfile(t, dir)
				defer closeStore(store)
				result, err := engine.SetMessageStar(ctx, mintDirectEchoID(t), id, previous, starred)
				if err != nil || result.Star.Starred != starred {
					t.Fatalf("explicit star operation: %+v, %v", result, err)
				}
				return result.Star
			}
			setShortRetention := func(dir string) {
				t.Helper()
				store, engine, _ := openDirectEchoProfile(t, dir)
				defer closeStore(store)
				days := 1
				if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
					t.Fatal(err)
				}
				if n, err := store.PurgeExpiredHistory(ctx, time.Now(), directEchoNewID); err != nil || n != 0 {
					t.Fatalf("purge before restore: %d messages, %v", n, err)
				}
			}

			first, second := observe("first"), observe("second")
			oldStar := set(first.dir, first.message.ID, "", true)
			ancestorArchive := writeRoomEchoBackup(t, first.dir)
			apply(second.dir, ancestorArchive, 0)
			var unrelated domain.MessageStar
			var fork string
			if tc.sibling {
				fork = t.TempDir()
				apply(fork, writeRoomEchoBackup(t, second.dir), 1)
				unrelated = set(fork, second.message.ID, oldStar.RevisionID, false)
			}
			unstar := set(second.dir, second.message.ID, oldStar.RevisionID, false)
			if tc.sibling {
				apply(second.dir, writeRoomEchoBackup(t, fork), 0)
			}
			secondArchive := writeRoomEchoBackup(t, second.dir)
			carriers := []domain.Message{first.message, second.message}
			var relay string
			if tc.multiHop {
				third := observe("third")
				apply(third.dir, secondArchive, 0)
				restar := set(third.dir, third.message.ID, unstar.RevisionID, true)
				unstar = set(third.dir, third.message.ID, restar.RevisionID, false)
				carriers = append(carriers, third.message)
				relay = writeRoomEchoBackup(t, third.dir)
			} else if tc.sibling {
				relay = secondArchive
			} else {
				apply(first.dir, secondArchive, 0)
				relay = writeRoomEchoBackup(t, first.dir)
			}
			check := func(dir string, want domain.MessageStar) {
				t.Helper()
				store, engine, _ := openDirectEchoProfile(t, dir)
				defer closeStore(store)
				star, err := engine.MessageStar(ctx, first.message.ID)
				if err != nil || star.Starred != want.Starred || star.RevisionID != want.RevisionID {
					t.Fatalf("retained star: got %+v, want %+v, %v", star, want, err)
				}
				history, err := store.Messages(ctx, chat.ID, "", 10)
				if err != nil || len(history) != 1 || history[0].ID != first.message.ID || history[0].RevisionID != first.message.RevisionID {
					t.Fatalf("restore changed the retained logical observation: %+v, %v", history, err)
				}
				if !want.Starred {
					for _, carrier := range carriers {
						raw, err := store.MessageSource(ctx, carrier.ID)
						if err != nil || raw.RevisionID != carrier.RevisionID {
							t.Fatalf("causal physical revision %s did not survive: %+v, %v", carrier.RevisionID, raw, err)
						}
					}
				}
			}

			destination := t.TempDir()
			apply(destination, ancestorArchive, 1)
			setShortRetention(destination)
			retainsAlternative := func(dir string) bool {
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
				var exists bool
				if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM message_star_revisions WHERE id=?)`, unrelated.RevisionID).Scan(&exists); err != nil {
					t.Fatal(err)
				}
				return exists
			}
			if tc.sibling && !retainsAlternative(second.dir) {
				t.Fatal("the relay did not contain the unrelated revision")
			}
			var cursor int64
			for pass := range 2 {
				apply(destination, relay, 0)
				check(destination, unstar)
				if tc.sibling {
					store, _, _ := openDirectEchoProfile(t, destination)
					_, current, err := store.Snapshot(ctx)
					closeStore(store)
					if err != nil || (pass > 0 && current != cursor) || retainsAlternative(destination) {
						t.Fatalf("applying the archive again admitted the unrelated ancestry or emitted changes: cursor %d to %d, %v", cursor, current, err)
					}
					cursor = current
				}
			}

			// The re-export includes the intermediary even though that physical
			// observation has no current star of its own.
			forwarded := t.TempDir()
			apply(forwarded, ancestorArchive, 1)
			setShortRetention(forwarded)
			apply(forwarded, writeRoomEchoBackup(t, destination), 0)
			check(forwarded, unstar)

			independent := t.TempDir()
			apply(independent, ancestorArchive, 1)
			cleared := set(independent, first.message.ID, oldStar.RevisionID, false)
			keep := set(independent, first.message.ID, cleared.RevisionID, true)
			setShortRetention(independent)
			apply(independent, relay, 0)
			check(independent, keep)
			store, _, _ := openDirectEchoProfile(t, independent)
			for _, carrier := range carriers[1:] {
				if _, err := store.MessageSource(ctx, carrier.ID); !errors.Is(err, application.ErrNotFound) {
					t.Fatalf("an unrelated branch admitted old physical observation %s: %v", carrier.ID, err)
				}
			}
			closeStore(store)

			fresh := t.TempDir()
			apply(fresh, seed, 0)
			setShortRetention(fresh)
			apply(fresh, relay, 0)

			if tc.sibling {
				setRetention := func(days int) {
					t.Helper()
					store, engine, _ := openDirectEchoProfile(t, destination)
					defer closeStore(store)
					policy, err := engine.ConversationRetention(ctx, chat.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := engine.SetConversationRetention(ctx, chat.ID, policy.Revision, &days); err != nil {
						t.Fatal(err)
					}
				}
				setRetention(0)
				apply(destination, relay, 0)
				check(destination, unstar)
				if !retainsAlternative(destination) {
					t.Fatal("ordinary eligibility did not admit the valid revision after widening")
				}
				setRetention(1)
			}

			// Even widening afterward must leave explicit expiry evidence in
			// force when the old archive still retains the ancestor star.
			store, engine, _ = openDirectEchoProfile(t, destination)
			if n, err := store.PurgeExpiredHistory(ctx, time.Now(), directEchoNewID); err != nil || n != 1 {
				t.Fatalf("the causal unstar did not let the expired message leave: %d, %v", n, err)
			}
			policy, err := engine.ConversationRetention(ctx, chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			unlimited := 0
			if _, err := engine.SetConversationRetention(ctx, chat.ID, policy.Revision, &unlimited); err != nil {
				t.Fatal(err)
			}
			closeStore(store)
			apply(destination, ancestorArchive, 0)
			apply(destination, relay, 0)
			for _, dir := range []string{fresh, destination} {
				store, _, _ := openDirectEchoProfile(t, dir)
				for _, carrier := range carriers {
					if _, err := store.MessageSource(ctx, carrier.ID); !errors.Is(err, application.ErrNotFound) {
						t.Fatalf("restore returned an excluded observation %s: %v", carrier.ID, err)
					}
				}
				closeStore(store)
			}
		})
	}
}
