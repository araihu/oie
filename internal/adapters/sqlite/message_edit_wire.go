package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
	sqlitedriver "modernc.org/sqlite"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_edit_wire_matches", 3, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		var edit domain.PendingMessageEdit
		var message domain.Message
		editPayload, _ := args[0].(string)
		messagePayload, _ := args[1].(string)
		if err := json.Unmarshal([]byte(editPayload), &edit); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(messagePayload), &message); err != nil {
			return nil, err
		}
		replyPayload, _ := args[2].(string)
		reply, err := decodeReplyReference(replyPayload)
		if err != nil {
			return nil, err
		}
		return number(matchesEditWire(edit, message, reply)), nil
	})
}

func readExactEditWire(ctx context.Context, q *dbgen.Queries, p domain.PendingMessageEdit, m domain.Message) (bool, error) {
	reply, err := readMessageWireReply(ctx, q, string(m.ID), m.SourceBodyHash())
	if err != nil {
		return false, err
	}
	return matchesEditWire(p, m, reply), nil
}

// An existing, locally accepted correction owns its original body and wire
// shape. An observation can add metadata only if it matches that acceptance.
func knownEditReplay(ctx context.Context, q *dbgen.Queries, retained dbgen.Message) (*domain.PendingMessageEdit, error) {
	if retained.IsOwn != 1 || retained.State != "submitted" {
		return nil, nil
	}
	previous, err := decodeMessage(retained)
	if err != nil {
		return nil, err
	}
	key := previous.ProviderID
	roomFact, err := observedRoomEditFact(ctx, q, previous)
	if err != nil {
		return nil, err
	}
	if roomFact != nil {
		key = roomFact.RemoteID
	}
	if _, err := domain.ParseID(key); err != nil {
		return nil, nil
	}
	row, err := q.GetMessageEditCommand(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	edit, err := decodeEdit(row)
	if err != nil {
		return nil, err
	}
	verified, err := verifiedEditSource(ctx, q, edit, previous)
	if err != nil || !verified {
		return nil, err
	}
	return &edit, nil
}

func matchesEditReplay(edit domain.PendingMessageEdit, incoming domain.Message) bool {
	fact := incoming.Correction
	return incoming.IsOwn && correctionValid(fact) && fact.ID == string(edit.Command.ID) && fact.TargetID == edit.Plan.OriginalWireID &&
		fact.Scope == edit.Plan.Scope && fact.Author == edit.Plan.Author && matchesEditWire(edit, incoming, incoming.ReplyReference)
}

func allowsEditReplay(ctx context.Context, q *dbgen.Queries, retained dbgen.Message, incoming domain.Message) (bool, error) {
	edit, err := knownEditReplay(ctx, q, retained)
	return edit == nil || matchesEditReplay(*edit, incoming), err
}

// Unlike direct stanzas, an archived room correction can use an occupant-
// scoped provider key while its local acceptance uses the original client ID.
func matchNativeRoomEditReplay(ctx context.Context, q *dbgen.Queries, chat domain.Conversation, incoming *domain.Message) (bool, error) {
	fact := incoming.Correction
	if chat.Kind != "room" || !incoming.IsOwn || !correctionValid(fact) || fact.TargetID == "" ||
		!strings.HasPrefix(fact.Scope, "xmpp:correct:room:"+chat.Address+":") || !strings.HasPrefix(fact.Author, chat.Address+"/") {
		return true, nil
	}
	key := fact.ID
	provisional := incoming.ProviderID == key
	// Origin matching may have selected the client ID before the room's
	// occupant authority and the retained physical source were verified.
	incoming.ProviderID = roomCorrectionProviderID(fact.Author, key)
	lookup := dbgen.FindProviderMessageParams{
		AccountID: string(chat.AccountID), ConversationID: string(chat.ID), ProviderID: key, IsOwn: 1,
	}
	retained, err := q.FindProviderMessage(ctx, lookup)
	if errors.Is(err, sql.ErrNoRows) {
		if !provisional {
			return true, nil
		}
		return true, bindUnmaterializedRoomEdit(ctx, q, incoming, lookup)
	}
	if err != nil {
		return false, err
	}
	// A different verified occupant is a separate wire authority, even if it
	// uses the same client ID. Only intercept the retained occupant's packet.
	edit, err := knownEditReplay(ctx, q, retained)
	if err != nil {
		return false, err
	}
	if edit == nil || edit.Plan.Scope != fact.Scope || edit.Plan.Author != fact.Author {
		return true, nil
	}
	if !matchesEditReplay(*edit, *incoming) {
		return false, nil
	}
	incoming.ProviderID = retained.ProviderID
	return true, nil
}

func bindUnmaterializedRoomEdit(ctx context.Context, q *dbgen.Queries, incoming *domain.Message, lookup dbgen.FindProviderMessageParams) error {
	row, err := q.GetMessageEditCommand(ctx, lookup.ProviderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	edit, err := decodeEdit(row)
	if err != nil {
		return err
	}
	if row.AccountID != lookup.AccountID || row.ConversationID != lookup.ConversationID || !matchesEditReplay(edit, *incoming) {
		return nil
	}
	// Restore can retain the archived occupant-scoped observation while
	// adding its native acceptance receipt. Keep that source and its aliases.
	lookup.ProviderID = incoming.ProviderID
	if _, err := q.FindProviderMessage(ctx, lookup); errors.Is(err, sql.ErrNoRows) {
		incoming.ProviderID = string(edit.Command.ID)
	} else if err != nil {
		return err
	}
	return nil
}

func matchesEditWire(p domain.PendingMessageEdit, m domain.Message, observed *domain.ReplyReference) bool {
	if m.SourceBodyHash() != p.WireBodyHash() || m.WireMentions == domain.WireMentionsPresent || len(m.Mentions) != 0 ||
		len(m.Attachments) != 0 || m.AttachmentWireText != "" || len(m.FileReferences) != 0 ||
		len(m.AudioURLs) != 0 || len(m.FileDigests) != 0 || len(m.MediaThumbnails) != 0 {
		return false
	}
	expected := p.ReplyWireDigest()
	if reply := m.OutboundReply; reply != nil {
		// A native correction retains its local target and content revision
		// after quote removal, even in snapshots made before the wire digest.
		if m.ID != p.Command.ID || m.RevisionID != p.ContentRevisionID || p.Reply == nil ||
			reply.TargetID != p.Reply.TargetID || reply.TargetRevisionID != p.Reply.TargetRevisionID ||
			reply.TargetBodyHash != p.Reply.TargetBodyHash || reply.BodyText != p.Reply.BodyText {
			return false
		}
		if reply.Reference == nil {
			return m.WireBodySHA256 != "" && m.OriginalText == reply.BodyText
		}
		return (domain.ReplyReference{Target: *reply.Reference, Fallback: reply.Fallback}).WireDigest() == expected
	}
	if expected == "" {
		return p.Reply == nil && observed == nil && m.OriginalText == p.WireBody()
	}
	if observed == nil {
		return false
	}
	if observed.WireDigest() == expected {
		return true
	}
	// An older inbound redaction retained the exact target and wire hash.
	// Apply the frozen spans to the original wire body to verify its answer.
	if observed.WireSHA256 != "" || len(observed.Fallback) != 0 || m.WireBodySHA256 == "" ||
		p.Reply == nil || p.Reply.Reference == nil || observed.Target != *p.Reply.Reference {
		return false
	}
	body, _ := domain.ReplyBody(p.WireBody(), p.Reply.Fallback)
	return body != p.WireBody() && body == m.OriginalText
}
