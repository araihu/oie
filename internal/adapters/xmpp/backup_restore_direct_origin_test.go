package xmpp

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestDirectOwnOriginIDAcrossStanzaIDs(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		live, omitStanzaID, wrongPeer  bool
		foreignSender, duplicateOrigin bool
		oldObservation                 string
		alternateUUID                  bool
	}{
		{name: "archived own origin with another stanza ID"},
		{name: "archived own origin with another UUID stanza ID", alternateUUID: true},
		{name: "archived own origin without a stanza ID", omitStanzaID: true},
		{name: "live own origin with another stanza ID", live: true},
		{name: "live own origin without a stanza ID", live: true, omitStanzaID: true},
		{name: "own origin in another conversation", wrongPeer: true},
		{name: "foreign sender claiming the origin", foreignSender: true},
		{name: "multiple origin elements", duplicateOrigin: true},
		{name: "prior decoder observation in the backup", oldObservation: "backup"},
		{name: "prior decoder guessed another UUID as the origin", oldObservation: "backup", alternateUUID: true},
		{name: "prior decoder observation in the destination", oldObservation: "destination"},
		{name: "multiple retained sender identifiers", oldObservation: "ambiguous"},
		{name: "multiple retained sender identifiers and a native UUID provider", oldObservation: "ambiguous", alternateUUID: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			newID := func() (domain.ID, error) {
				id, err := uuid.NewV7()
				return domain.ID(id.String()), err
			}
			mint := func() domain.ID {
				t.Helper()
				id, err := newID()
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			open := func(dir string) (*sqlite.Store, *application.Engine) {
				t.Helper()
				store, err := sqlite.Open(ctx, dir, mint())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				engine, err := application.New(store, "local", newID, time.Now)
				if err != nil {
					t.Fatal(err)
				}
				engine.SetProtocols(New(dir, nil))
				return store, engine
			}
			localDir, witnessDir := t.TempDir(), t.TempDir()
			store, engine := open(localDir)
			registered, err := engine.Register(ctx, mint(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
			if err != nil {
				t.Fatal(err)
			}
			account := registered.Account
			peer := "bob@localhost"
			if tc.live {
				peer = account.Address
			}
			opened, err := engine.OpenConversation(ctx, mint(), account.ID, peer)
			if err != nil {
				t.Fatal(err)
			}
			chat := opened.Conversation
			if !tc.live {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				database, err := os.ReadFile(filepath.Join(localDir, "oie.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(witnessDir, "oie.sqlite"), database, 0600); err != nil {
					t.Fatal(err)
				}
				store, engine = open(localDir)
			}
			const body = "the original direct send"
			key := mint()
			accepted, err := engine.SendMessage(ctx, key, chat.ID, body)
			if err != nil {
				t.Fatal(err)
			}
			origin := accepted.Message.ID
			from, to := account.Address+"/other-client", peer
			if tc.wrongPeer {
				to = "carol@localhost"
			}
			if tc.foreignSender {
				from, to = peer+"/other-client", account.Address
			}
			id := ` id="provider-stanza-id"`
			var replacementID domain.ID
			var legacyMessage domain.Message
			ambiguousProvider := tc.oldObservation == "ambiguous" && tc.alternateUUID
			if tc.alternateUUID {
				if ambiguousProvider {
					unrelated, err := engine.SendMessage(ctx, mint(), chat.ID, "another actual queued send")
					if err != nil {
						t.Fatal(err)
					}
					replacementID = unrelated.Message.ID
				} else {
					replacementID = mint()
				}
				id = fmt.Sprintf(" id=%q", replacementID)
			}
			if tc.omitStanzaID {
				id = ""
			}
			originXML := fmt.Sprintf(`<origin-id xmlns="urn:xmpp:sid:0" id="%s"/>`, origin)
			if tc.duplicateOrigin {
				originXML += originXML
			}
			packet := fmt.Sprintf(`<message xmlns="jabber:client" from="%s" to="%s"%s type="chat"><body>%s</body>%s</message>`,
				from, to, id, body, originXML)
			if tc.live {
				manager := New(localDir, nil)
				manager.host = engine
				connection := &entry{account: account}
				for range 2 {
					decoder := xml.NewDecoder(strings.NewReader(packet))
					token, err := decoder.Token()
					if err != nil {
						t.Fatal(err)
					}
					start := token.(xml.StartElement)
					if err := manager.handle(ctx, connection, tokens{decoder, xml.NewEncoder(&bytes.Buffer{})}, &start); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="direct-archive">
  <forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/>%s</forwarded>
</result>`, time.Now().UTC().Format(time.RFC3339Nano), packet)
				var archived archiveResult
				if err := xml.Unmarshal([]byte(raw), &archived); err != nil {
					t.Fatal(err)
				}
				incoming, ok, err := archivedIncoming(account, account.Address, &archived, archiveTrust{})
				var wantOrigin domain.ID
				if !tc.foreignSender && !tc.duplicateOrigin {
					wantOrigin = origin
				}
				if err != nil || !ok || incoming.CanonicalID != wantOrigin || incoming.IsOwn == tc.foreignSender {
					t.Fatalf("wrong source identity after decoding the direct archive: %+v, %v, %v", incoming, ok, err)
				}
				witness, observer := open(witnessDir)
				if tc.oldObservation != "" {
					// Earlier direct decoders omitted the origin field while
					// retaining the validated sender reference.
					incoming.CanonicalID = ""
				}
				receiver := observer
				if tc.oldObservation == "destination" {
					receiver = engine
				}
				if err := receiver.Receive(ctx, incoming); err != nil {
					t.Fatal(err)
				}
				if tc.oldObservation == "ambiguous" {
					reference := incoming.References[0]
					reference.ID = string(mint())
					incoming.References = []domain.MessageReference{reference}
					if err := receiver.Receive(ctx, incoming); err != nil {
						t.Fatal(err)
					}
					if ambiguousProvider {
						messages, err := witness.Messages(ctx, chat.ID, "", 10)
						if err != nil || len(messages) != 1 || messages[0].OwnOriginID != replacementID {
							t.Fatalf("fixture did not retain the guessed provider: %+v, %v", messages, err)
						}
						legacyMessage = messages[0]
					}
				}
				if err := witness.Close(); err != nil {
					t.Fatal(err)
				}
				archive := writeRoomEchoBackup(t, witnessDir)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := sqlite.RestoreProfileBackup(ctx, localDir, archive, mint()); err != nil {
					t.Fatal(err)
				}
				if result, err := sqlite.RestoreProfileBackup(ctx, localDir, archive, mint()); err != nil || result != (sqlite.RestoreSummary{}) {
					t.Fatalf("repeated direct archive changed the result: %+v, %v", result, err)
				}
				store, engine = open(localDir)
			}
			wantDue, wantRows, wantState := 0, 1, "submitted"
			if tc.wrongPeer || tc.foreignSender || tc.duplicateOrigin || tc.oldObservation == "ambiguous" {
				wantDue, wantState = 1, "queued"
			} else if tc.oldObservation != "" {
				wantState = "unknown"
			}
			if tc.foreignSender || tc.duplicateOrigin || tc.oldObservation != "" {
				wantRows = 2
			}
			if ambiguousProvider {
				wantDue, wantRows = 2, 3
			}
			due, err := store.PendingMessages(ctx, time.Now().Add(time.Hour))
			if err != nil || len(due) != wantDue || (wantDue > 0 && due[0].ID != origin) ||
				(ambiguousProvider && due[1].ID != replacementID) {
				t.Fatalf("wrong original send eligibility after observing a direct stanza: %+v, %v", due, err)
			}
			got, err := store.Message(ctx, origin)
			if err != nil || got.SendState != wantState || got.ProviderID != string(origin) || got.OriginalText != body {
				t.Fatalf("origin did not preserve the intended direct send: %+v, %v", got, err)
			}
			if wantState == "submitted" && (len(got.References) != 1 || got.References[0].ID != string(origin) ||
				got.References[0].Scope != "sender:"+account.Address || got.References[0].Author != account.Address) {
				t.Fatalf("the confirmed message lost its scoped origin reference: %+v", got.References)
			}
			history, err := store.Messages(ctx, chat.ID, "", 10)
			if err != nil || len(history) != wantRows {
				t.Fatalf("expected %d logical messages, got %+v, %v", wantRows, history, err)
			}
			if ambiguousProvider {
				kept, err := store.Message(ctx, legacyMessage.ID)
				if err != nil || kept.RevisionID != legacyMessage.RevisionID || kept.OriginalText != body || kept.OwnOriginID != "" {
					t.Fatalf("the legacy observation lost its revision or kept the guessed origin: %+v, %v", kept, err)
				}
			}
			if replayed, err := engine.SendMessage(ctx, key, chat.ID, body); err != nil || !replayed.Replayed || replayed.Message.ID != origin {
				t.Fatalf("the original acceptance receipt no longer replays: %+v, %v", replayed, err)
			}
			if replacementID != "" {
				db, err := sql.Open("sqlite", filepath.Join(localDir, "oie.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}()
				var guessed bool
				if err := db.QueryRowContext(ctx, `SELECT EXISTS (
SELECT 1 FROM restored_own_echoes WHERE account_id=? AND conversation_id=? AND message_id=?
)`, account.ID, chat.ID, replacementID).Scan(&guessed); err != nil || guessed {
					t.Fatalf("the ordinary stanza ID became additional transmission proof: %v, %v", guessed, err)
				}
			}
		})
	}
}
