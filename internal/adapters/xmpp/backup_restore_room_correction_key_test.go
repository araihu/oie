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

func TestBackupRestoreMatchesLegacyAndScopedRoomCorrectionOwners(t *testing.T) {
	for _, test := range []struct {
		name          string
		differentBody bool
	}{
		{name: "same wire"},
		{name: "competing wire", differentBody: true},
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
			receive(editor, originalID, "", "original", "the own observed original")
			if err := base.Close(); err != nil {
				t.Fatal(err)
			}
			seed := writeRoomEchoBackup(t, baseDir)

			type profile struct {
				dir, key, body, backup string
				source                 domain.Message
			}
			left, right := &profile{dir: t.TempDir(), key: "legacy-correction", body: "the verified correction"},
				&profile{dir: t.TempDir(), key: "scoped-correction", body: "the verified correction"}
			if test.differentBody {
				right.body = "another body for the same authenticated correction"
			}
			for _, side := range []*profile{left, right} {
				if _, err := sqlite.RestoreProfileBackup(ctx, side.dir, seed, mintDirectEchoID(t)); err != nil {
					t.Fatal(err)
				}
				store, engine, _ := openDirectEchoProfile(t, side.dir)
				legacy := receive(engine, correctionID, originalID, side.key, side.body)
				db, err := sql.Open("sqlite", filepath.Join(side.dir, "oie.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				owner := readRoomCorrectionArchiveOwner(t, db, account.ID, room.Address, side.key)
				if side == left {
					restoreLegacyRoomProviderKey(t, db, owner, legacy)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				side.source, err = store.MessageSource(ctx, owner)
				if err != nil || (side.source.ProviderID == legacy) != (side == left) {
					t.Fatalf("room provider key fixture: %+v, %v", side.source, err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				side.backup = writeRoomEchoBackup(t, side.dir)
			}
			if left.source.ID == right.source.ID {
				t.Fatal("expected two independent physical observations")
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
				store, engine, _ := openDirectEchoProfile(t, retained.dir)
				receive(engine, correctionID, originalID, "later-room-observation", retained.body)
				history, err := store.Messages(ctx, room.ID, "", 10)
				if err != nil || len(history) != 1 || !history[0].Edited ||
					history[0].ProjectionSourceID != retained.source.ID || history[0].RevisionID != retained.source.RevisionID {
					t.Fatalf("restore replaced the retained room correction: %+v, %v", history, err)
				}
				raw, err := store.MessageSource(ctx, retained.source.ID)
				if err != nil || raw.ProviderID != retained.source.ProviderID {
					t.Fatalf("restore changed a persisted provider key: %+v, %v", raw, err)
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
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
