package sqlite

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"slices"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
	sqlitedriver "modernc.org/sqlite"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_same_direct_echo", 4, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		var a, b domain.Message
		aPayload, _ := args[0].(string)
		bPayload, _ := args[2].(string)
		if err := json.Unmarshal([]byte(aPayload), &a); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(bPayload), &b); err != nil {
			return nil, err
		}
		if !sameObservedDirectWire(a, b) {
			return int64(0), nil
		}
		aReply, _ := args[1].(string)
		bReply, _ := args[3].(string)
		ar, err := decodeReplyReference(aReply)
		if err != nil {
			return nil, err
		}
		br, err := decodeReplyReference(bReply)
		if err != nil {
			return nil, err
		}
		return number(sameObservedDirectReply(a, ar, b, br)), nil
	})
}

// Live delivery has no provider key for a stanza without an ID, while MAM
// assigns its archive key. The authenticated sender reference is shared.
func findDirectEchoWithoutStanzaID(ctx context.Context, q *dbgen.Queries, c domain.Conversation, incoming domain.Message) (string, error) {
	if c.Kind != "direct" || !incoming.IsOwn || incoming.OwnOriginID == "" || !withoutStanzaID(incoming) ||
		incoming.WireMentions == "" || (incoming.Correction != nil && incoming.Correction.TargetID != "") {
		return "", nil
	}
	payload, err := q.GetAccount(ctx, string(incoming.AccountID))
	if err != nil {
		return "", err
	}
	var account domain.Account
	if err := json.Unmarshal([]byte(payload), &account); err != nil {
		return "", err
	}
	ref := domain.MessageReference{Scope: "sender:" + account.Address, ID: string(incoming.OwnOriginID), Author: account.Address}
	if !slices.Contains(incoming.References, ref) {
		return "", nil
	}
	rows, err := q.FindReplyTargets(ctx, dbgen.FindReplyTargetsParams{
		AccountID: string(incoming.AccountID), ConversationID: string(c.ID), RemoteID: ref.ID, Scope: ref.Scope, Author: ref.Author,
	})
	if err != nil || len(rows) > 128 {
		return "", err
	}
	hash := incoming.SourceBodyHash()
	var match string
	for _, row := range rows {
		if row.SourceHash != hash {
			continue
		}
		var previous domain.Message
		if err := json.Unmarshal([]byte(row.Payload), &previous); err != nil {
			return "", err
		}
		if !sameObservedDirectWire(previous, incoming) {
			continue
		}
		canonical, err := resolveMessageAlias(ctx, q, domain.ID(row.ID))
		if err != nil {
			return "", err
		}
		if canonical != domain.ID(row.ID) {
			continue
		}
		reply, err := readMessageWireReply(ctx, q, row.ID, hash)
		if err != nil {
			return "", err
		}
		if !sameObservedDirectReply(previous, reply, incoming, incoming.ReplyReference) {
			continue
		}
		if match != "" && match != row.ID {
			return "", nil
		}
		match = row.ID
	}
	return match, nil
}

func withoutStanzaID(m domain.Message) bool {
	return m.ProviderID == "" || (m.ArchiveID != "" && m.ProviderID == "archive:"+bodyHash(m.ArchiveScope+"\x00"+m.ArchiveID))
}

func sameObservedDirectWire(a, b domain.Message) bool {
	return a.IsOwn && b.IsOwn && a.SendState == "submitted" && b.SendState == "submitted" &&
		a.OwnOriginID != "" && a.OwnOriginID == b.OwnOriginID && withoutStanzaID(a) && withoutStanzaID(b) &&
		a.SourceBodyHash() == b.SourceBodyHash() && a.WireMentions != "" && a.WireMentions == b.WireMentions &&
		a.MatchesWireMentions(domain.WireMentionProof{Evidence: b.WireMentions, Digest: b.WireMentionDigest}) &&
		slices.Equal(a.FileReferences, b.FileReferences) && slices.Equal(a.AudioURLs, b.AudioURLs) &&
		slices.Equal(a.FileDigests, b.FileDigests) && slices.Equal(a.MediaThumbnails, b.MediaThumbnails)
}

func sameObservedDirectReply(a domain.Message, ar *domain.ReplyReference, b domain.Message, br *domain.ReplyReference) bool {
	if ar == nil || br == nil {
		return ar == nil && br == nil && a.OriginalText == b.OriginalText
	}
	if ar.Target != br.Target {
		return false
	}
	if a.OriginalText == b.OriginalText && slices.Equal(ar.Fallback, br.Fallback) {
		return true
	}
	// Retention drops a reply's quote spans but keeps the raw wire hash. Verify
	// the original spans still produce exactly the retained, redacted answer.
	return sameRedactedDirectReply(a, ar, b, br) || sameRedactedDirectReply(b, br, a, ar)
}

func sameRedactedDirectReply(redacted domain.Message, retained *domain.ReplyReference, raw domain.Message, observed *domain.ReplyReference) bool {
	return redacted.WireBodySHA256 != "" && len(retained.Fallback) == 0 && redacted.OutboundReply == nil &&
		stripInboundQuote(&raw, *observed) && raw.WireBodySHA256 == redacted.WireBodySHA256 &&
		raw.OriginalText == redacted.OriginalText
}
