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

func TestOwnDirectEchoPreservesStructuredCompositionAndNativeSend(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		structured, legacy    bool
		witnessFirst, restore bool
		sharedReference, live bool
	}{
		{name: "plain archive", restore: true, sharedReference: true},
		{name: "structured archive sharing the original reference", structured: true, restore: true, sharedReference: true},
		{name: "structured archive with its own reference", structured: true, restore: true},
		{name: "legacy structured archive", structured: true, legacy: true, restore: true},
		{name: "legacy witness first", structured: true, legacy: true, witnessFirst: true, restore: true},
		{name: "direct archive recovery", structured: true},
		{name: "live structured direct self-message", structured: true, live: true},
		{name: "live plain direct self-message", sharedReference: true, live: true},
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
			local, engine := open(localDir)
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
			if tc.restore {
				if err := local.Close(); err != nil {
					t.Fatal(err)
				}
				database, err := os.ReadFile(filepath.Join(localDir, "oie.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(witnessDir, "oie.sqlite"), database, 0600); err != nil {
					t.Fatal(err)
				}
				local, engine = open(localDir)
			}
			const body = "Juliet hello"
			composition := domain.OutgoingComposition{OriginalText: body}
			key := mint()
			accepted, err := engine.SendComposition(ctx, key, chat.ID, composition, nil)
			if err != nil {
				t.Fatal(err)
			}
			origin := accepted.Message.ID
			if tc.witnessFirst {
				at := time.Now().UTC()
				if _, err := local.TransitionMessage(ctx, origin, "queued", "dispatching", mint(), at, at); err != nil {
					t.Fatal(err)
				}
				if _, err := local.TransitionMessage(ctx, origin, "dispatching", "submitted", mint(), at, at); err != nil {
					t.Fatal(err)
				}
			}
			var mention string
			if tc.structured {
				mention = `<reference xmlns="urn:xmpp:reference:0" type="mention" uri="xmpp:juliet@localhost" begin="0" end="6"/>`
			}
			reference := string(origin)
			if !tc.sharedReference {
				reference = "independent-witness"
			}
			packet := fmt.Sprintf(`<message xmlns="jabber:client" from="%s/other-client" to="%s" id="%s" type="chat"><body>%s</body><origin-id xmlns="urn:xmpp:sid:0" id="%s"/>%s</message>`,
				account.Address, chat.Address, origin, body, reference, mention)
			raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="direct-witness"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/>%s</forwarded></result>`,
				time.Now().UTC().Format(time.RFC3339Nano), packet)
			var archived archiveResult
			if err := xml.Unmarshal([]byte(raw), &archived); err != nil {
				t.Fatal(err)
			}
			incoming, ok, err := archivedIncoming(account, account.Address, &archived, archiveTrust{})
			if err != nil || !ok || !incoming.IsOwn || len(incoming.References) != 1 || (incoming.WireMentionDigest != "") != tc.structured {
				t.Fatalf("fixture did not decode an authenticated direct echo: %+v, %v, %v", incoming, ok, err)
			}
			wantDestination := localDir
			var archivedEcho domain.Message
			if tc.restore {
				witness, observer := open(witnessDir)
				if err := observer.Receive(ctx, incoming); err != nil {
					t.Fatal(err)
				}
				witnessHistory, err := witness.Messages(ctx, chat.ID, "", 10)
				if err != nil || len(witnessHistory) != 1 {
					t.Fatalf("expected an independent observation: %+v, %v", witnessHistory, err)
				}
				archivedEcho = witnessHistory[0]
				if err := witness.Close(); err != nil {
					t.Fatal(err)
				}
				if tc.legacy {
					setLegacyDirectEchoProvider(t, witnessDir, witnessHistory[0].ID, origin)
				}
				if err := local.Close(); err != nil {
					t.Fatal(err)
				}
				source := witnessDir
				if tc.witnessFirst {
					wantDestination, source = witnessDir, localDir
				}
				archive := writeRoomEchoBackup(t, source)
				if _, err := sqlite.RestoreProfileBackup(ctx, wantDestination, archive, mint()); err != nil {
					t.Fatal(err)
				}
				if got, err := sqlite.RestoreProfileBackup(ctx, wantDestination, archive, mint()); err != nil || got != (sqlite.RestoreSummary{}) {
					t.Fatalf("repeated restore changed the result: %+v, %v", got, err)
				}
				local, engine = open(wantDestination)
			}
			for range 2 {
				if tc.live {
					manager := New(wantDestination, nil)
					manager.host = engine
					entry := &entry{account: account}
					decoder := xml.NewDecoder(strings.NewReader(packet))
					token, err := decoder.Token()
					if err != nil {
						t.Fatal(err)
					}
					start := token.(xml.StartElement)
					if err := manager.handle(ctx, entry, tokens{decoder, xml.NewEncoder(&bytes.Buffer{})}, &start); err != nil {
						t.Fatal(err)
					}
				} else if err := engine.Receive(ctx, incoming); err != nil {
					t.Fatal(err)
				}
			}
			history, err := local.Messages(ctx, chat.ID, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			wantRows, wantState, wantRefs := 1, "submitted", 1
			if tc.structured {
				wantRows, wantState = 2, "unknown"
				if tc.sharedReference {
					wantRefs = 0
				}
			}
			if tc.witnessFirst {
				wantState = "submitted"
			}
			native, err := local.Message(ctx, origin)
			if err != nil || native.ProviderID != string(origin) || native.SendState != wantState || native.OriginalText != body || len(native.References) != wantRefs {
				t.Fatalf("native send changed composition or acquired another packet's reference: %+v, %v", native, err)
			}
			if wantRefs == 1 && native.References[0].ID != string(origin) {
				t.Fatalf("native send lost its original scoped reference: %+v", native.References)
			}
			if len(history) != wantRows {
				t.Fatalf("expected %d visible messages, got %+v", wantRows, history)
			}
			for _, echo := range history {
				if echo.ID == origin {
					continue
				}
				echo, err := local.Message(ctx, echo.ID)
				if err != nil || echo.OwnOriginID != origin || echo.WireMentionDigest != incoming.WireMentionDigest || echo.ProviderID == string(origin) ||
					len(echo.Mentions) != 0 || (tc.restore && (echo.ID != archivedEcho.ID || echo.RevisionID != archivedEcho.RevisionID)) {
					t.Fatalf("lost the distinct observation or its revision: %+v, %v", echo, err)
				}
				if tc.sharedReference {
					if len(echo.References) != 0 {
						t.Fatalf("ambiguous references unexpectedly resolve to the witness: %+v", echo.References)
					}
				} else if len(echo.References) != 1 || echo.References[0] != incoming.References[0] {
					t.Fatalf("witness lost its separate reference: %+v", echo.References)
				}
			}
			if reopened, err := engine.SendComposition(ctx, key, chat.ID, composition, nil); err != nil || !reopened.Replayed || reopened.Message.ID != origin {
				t.Fatalf("original acceptance was lost: %+v, %v", reopened, err)
			}
			if due, err := local.PendingMessages(ctx, time.Now().Add(time.Hour)); err != nil || len(due) != 0 {
				t.Fatalf("received evidence left an executable copy: %+v, %v", due, err)
			}
		})
	}
}

// Older profiles could store a direct observation under the original wire key.
func setLegacyDirectEchoProvider(t *testing.T, dir string, id, origin domain.ID) {
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
	if _, err := db.ExecContext(t.Context(), `UPDATE messages SET provider_id=?,payload=json_set(payload,'$.ProviderID',?) WHERE id=?`, origin, origin, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE message_revisions SET payload=json_set(payload,'$.ProviderID',?) WHERE entity_id=?`, origin, id); err != nil {
		t.Fatal(err)
	}
}
