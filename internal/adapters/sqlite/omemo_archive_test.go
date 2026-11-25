package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type protectedRoomEchoProtocol struct {
	outboundProtocol
	digests []string
}

func (*protectedRoomEchoProtocol) RoomEncryption(domain.Account, string) (bool, error) {
	return true, nil
}

func (*protectedRoomEchoProtocol) InspectRoomOMEMO(context.Context, domain.Account, string) (application.RoomOMEMOState, error) {
	return application.RoomOMEMOState{Enabled: true}, nil
}

func (*protectedRoomEchoProtocol) SetRoomOMEMO(context.Context, domain.Account, string, bool) error {
	return nil
}

func (*protectedRoomEchoProtocol) AttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error {
	return nil
}

func (*protectedRoomEchoProtocol) RequiresOriginalWireAttempt(domain.Account) bool { return true }

func (p *protectedRoomEchoProtocol) Send(ctx context.Context, _ domain.Account, out application.Outgoing) string {
	if out.RecordOMEMOWireAttempt == nil || out.RecordOMEMOWireAttempt(ctx, p.digests) != nil {
		return application.Failed
	}
	return application.Submitted
}

func TestOMEMORoomWireProofBindsExactPartThenAppliesModeration(t *testing.T) {
	ctx := t.Context()
	store, engine, direct := outboxFixture(t)
	chat := outboundRoom(t, store, direct.AccountID, "private@conference.localhost")
	digests := []string{"omemo:" + strings.Repeat("a", 64), "omemo:" + strings.Repeat("b", 64)}
	engine.SetProtocols(&protectedRoomEchoProtocol{outboundProtocol: outboundProtocol{autoJoined: true, connected: true}, digests: digests})
	if _, err := engine.JoinRoom(ctx, migrationID(), chat.ID, "Tester", ""); err != nil {
		t.Fatal(err)
	}
	defer startOutboundWorker(t, engine)()
	file := migrationID()
	if _, _, err := engine.StageAttachment(ctx, chat.ID, file, "photo.png", "image/png", strings.NewReader("fixture file")); err != nil {
		t.Fatal(err)
	}
	sent, err := engine.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{OriginalText: "encrypted caption", AttachmentIDs: []domain.ID{file}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	awaitOutbound(t, func() bool {
		row, err := store.Message(ctx, sent.Message.ID)
		if err == nil && row.SendState == application.Failed {
			t.Fatal("wire proof was not accepted")
		}
		return err == nil && row.SendState == application.Submitted
	})

	echo := application.RoomOMEMOEcho{
		AccountID: chat.AccountID, MessageID: sent.Message.ID, Room: chat.Address, Author: chat.Address + "/Tester",
		Part: 1, EnvelopeID: digests[0], StanzaID: "part-stanza-id", ArchiveID: "part-archive-id",
	}
	if changed, err := store.BindRoomOMEMOEcho(ctx, echo); err != nil || changed {
		t.Fatalf("accepted a digest from a different wire part: %v %v", changed, err)
	}
	if err := store.RecordRoomModeration(ctx, chat.AccountID, chat.Address, application.IncomingRoomModeration{
		StanzaID: echo.StanzaID, MessageModeration: domain.MessageModeration{At: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	echo.EnvelopeID = digests[1]
	if changed, err := store.BindRoomOMEMOEcho(ctx, echo); err != nil || !changed {
		t.Fatalf("failed to bind the original wire part: %v %v", changed, err)
	}
	if changed, err := store.BindRoomOMEMOEcho(ctx, echo); err != nil || changed {
		t.Fatalf("replay modified an accepted echo: %v %v", changed, err)
	}
	stored, err := store.ArchivedRoomMessage(ctx, chat, echo.ArchiveID)
	if err != nil || stored.ID != sent.Message.ID || stored.Moderation == nil {
		t.Fatalf("room notice did not moderate the original outgoing message: %+v %v", stored, err)
	}
}

func TestOMEMORoomArchiveEnrichesRetainedLiveEnvelope(t *testing.T) {
	ctx := t.Context()
	store, engine, direct := outboxFixture(t)
	chat := outboundRoom(t, store, direct.AccountID, "private@conference.localhost")
	engine.SetProtocols(&outboundProtocol{})
	live := application.Incoming{
		AccountID: chat.AccountID, Address: chat.Address, Kind: "room", SenderAddress: "bob@localhost", SenderName: "Bob",
		ProviderID: "omemo:" + strings.Repeat("c", 64), CreatedAt: time.Now().UTC(), OriginalText: "encrypted text", Encrypted: true,
	}
	if err := engine.Receive(ctx, live); err != nil {
		t.Fatal(err)
	}
	items, err := store.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("live message did not persist: %d %v", len(items), err)
	}
	archived := live
	archived.Historical, archived.ArchiveScope, archived.ArchiveID = true, chat.Address, "archive-id"
	archived.OriginalText = ""
	archived.References = []domain.MessageReference{{Scope: "room:" + chat.Address, ID: "room-stanza-id", Author: chat.Address + "/Bob"}}
	if err := store.RecordRoomModeration(ctx, chat.AccountID, chat.Address, application.IncomingRoomModeration{
		StanzaID: archived.References[0].ID, MessageModeration: domain.MessageModeration{At: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	wrong := archived
	wrong.SenderAddress = "eve@localhost"
	if changed, err := store.BindRetainedRoomOMEMOArchive(ctx, wrong); err != nil || changed {
		t.Fatalf("bound another sender's archive metadata: %v %v", changed, err)
	}
	if changed, err := store.BindRetainedRoomOMEMOArchive(ctx, archived); err != nil || !changed {
		t.Fatalf("failed to enrich the retained body: %v %v", changed, err)
	}
	got, err := store.ArchivedRoomMessage(ctx, chat, archived.ArchiveID)
	if err != nil || got.ID != items[0].ID || got.Moderation == nil {
		t.Fatalf("archive duplicate did not expose authenticated withdrawal: %+v %v", got, err)
	}
}
