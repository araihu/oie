package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

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
		if !previous.IsOwn || previous.SendState != "submitted" || previous.OwnOriginID != incoming.OwnOriginID ||
			!withoutStanzaID(previous) || previous.SourceBodyHash() != hash ||
			previous.WireMentions != incoming.WireMentions ||
			!previous.MatchesWireMentions(domain.WireMentionProof{Evidence: incoming.WireMentions, Digest: incoming.WireMentionDigest}) ||
			!slices.Equal(previous.FileReferences, incoming.FileReferences) || !slices.Equal(previous.AudioURLs, incoming.AudioURLs) ||
			!slices.Equal(previous.FileDigests, incoming.FileDigests) || !slices.Equal(previous.MediaThumbnails, incoming.MediaThumbnails) {
			continue
		}
		matches, err := sameObservedDirectReply(ctx, q, row.ID, hash, incoming.ReplyReference)
		if err != nil {
			return "", err
		}
		if !matches {
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

func sameObservedDirectReply(ctx context.Context, q *dbgen.Queries, id, hash string, incoming *domain.ReplyReference) (bool, error) {
	payload, err := q.GetMessageReply(ctx, dbgen.GetMessageReplyParams{MessageID: id, BodyHash: hash})
	if errors.Is(err, sql.ErrNoRows) {
		return incoming == nil, nil
	}
	if err != nil || incoming == nil {
		return false, err
	}
	var previous domain.ReplyReference
	if err := json.Unmarshal([]byte(payload), &previous); err != nil {
		return false, err
	}
	return previous.Target == incoming.Target && slices.Equal(previous.Fallback, incoming.Fallback), nil
}
