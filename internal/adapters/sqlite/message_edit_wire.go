package sqlite

import (
	"context"
	"database/sql/driver"
	"encoding/json"

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
