package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

// ReadMessageEditHistory reads the immutable first content of physical source
// messages. Ordinary send-state revisions never become visible content history.
func (s *Store) ReadMessageEditHistory(ctx context.Context, requested domain.ID) (domain.MessageEditHistoryPage, error) {
	page := domain.MessageEditHistoryPage{RequestedMessageID: requested, Items: []domain.MessageEditHistoryItem{}}
	tx, q, err := begin(ctx, s)
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	requestedRow, err := q.GetMessage(ctx, string(requested))
	if err != nil {
		return page, notFound(err)
	}
	canonical, err := resolveMessageAlias(ctx, q, requested)
	if err != nil {
		return page, err
	}
	row, err := q.GetMessage(ctx, string(canonical))
	if err != nil {
		return page, notFound(err)
	}
	if row.AccountID != requestedRow.AccountID || row.ConversationID != requestedRow.ConversationID {
		return page, application.ErrNotFound
	}
	page.MessageID = canonical
	page.AccountID = domain.ID(row.AccountID)
	page.ConversationID = domain.ID(row.ConversationID)
	head, headErr := q.GetContentHead(ctx, string(canonical))
	if headErr != nil && !errors.Is(headErr, sql.ErrNoRows) {
		return page, headErr
	}
	sources, err := q.ListMessageHistorySources(ctx, dbgen.ListMessageHistorySourcesParams{AccountID: row.AccountID, ConversationID: row.ConversationID, CanonicalID: string(canonical)})
	if err != nil {
		return page, err
	}
	if len(sources) == 0 || len(sources) > 128 {
		return page, application.ErrProtocolUnavailable
	}
	nodes := make(map[domain.ID]*historyNode, len(sources))
	for _, source := range sources {
		var content domain.Message
		if err = json.Unmarshal([]byte(source.Payload), &content); err != nil {
			return page, err
		}
		if content.ID != domain.ID(source.SourceID) || content.AccountID != page.AccountID || content.ConversationID != page.ConversationID || content.RevisionID != domain.ID(source.RevisionID) {
			return page, application.ErrProtocolUnavailable
		}
		at, err := time.Parse(time.RFC3339Nano, source.CreatedAt)
		if err != nil {
			return page, err
		}
		content.Sender.PersonalAddress = ""
		item := domain.MessageEditHistoryItem{RevisionID: domain.ID(source.RevisionID), SourceMessageID: domain.ID(source.SourceID), CreatedAt: at.UTC(), OriginalText: content.OriginalText, Sender: content.Sender, ParentRevisionIDs: []domain.ID{}}
		nodes[item.SourceMessageID] = &historyNode{item: item, source: content}
	}
	original := nodes[canonical]
	if original == nil {
		return page, application.ErrProtocolUnavailable
	}
	proofs, err := q.ListMessageHistoryProofs(ctx, dbgen.ListMessageHistoryProofsParams{AccountID: row.AccountID, ConversationID: row.ConversationID, CanonicalID: string(canonical)})
	if err != nil {
		return page, err
	}
	if errors.Is(headErr, sql.ErrNoRows) {
		// An ambiguous or unsupported correction remains an independent retained
		// source, never an invitation to attach another person's history.
		original.item.IsCurrent = true
		original.item.IsOriginal = true
		for _, proof := range proofs {
			if proof.MessageID == string(canonical) && (proof.TargetID != "" || !historyProofValid(proof, original.source)) {
				original.item.IsOriginal = false
			}
		}
		if len(proofs) > 128 {
			original.item.IsOriginal = false
		}
		page.CurrentRevisionID = original.item.RevisionID
		page.Items = append(page.Items, original.item)
		return page, tx.Commit()
	}
	if len(proofs) > 128 || len(proofs) != len(nodes) {
		return page, application.ErrProtocolUnavailable
	}
	byRemote := map[string]domain.ID{}
	scope, author := "", ""
	for _, proof := range proofs {
		node := nodes[domain.ID(proof.MessageID)]
		if node == nil || node.proof != nil || !historyProofValid(proof, node.source) {
			return page, application.ErrProtocolUnavailable
		}
		if scope != "" && (proof.Scope != scope || proof.Author != author) {
			return page, application.ErrProtocolUnavailable
		}
		scope, author = proof.Scope, proof.Author
		if _, exists := byRemote[proof.RemoteID]; exists {
			return page, application.ErrProtocolUnavailable
		}
		byRemote[proof.RemoteID] = node.item.SourceMessageID
		node.proof = &proof
	}
	selected := nodes[domain.ID(head.SourceID)]
	if selected == nil || selected.proof.SourceRevisionID != head.RevisionID {
		return page, application.ErrProtocolUnavailable
	}
	revisions := make([]domain.Revision, 0, len(nodes))
	for id, node := range nodes {
		parents := []domain.ID{}
		if parentID := byRemote[node.proof.TargetID]; parentID != "" {
			parent := nodes[parentID]
			node.item.ParentRevisionIDs = append(node.item.ParentRevisionIDs, parent.item.RevisionID)
			parents = append(parents, domain.ID(parent.proof.SourceRevisionID))
		}
		seen := map[domain.ID]bool{}
		at := id
		for {
			if seen[at] {
				return page, application.ErrProtocolUnavailable
			}
			seen[at] = true
			parent := byRemote[nodes[at].proof.TargetID]
			if parent == "" {
				break
			}
			at = parent
		}
		if at != canonical {
			return page, application.ErrProtocolUnavailable
		}
		node.item.IsCurrent = id == domain.ID(head.SourceID)
		node.item.IsOriginal = id == canonical && node.proof.TargetID == ""
		revisions = append(revisions, domain.Revision{ID: domain.ID(node.proof.SourceRevisionID), EntityID: canonical, Parents: parents, CreatedAt: node.item.CreatedAt})
		page.Items = append(page.Items, node.item)
	}
	winner, err := domain.CurrentRevision(revisions)
	if err != nil || string(winner.ID) != head.RevisionID {
		return page, application.ErrProtocolUnavailable
	}
	page.CurrentRevisionID = selected.item.RevisionID
	return page, tx.Commit()
}

type historyNode struct {
	item   domain.MessageEditHistoryItem
	source domain.Message
	proof  *dbgen.ListMessageHistoryProofsRow
}

func historyProofValid(proof dbgen.ListMessageHistoryProofsRow, source domain.Message) bool {
	if proof.ProofPayload == nil || !correctionValid(&domain.MessageCorrection{Scope: proof.Scope, Author: proof.Author, ID: proof.RemoteID, TargetID: proof.TargetID}) || bodyHash(source.OriginalText) != proof.SourceHash {
		return false
	}
	var observed domain.Message
	if json.Unmarshal([]byte(*proof.ProofPayload), &observed) != nil {
		return false
	}
	return observed.ID == source.ID && observed.AccountID == source.AccountID && observed.ConversationID == source.ConversationID && observed.RevisionID == domain.ID(proof.SourceRevisionID) && bodyHash(observed.OriginalText) == proof.SourceHash
}
