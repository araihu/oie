package xmpp

import (
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
)

func TestBackupRestoreMatchesIndependentlyObservedRoomCorrections(t *testing.T) {
	for _, test := range []struct {
		name            string
		legacy          bool
		independentRoot bool
		differentBody   bool
		sourceStar      bool
		localUnstar     bool
	}{
		{name: "legacy and scoped witnesses", legacy: true},
		{name: "two scoped witnesses"},
		{name: "independent originals and mixed witnesses", legacy: true, independentRoot: true},
		{name: "inherit source star on independent original", legacy: true, independentRoot: true, sourceStar: true},
		{name: "preserve explicit unstar on independent original", legacy: true, independentRoot: true, sourceStar: true, localUnstar: true},
		{name: "competing legacy wire", legacy: true, differentBody: true},
		{name: "competing scoped wire", differentBody: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			baseDir := t.TempDir()
			base, editor, _ := openDirectEchoProfile(t, baseDir)
			account, _ := setupDirectEchoProfile(t, editor)
			opened, err := editor.OpenRoom(ctx, mintDirectEchoID(t), account.ID, "room@conference.localhost", "Alice", "", false)
			if err != nil {
				t.Fatal(err)
			}
			room := opened.Conversation
			author := room.Address + "/Alice"
			at := time.Now().UTC()
			receive := func(engine *application.Engine, id, replaces domain.ID, key, body string) string {
				t.Helper()
				var correction string
				if replaces != "" {
					correction = fmt.Sprintf(`<replace xmlns="urn:xmpp:message-correct:0" id="%s"/>`, replaces)
				}
				raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s" type="groupchat" id="%s"><body>%s</body>%s<occupant-id xmlns="urn:xmpp:occupant-id:0" id="one-verified-occupant"/><x xmlns="http://jabber.org/protocol/muc#user"><item jid="%s/other-client"/></x></message></forwarded></result>`,
					key, at.Format(time.RFC3339Nano), author, id, escaped(body), correction, account.Address)
				var result archiveResult
				if err := xml.Unmarshal([]byte(raw), &result); err != nil {
					t.Fatal(err)
				}
				incoming, ok, err := archivedIncoming(account, room.Address, &result, archiveTrust{SIDVerified: true, OccupantVerified: true})
				if err != nil || !ok || !incoming.IsOwn || incoming.Correction == nil || incoming.Correction.ID != string(id) {
					t.Fatalf("authenticated room observation: %+v, %v, %v", incoming, ok, err)
				}
				if err := engine.Receive(ctx, incoming); err != nil {
					t.Fatal(err)
				}
				return incoming.ProviderID
			}
			originalID, correctionID := mintDirectEchoID(t), mintDirectEchoID(t)
			if !test.independentRoot {
				receive(editor, originalID, "", "original", "the own observed original")
			}
			if err := base.Close(); err != nil {
				t.Fatal(err)
			}
			seed := writeRoomEchoBackup(t, baseDir)

			type profile struct {
				dir, key, body, backup string
				root, source           domain.Message
				star                   domain.MessageStar
			}
			left, right := &profile{dir: t.TempDir(), key: "first-correction", body: "the verified correction"},
				&profile{dir: t.TempDir(), key: "second-correction", body: "the verified correction"}
			if test.differentBody {
				right.body = "another body for the same authenticated correction"
			}
			for _, side := range []*profile{left, right} {
				if _, err := sqlite.RestoreProfileBackup(ctx, side.dir, seed, mintDirectEchoID(t)); err != nil {
					t.Fatal(err)
				}
				store, engine, _ := openDirectEchoProfile(t, side.dir)
				rootKey := "original"
				if test.independentRoot {
					rootKey = side.key + "-root"
					receive(engine, originalID, "", "original", "the own observed original")
					receive(engine, originalID, "", rootKey, "the own observed original")
				}
				legacy := receive(engine, correctionID, originalID, side.key, side.body)
				db, err := sql.Open("sqlite", filepath.Join(side.dir, "oie.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				owner := readRoomCorrectionArchiveOwner(t, db, account.ID, room.Address, side.key)
				root := readRoomCorrectionArchiveOwner(t, db, account.ID, room.Address, rootKey)
				isLegacy := test.legacy && side == left
				if isLegacy {
					restoreLegacyRoomProviderKey(t, db, owner, legacy)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				side.source, err = store.MessageSource(ctx, owner)
				if err != nil || (side.source.ProviderID == legacy) != isLegacy {
					t.Fatalf("room provider key fixture: %+v, %v", side.source, err)
				}
				side.root, err = store.MessageSource(ctx, root)
				if err != nil {
					t.Fatal(err)
				}
				if test.sourceStar && (side == right || test.localUnstar) {
					star, err := engine.SetMessageStar(ctx, mintDirectEchoID(t), root, "", true)
					if err != nil || !star.Star.Starred {
						t.Fatalf("star fixture: %+v, %v", star, err)
					}
					if side == left {
						star, err = engine.SetMessageStar(ctx, mintDirectEchoID(t), root, star.Star.RevisionID, false)
						if err != nil || star.Star.Starred || star.Star.RevisionID == "" {
							t.Fatalf("unstar fixture: %+v, %v", star, err)
						}
					}
					side.star = star.Star
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				side.backup = writeRoomEchoBackup(t, side.dir)
			}
			if left.source.ID == right.source.ID {
				t.Fatal("expected two independent physical observations")
			}
			if (left.root.ID != right.root.ID) != test.independentRoot {
				t.Fatal("original source IDs do not match the fixture")
			}

			for _, order := range [][2]*profile{{left, right}, {right, left}} {
				retained, incoming := order[0], order[1]
				summary, err := sqlite.RestoreProfileBackup(ctx, retained.dir, incoming.backup, mintDirectEchoID(t))
				if test.differentBody {
					if err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
						t.Fatalf("restore accepted conflicting bodies for one authenticated correction: %+v, %v", summary, err)
					}
					continue
				}
				if err != nil || summary.Messages != 0 {
					t.Fatalf("restore of equivalent observed correction: %+v, %v", summary, err)
				}
				repeat, err := sqlite.RestoreProfileBackup(ctx, retained.dir, incoming.backup, mintDirectEchoID(t))
				if err != nil || repeat != (sqlite.RestoreSummary{}) {
					t.Fatalf("repeat of equivalent observed correction: %+v, %v", repeat, err)
				}
				store, engine, _ := openDirectEchoProfile(t, retained.dir)
				receive(engine, correctionID, originalID, incoming.key, retained.body)
				receive(engine, correctionID, originalID, "later-room-observation", retained.body)
				history, err := store.Messages(ctx, room.ID, "", 10)
				if err != nil || len(history) != 1 || !history[0].Edited || history[0].ID != retained.root.ID ||
					history[0].ProjectionSourceID != retained.source.ID || history[0].RevisionID != retained.source.RevisionID {
					t.Fatalf("restore replaced the retained room correction: %+v, %v", history, err)
				}
				raw, err := store.MessageSource(ctx, retained.source.ID)
				if err != nil || raw.ProviderID != retained.source.ProviderID {
					t.Fatalf("restore changed a persisted provider key: %+v, %v", raw, err)
				}
				rawRoot, err := store.MessageSource(ctx, retained.root.ID)
				if err != nil || rawRoot.RevisionID != retained.root.RevisionID || rawRoot.ProviderID != retained.root.ProviderID {
					t.Fatalf("restore changed the original source: %+v, %v", rawRoot, err)
				}
				db, err := sql.Open("sqlite", filepath.Join(retained.dir, "oie.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{retained.key, incoming.key, "later-room-observation"} {
					if owner := readRoomCorrectionArchiveOwner(t, db, account.ID, room.Address, key); owner != retained.source.ID {
						t.Fatalf("archive duplicate moved the retained key %q: %s", key, owner)
					}
				}
				if test.independentRoot {
					for _, key := range []string{"original", retained.key + "-root", incoming.key + "-root"} {
						if owner := readRoomCorrectionArchiveOwner(t, db, account.ID, room.Address, key); owner != retained.root.ID {
							t.Fatalf("merge moved the retained original archive key %q: %s", key, owner)
						}
					}
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if test.sourceStar {
					wantStar := retained.star
					if wantStar.RevisionID == "" {
						wantStar = incoming.star
					}
					assertStar := func(engine *application.Engine) {
						t.Helper()
						star, err := engine.MessageStar(ctx, retained.root.ID)
						if err != nil || star.RevisionID != wantStar.RevisionID || star.Starred != wantStar.Starred {
							t.Fatalf("verified room star: got %+v, want %+v, %v", star, wantStar, err)
						}
					}
					assertLatest := func(store *sqlite.Store) {
						t.Helper()
						current, err := store.Messages(ctx, room.ID, "", 10)
						if err != nil || len(current) != 1 ||
							current[0].ID != retained.root.ID || domain.DisplayBody(current[0]) != "the subsequent correction" {
							t.Fatalf("the verified room correction lost its single original: %+v, %v", current, err)
						}
					}
					assertStar(engine)
					receive(engine, mintDirectEchoID(t), correctionID, "subsequent-room-edit", "the subsequent correction")
					assertLatest(store)
					assertStar(engine)
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					relayed := writeRoomEchoBackup(t, retained.dir)
					fresh := t.TempDir()
					for range 2 {
						if _, err := sqlite.RestoreProfileBackup(ctx, fresh, relayed, mintDirectEchoID(t)); err != nil {
							t.Fatal(err)
						}
					}
					store, engine, _ = openDirectEchoProfile(t, fresh)
					assertStar(engine)
					assertLatest(store)
					hidden, err := store.MessageSource(ctx, incoming.root.ID)
					if err != nil || hidden.ProviderID != "" || hidden.RevisionID != incoming.root.RevisionID {
						t.Fatalf("re-export lost the immutable hidden observation: %+v, %v", hidden, err)
					}
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
