package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	sqlitedriver "modernc.org/sqlite"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("oie_source_hash", 1, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		text, _ := args[0].(string)
		return bodyHash(text), nil
	})
}

func correctionValid(c *domain.MessageCorrection) bool {
	return c != nil && c.Scope != "" && c.Author != "" && c.ID != "" && len(c.Scope) <= 4096 && len(c.Author) <= 4096 && len(c.ID) <= 4096 && len(c.TargetID) <= 4096 && utf8.ValidString(c.Scope+c.Author+c.ID+c.TargetID) && !strings.ContainsRune(c.Scope+c.Author+c.ID+c.TargetID, 0)
}
func resolveMessageAlias(ctx context.Context, q *dbgen.Queries, id domain.ID) (domain.ID, error) {
	if id == "" {
		return id, nil
	}
	canonical, err := q.ResolveMessageAlias(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return id, nil
	}
	return domain.ID(canonical), err
}
func hydrateMessageProjection(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	id, err := resolveMessageAlias(ctx, q, m.ID)
	if err != nil {
		return err
	}
	if id != m.ID {
		row, err := q.GetMessage(ctx, string(id))
		if err != nil {
			return err
		}
		*m, err = decodeMessage(row)
		if err != nil {
			return err
		}
	}
	head, err := q.GetContentHead(ctx, string(m.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var projected domain.Message
	if err = json.Unmarshal([]byte(head.Payload), &projected); err != nil {
		return err
	}
	// Delivery ownership and accepted outbox bodies remain in the immutable source.
	projected.SendState = m.SendState
	projected.IsOwn = m.IsOwn
	projected.ProviderID = m.ProviderID
	projected.ArchiveID = m.ArchiveID
	projected.ArchiveScope = m.ArchiveScope
	projected.Historical = m.Historical
	projected.ProjectionSourceID = domain.ID(head.SourceID)
	*m = projected
	return nil
}
func recordCorrectionMetadata(ctx context.Context, q *dbgen.Queries, id string, in domain.Message) error {
	if !correctionValid(in.Correction) {
		return nil
	}
	row, err := q.GetMessage(ctx, id)
	if err != nil {
		return err
	}
	raw, err := decodeMessage(row)
	if err != nil {
		return err
	}
	if raw.AccountID != in.AccountID {
		return nil
	}
	c := in.Correction
	n, err := q.InsertCorrectionFact(ctx, dbgen.InsertCorrectionFactParams{MessageID: id, Scope: c.Scope, Author: c.Author, RemoteID: c.ID, TargetID: c.TargetID, SourceHash: bodyHash(in.OriginalText), SourceRevisionID: string(raw.RevisionID)})
	if err != nil || n == 0 {
		return err
	}
	return reconcileCorrections(ctx, q, raw, c)
}

type correctionNode struct {
	message domain.Message
	fact    dbgen.MessageCorrectionFact
}

func reconcileCorrections(ctx context.Context, q *dbgen.Queries, seed domain.Message, proof *domain.MessageCorrection) error {
	ids := map[string]bool{proof.ID: true}
	if proof.TargetID != "" {
		ids[proof.TargetID] = true
	}
	nodes := map[domain.ID]correctionNode{}
	ambiguous := false
	for {
		keys := make([]string, 0, len(ids))
		for id := range ids {
			keys = append(keys, id)
		}
		slices.Sort(keys)
		rows, err := q.CorrectionNeighbors(ctx, dbgen.CorrectionNeighborsParams{AccountID: string(seed.AccountID), ConversationID: string(seed.ConversationID), Scope: proof.Scope, Author: proof.Author, RemoteIds: jsonText(keys)})
		if err != nil {
			return err
		}
		oversized := len(rows) > 128
		previous := len(ids)
		for _, row := range rows {
			var m domain.Message
			if err = json.Unmarshal([]byte(row.MessagePayload), &m); err != nil {
				return err
			}
			if bodyHash(m.OriginalText) != row.SourceHash {
				ambiguous = true
			}
			f := dbgen.MessageCorrectionFact{MessageID: row.MessageID, Scope: row.Scope, Author: row.Author, RemoteID: row.RemoteID, TargetID: row.TargetID, SourceHash: row.SourceHash, SourceRevisionID: row.SourceRevisionID}
			if old, ok := nodes[m.ID]; ok && old.fact != f {
				ambiguous = true
			}
			nodes[m.ID] = correctionNode{message: m, fact: f}
			ids[f.RemoteID] = true
			if f.TargetID != "" {
				ids[f.TargetID] = true
			}
		}
		if oversized || len(ids) > 256 || len(nodes) > 128 {
			ambiguous = true
			break
		}
		if previous == len(ids) {
			break
		}
	}
	if len(nodes) == 0 {
		return nil
	}
	byRemote := map[string]domain.ID{}
	for id, node := range nodes {
		if prior, ok := byRemote[node.fact.RemoteID]; ok && prior != id {
			ambiguous = true
		}
		byRemote[node.fact.RemoteID] = id
		facts, err := q.GetCorrectionFacts(ctx, string(id))
		if err != nil {
			return err
		}
		for _, f := range facts {
			if f.SourceHash == node.fact.SourceHash && (f.Scope != node.fact.Scope || f.Author != node.fact.Author || f.RemoteID != node.fact.RemoteID || f.TargetID != node.fact.TargetID) {
				ambiguous = true
			}
		}
	}
	roots := map[domain.ID]domain.ID{}
	parents := map[domain.ID]domain.ID{}
	for id, node := range nodes {
		if target, ok := byRemote[node.fact.TargetID]; ok {
			parents[id] = target
		}
	}
	for id := range nodes {
		seen := map[domain.ID]bool{}
		at := id
		for parents[at] != "" {
			if seen[at] {
				ambiguous = true
				break
			}
			seen[at] = true
			at = parents[at]
		}
		roots[id] = at
	}
	memberIDs := make([]domain.ID, 0, len(nodes))
	before := map[domain.ID]domain.Message{}
	for id, node := range nodes {
		memberIDs = append(memberIDs, id)
		copy := node.message
		if err := hydrateMessageProjection(ctx, q, &copy); err != nil {
			return err
		}
		before[id] = copy
		if copy.ID != id {
			memberIDs = append(memberIDs, copy.ID)
		}
	}
	slices.Sort(memberIDs)
	memberIDs = slices.Compact(memberIDs)
	if err := q.ClearCanonicalAliases(ctx, jsonText(memberIDs)); err != nil {
		return err
	}
	if err := q.ClearContentHeads(ctx, jsonText(memberIDs)); err != nil {
		return err
	}
	if !ambiguous {
		groups := map[domain.ID][]domain.Revision{}
		for id, node := range nodes {
			root := roots[id]
			parent := []domain.ID{}
			if p := parents[id]; p != "" {
				parent = append(parent, domain.ID(nodes[p].fact.SourceRevisionID))
			}
			groups[root] = append(groups[root], domain.Revision{ID: domain.ID(node.fact.SourceRevisionID), EntityID: root, Parents: parent, CreatedAt: node.message.CreatedAt})
			if id != root {
				if err := q.PutCanonicalAlias(ctx, dbgen.PutCanonicalAliasParams{AliasID: string(id), CanonicalID: string(root)}); err != nil {
					return err
				}
			}
		}
		for root, revisions := range groups {
			winner, err := domain.CurrentRevision(revisions)
			if err != nil {
				return err
			}
			var source correctionNode
			for _, node := range nodes {
				if node.fact.SourceRevisionID == string(winner.ID) {
					source = node
					break
				}
			}
			if source.message.ID == root && source.fact.TargetID == "" {
				continue
			}
			projected := source.message
			base := nodes[root].message
			projected.ID = root
			projected.RevisionID = winner.ID
			projected.AccountID = base.AccountID
			projected.ConversationID = base.ConversationID
			projected.CreatedAt = base.CreatedAt
			projected.IsOwn = base.IsOwn
			projected.SendState = base.SendState
			projected.ProviderID = base.ProviderID
			projected.Edited = true
			projected.EditedAt = source.message.CreatedAt.UTC()
			projected.Correction = nil
			projected.References = nil
			projected.Reply = nil
			projected.ReplyReference = nil
			if err = q.PutContentHead(ctx, dbgen.PutContentHeadParams{CanonicalID: string(root), SourceID: string(source.message.ID), RevisionID: string(winner.ID), Payload: jsonText(projected), EditedAt: stamp(projected.EditedAt)}); err != nil {
				return err
			}
		}
	}
	changed := false
	emitted := map[domain.ID]bool{}
	for id, node := range nodes {
		after := node.message
		if err := hydrateMessageProjection(ctx, q, &after); err != nil {
			return err
		}
		if !reflect.DeepEqual(before[id], after) {
			changed = true
			if !emitted[after.ID] {
				if _, err := emitCanonicalMessageEvent(ctx, q, "message.updated", after, time.Now().UTC()); err != nil {
					return err
				}
				emitted[after.ID] = true
			}
		}
		if prior := before[id].ID; prior != after.ID && !emitted[prior] {
			row, err := q.GetMessage(ctx, string(prior))
			if err != nil {
				return err
			}
			oldRoot, err := decodeMessage(row)
			if err != nil {
				return err
			}
			if err = hydrateMessageProjection(ctx, q, &oldRoot); err != nil {
				return err
			}
			if !emitted[oldRoot.ID] {
				if _, err = emitCanonicalMessageEvent(ctx, q, "message.updated", oldRoot, time.Now().UTC()); err != nil {
					return err
				}
				emitted[oldRoot.ID] = true
			}
		}
	}
	for _, id := range memberIDs {
		if err := q.InvalidateSharedContent(ctx, string(id)); err != nil {
			return err
		}
	}
	if changed {
		return refreshCorrectionConversation(ctx, q, seed.ConversationID)
	}
	return nil
}
func refreshCorrectionConversation(ctx context.Context, q *dbgen.Queries, id domain.ID) error {
	row, err := q.GetConversation(ctx, string(id))
	if err != nil {
		return err
	}
	c, err := decodeConversation(row)
	if err != nil {
		return err
	}
	latest, err := q.LatestLogicalMessage(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	m, err := decodeMessage(latest)
	if err != nil {
		return err
	}
	if err = hydrateMessageProjection(ctx, q, &m); err != nil {
		return err
	}
	if c.LastMessage == m.OriginalText && c.UpdatedAt.Equal(m.CreatedAt) {
		return nil
	}
	c.LastMessage = m.OriginalText
	c.UpdatedAt = m.CreatedAt
	if err = q.UpdateConversation(ctx, dbgen.UpdateConversationParams{RevisionID: string(c.RevisionID), Payload: jsonText(c), ActivitySequence: c.ActivitySequence, UnreadCount: int64(c.UnreadCount), ID: string(c.ID)}); err != nil {
		return err
	}
	_, err = change(ctx, q, "conversation.updated", c, time.Now().UTC())
	return err
}

// Source rows stay retained, while invalidations always name the visible
// canonical entity. Publishing a hidden alias would recreate a second bubble
// in API consumers that upsert SSE payloads directly.
func emitCanonicalMessageEvent(ctx context.Context, q *dbgen.Queries, kind string, m domain.Message, at time.Time) (int64, error) {
	if err := hydrateMessageProjection(ctx, q, &m); err != nil {
		return 0, err
	}
	if err := hydrateReply(ctx, q, &m); err != nil {
		return 0, err
	}
	return change(ctx, q, kind, m, at)
}
