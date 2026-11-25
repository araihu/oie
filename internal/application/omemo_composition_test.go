package application

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type omemoCompositionRepository struct {
	roomWakeRepo
	MessageEditRepository
	MessageReplayRepository
	SchedulingRepository
	source domain.Message
	staged domain.StagedAttachment
	body   string
	result *domain.Message
}

func (r *omemoCompositionRepository) Message(context.Context, domain.ID) (domain.Message, error) {
	return r.source, nil
}

func (r *omemoCompositionRepository) ReadMessageEditTarget(context.Context, domain.ID, bool) (MessageEditCandidate, error) {
	return MessageEditCandidate{Target: domain.MessageEditTarget{Eligible: true, ConversationID: r.conversation.ID}, Source: r.source}, nil
}

func (r *omemoCompositionRepository) ReplayAcceptedMessage(context.Context, domain.ID, string) (*domain.Message, error) {
	return r.result, nil
}

func (r *omemoCompositionRepository) StageAttachment(_ context.Context, _ domain.ID, file domain.StagedAttachment, body io.Reader) (domain.StagedAttachment, bool, error) {
	bytes, err := io.ReadAll(body)
	file.Size = int64(len(bytes))
	r.staged, r.body = file, string(bytes)
	return file, false, err
}

func (r *omemoCompositionRepository) StagedAttachment(_ context.Context, _ domain.ID, id domain.ID) (domain.StagedAttachment, error) {
	if r.staged.ID != id {
		return domain.StagedAttachment{}, ErrNotFound
	}
	return r.staged, nil
}

func (r *omemoCompositionRepository) AcceptMessage(_ context.Context, in MessageMutation) (MessageResult, error) {
	r.result = &in.Message
	return MessageResult{Message: in.Message}, nil
}

type omemoCompositionProtocol struct {
	Protocols
	encrypted                                       bool
	edits, reactions, opened, protectedAvailability int
}

func (p *omemoCompositionProtocol) RoomEncryption(domain.Account, string) (bool, error) {
	return p.encrypted, nil
}

func (p *omemoCompositionProtocol) InspectRoomOMEMO(context.Context, domain.Account, string) (RoomOMEMOState, error) {
	return RoomOMEMOState{}, nil
}

func (p *omemoCompositionProtocol) SetRoomOMEMO(context.Context, domain.Account, string, bool) error {
	return nil
}

func (p *omemoCompositionProtocol) RoomSession(context.Context, domain.Account, domain.Conversation) (domain.RoomSession, error) {
	return domain.RoomSession{State: "joined", Active: true}, nil
}

func (p *omemoCompositionProtocol) JoinRoom(context.Context, domain.Account, domain.Conversation, string, domain.ID) (domain.RoomSession, error) {
	return domain.RoomSession{}, nil
}

func (p *omemoCompositionProtocol) LeaveRoom(context.Context, domain.Account, domain.Conversation, domain.ID) (domain.RoomSession, error) {
	return domain.RoomSession{}, nil
}

func (p *omemoCompositionProtocol) SelectReplyReference(domain.Account, domain.Conversation, domain.Message) (domain.MessageReference, bool) {
	return domain.MessageReference{Scope: "room", ID: "native-id"}, true
}

func (p *omemoCompositionProtocol) PrepareReaction(context.Context, domain.Account, domain.Conversation, domain.Message) (domain.OutgoingReaction, error) {
	p.reactions++
	return domain.OutgoingReaction{}, ErrProtocolUnavailable
}

func (p *omemoCompositionProtocol) PrepareCorrection(context.Context, domain.Account, domain.Conversation, domain.Message) (domain.OutgoingCorrection, error) {
	p.edits++
	return domain.OutgoingCorrection{}, ErrProtocolUnavailable
}

func (p *omemoCompositionProtocol) OpenOMEMOAttachment(context.Context, domain.EncryptedAttachment) (MediaRead, error) {
	p.opened++
	return MediaRead{FileName: "image.png", MediaType: "application/octet-stream", Body: io.NopCloser(strings.NewReader("verified file bytes"))}, nil
}

func (p *omemoCompositionProtocol) AttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error {
	return nil
}

func (p *omemoCompositionProtocol) OMEMOAttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error {
	p.protectedAvailability++
	return nil
}

func omemoCompositionFixture(t *testing.T) (*Engine, *omemoCompositionRepository, *omemoCompositionProtocol) {
	t.Helper()
	a := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Protocol: "xmpp", Address: "alice@example.org"}
	c := domain.Conversation{ID: "019a5df0-0000-7000-8000-000000000002", AccountID: a.ID, Kind: "room", Address: "private@rooms.example.org"}
	revision := domain.ID("019a5df0-0000-7000-8000-000000000003")
	r := &omemoCompositionRepository{
		roomWakeRepo: roomWakeRepo{account: a, conversation: c, intent: domain.RoomSession{Active: true, IntentRevisionID: revision}},
		source:       domain.Message{ID: "019a5df0-0000-7000-8000-000000000004", RevisionID: revision, AccountID: a.ID, ConversationID: c.ID, Sender: domain.Sender{DisplayName: "Bob"}, OriginalText: "original"},
	}
	p := &omemoCompositionProtocol{encrypted: true}
	engine, err := New(r, "local", func() (domain.ID, error) { return "019a5df0-0000-7000-8000-000000000005", nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetProtocols(p)
	return engine, r, p
}

func TestOMEMOCompositionKeepsReplyQuoteAndMentionText(t *testing.T) {
	e, r, _ := omemoCompositionFixture(t)
	in := domain.OutgoingComposition{OriginalText: "@Bob thanks", ReplyTo: r.source.ID,
		Mentions: []domain.MentionSelection{{ParticipantID: "Bob", Begin: 0, End: 4}}}
	m, err := e.prepareComposition(t.Context(), r.account, r.conversation, domain.Message{DirectEncryption: "omemo"}, in, ReplyReview{})
	if err != nil || m.Reply == nil || m.OutboundReply.Reference != nil {
		t.Fatalf("protected reply was not saved as a quote: %+v %v", m, err)
	}
	// A queued message from an older version can still have frozen metadata.
	m.OutboundReply.Reference = &domain.MessageReference{Scope: "room", ID: "native-id"}
	m.Mentions = []domain.MessageMention{{MentionSelection: in.Mentions[0], ReferenceMode: "structured"}}
	out := outgoingMessage(m, r.conversation)
	if out.OriginalText != "> Bob:\n> original\n\n@Bob thanks" || out.Reply != nil || len(out.Mentions) != 0 || !out.MatchesOriginalContent(m) {
		t.Fatalf("protected wire composition lost text or retained clear metadata: %+v", out)
	}
	in.Mentions = nil
	plain, err := e.prepareComposition(t.Context(), r.account, r.conversation, domain.Message{}, in, ReplyReview{})
	if err != nil || outgoingMessage(plain, r.conversation).Reply == nil {
		t.Fatalf("plain reply changed: %+v %v", plain, err)
	}
}

func TestOMEMOEditAndReactionEligibility(t *testing.T) {
	e, r, p := omemoCompositionFixture(t)
	for _, active := range []bool{true, false} {
		p.encrypted = active
		if !active {
			r.source.DirectEncryption = "omemo"
		}
		edited, err := e.MessageEditTarget(t.Context(), r.source.ID)
		if err != nil || edited.Eligible || p.edits != 0 {
			t.Fatalf("edit became eligible: %+v %v", edited, err)
		}
		scope := func() (reactionScope, error) { return e.loadReactionScope(t.Context(), r.conversation.ID) }
		src := MessageReactionCandidate{Source: r.source}
		reacted, _, err := e.reactionEligibility(t.Context(), src, scope)
		if err != nil || reacted.Snapshot.Eligible || p.reactions != 0 {
			t.Fatalf("reaction became eligible: %+v %v", reacted, err)
		}
	}
}

func TestForwardAttachmentOnlyOMEMOMessage(t *testing.T) {
	e, r, p := omemoCompositionFixture(t)
	r.source.OriginalText, r.source.DirectEncryption = "", "omemo"
	r.source.EncryptedAttachments = []domain.EncryptedAttachment{{FileName: "image.png"}}
	const key = domain.ID("019a5df0-0000-7000-8000-000000000006")
	first, err := e.ForwardMessage(t.Context(), key, r.source.ID, r.source.RevisionID, r.conversation.ID)
	if err != nil || len(first.Message.Attachments) != 1 || first.Message.DirectEncryption != "omemo" ||
		r.body != "verified file bytes" || r.staged.FileName != "image.png" {
		t.Fatalf("protected file was not staged for forwarding: %+v %q %v", first, r.body, err)
	}
	second, err := e.ForwardMessage(t.Context(), key, r.source.ID, r.source.RevisionID, r.conversation.ID)
	if err != nil || !second.Replayed || p.opened != 1 || p.protectedAvailability != 1 {
		t.Fatalf("forward replay fetched the file again: %+v %v", second, err)
	}
}
