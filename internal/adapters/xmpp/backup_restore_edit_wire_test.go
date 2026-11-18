package xmpp

import (
	"context"
	"encoding/xml"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type archiveEditFixtureProtocol struct{ *Manager }

func (p archiveEditFixtureProtocol) PrepareCorrection(_ context.Context, account domain.Account, _ domain.Conversation, message domain.Message) (domain.OutgoingCorrection, error) {
	return domain.OutgoingCorrection{
		TargetMessageID: message.ID, OriginalWireID: message.ProviderID, Scope: "xmpp:correct:sender:" + account.Address,
		Author: account.Address, SessionID: "fixture-session",
	}, nil
}

func TestBackupRestoreMatchesExactCorrectionWireFromMAM(t *testing.T) {
	for _, tc := range []struct {
		name, difference  string
		reply, scrubLocal bool
		scrubObserved     bool
	}{
		{name: "plain"},
		{name: "different-mention", difference: "mention"},
		{name: "unexpected-reply", difference: "reply"},
		{name: "different-file", difference: "file"},
		{name: "exact-reply", reply: true},
		{name: "different-reply-target", reply: true, difference: "reply"},
		{name: "different-reply-fallback", reply: true, difference: "fallback"},
		{name: "redacted-local-reply", reply: true, scrubLocal: true},
		{name: "redacted-observed-reply", reply: true, scrubObserved: true},
		{name: "redacted-reply-both-sides", reply: true, scrubLocal: true, scrubObserved: true},
		{name: "redacted-local-different-reply", reply: true, scrubLocal: true, difference: "reply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			now := time.Now().UTC()
			localDir, observedDir := t.TempDir(), t.TempDir()
			local, editor, manager := openDirectEchoProfile(t, localDir)
			editor.SetProtocols(archiveEditFixtureProtocol{manager})
			account, chat := setupDirectEchoProfile(t, editor)

			var quoted domain.ID
			const quotedWireID = "quoted-target"
			if tc.reply {
				ref := domain.MessageReference{Scope: "sender:" + account.Address, ID: quotedWireID, Author: account.Address}
				if err := editor.Receive(ctx, application.Incoming{
					AccountID: account.ID, Kind: "direct", Address: chat.Address, IsOwn: true,
					ProviderID: quotedWireID, OriginalText: "an earlier quoted original", CreatedAt: now.Add(-48 * time.Hour),
					References: []domain.MessageReference{ref}, Historical: true,
				}); err != nil {
					t.Fatal(err)
				}
				messages, err := local.Messages(ctx, chat.ID, "", 10)
				if err != nil || len(messages) != 1 {
					t.Fatalf("quoted fixture: %d rows, %v", len(messages), err)
				}
				quoted = messages[0].ID
			}
			root, err := editor.SendMessageWithReply(ctx, mintDirectEchoID(t), chat.ID, "own original", quoted)
			if err != nil {
				t.Fatal(err)
			}
			for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
				if _, err := local.TransitionMessage(ctx, root.Message.ID, transition[0], transition[1], mintDirectEchoID(t), now, now); err != nil {
					t.Fatal(err)
				}
			}
			if err := local.Close(); err != nil {
				t.Fatal(err)
			}
			seed := writeRoomEchoBackup(t, localDir)
			if _, err := sqlite.RestoreProfileBackup(ctx, observedDir, seed, mintDirectEchoID(t)); err != nil {
				t.Fatal(err)
			}
			local, editor, manager = openDirectEchoProfile(t, localDir)
			editor.SetProtocols(archiveEditFixtureProtocol{manager})
			observed, receiver, _ := openDirectEchoProfile(t, observedDir)
			target, err := editor.MessageEditTarget(ctx, root.Message.ID)
			if err != nil || !target.Eligible {
				t.Fatalf("edit target: %+v, %v", target, err)
			}
			key := mintDirectEchoID(t)
			const corrected = "the corrected text"
			accepted, err := editor.EditMessage(ctx, key, root.Message.ID, target.CurrentRevisionID, corrected)
			if err != nil || accepted.Command.State != "queued" {
				t.Fatalf("accept edit: %+v, %v", accepted, err)
			}
			if _, err := local.ClaimMessageEdit(ctx, key, mintDirectEchoID(t), now); err != nil {
				t.Fatal(err)
			}
			unknown, err := local.FinishMessageEdit(ctx, key, "unknown", mintDirectEchoID(t), now, now, "unknown_outcome")
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := local.MessageEdit(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			var metadata string
			if tc.reply || tc.difference == "reply" {
				referenceID := quotedWireID
				if tc.difference == "reply" {
					referenceID = "unrelated-target"
				}
				metadata = fmt.Sprintf(`<reply xmlns="urn:xmpp:reply:0" id="%s" to="%s"/>`, referenceID, account.Address)
				if tc.reply {
					if frozen.Reply == nil || frozen.Reply.Reference == nil || len(frozen.Reply.Fallback) != 1 {
						t.Fatalf("missing frozen reply: %+v", frozen.Reply)
					}
					span := frozen.Reply.Fallback[0]
					if tc.difference == "fallback" {
						span.End--
					}
					metadata += fmt.Sprintf(`<fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0"><body start="%d" end="%d"/></fallback>`, span.Start, span.End)
				}
			}
			switch tc.difference {
			case "mention":
				metadata = `<reference xmlns="urn:xmpp:reference:0" type="mention" uri="xmpp:jules@localhost" begin="0" end="3"/>`
			case "file":
				metadata = `<file-sharing xmlns="urn:xmpp:sfs:0"><file xmlns="urn:xmpp:file:metadata:0"><media-type>video/mp4</media-type></file><sources><url-data xmlns="http://jabber.org/protocol/url-data" target="https://files.example/video.mp4"/></sources></file-sharing>`
			}
			raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="independent-correction"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s/second-device" to="%s" type="chat" id="%s"><body>%s</body><replace xmlns="urn:xmpp:message-correct:0" id="%s"/>%s</message></forwarded></result>`,
				now.Format(time.RFC3339Nano), account.Address, chat.Address, key, escaped(frozen.WireBody()), root.Message.ID, metadata)
			var result archiveResult
			if err := xml.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			incoming, ok, err := archivedIncoming(account, account.Address, &result, archiveTrust{})
			if err != nil || !ok || !incoming.IsOwn || incoming.Correction == nil || incoming.Correction.TargetID != string(root.Message.ID) ||
				(tc.difference == "mention" && incoming.WireMentions != domain.WireMentionsPresent) ||
				(tc.difference == "file" && len(incoming.FileReferences) != 1) ||
				((tc.reply || tc.difference == "reply") && incoming.Reply == nil) {
				t.Fatalf("bad archived correction fixture: %+v, %v, %v", incoming, ok, err)
			}
			if err := receiver.Receive(ctx, incoming); err != nil {
				t.Fatal(err)
			}
			if tc.scrubLocal {
				scrubArchivedEditFixtureQuote(t, local, editor, chat, now)
			}
			if tc.scrubObserved {
				scrubArchivedEditFixtureQuote(t, observed, receiver, chat, now)
			}
			for _, store := range []*sqlite.Store{local, observed} {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			backup := writeRoomEchoBackup(t, observedDir)
			for range 2 {
				if _, err := sqlite.RestoreProfileBackup(ctx, localDir, backup, mintDirectEchoID(t)); err != nil {
					t.Fatal(err)
				}
			}
			_, restarted, _ := openDirectEchoProfile(t, localDir)
			replay, err := restarted.EditMessage(ctx, key, root.Message.ID, target.CurrentRevisionID, corrected)
			want := "submitted"
			if tc.difference != "" {
				want = "unknown"
			}
			if err != nil || !replay.Replayed || replay.Command.State != want ||
				(replay.Command.RevisionID == unknown.RevisionID) != (want == "unknown") {
				t.Fatalf("wire observation produced the wrong replay: %+v, %v. Expected %s", replay, err, want)
			}
		})
	}
}

func scrubArchivedEditFixtureQuote(t *testing.T, store *sqlite.Store, engine *application.Engine, chat domain.Conversation, now time.Time) {
	t.Helper()
	days := 1
	if _, err := engine.SetConversationRetention(t.Context(), chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PurgeExpiredHistory(t.Context(), now.Add(time.Second), directEchoNewID); err != nil || n != 1 {
		t.Fatalf("expire quoted original: %d, %v", n, err)
	}
}
