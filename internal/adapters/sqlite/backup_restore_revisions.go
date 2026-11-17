package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

type restoreRevisionSet struct {
	kind            string
	entity, current domain.ID
	revisions       []domain.Revision
	payloads        map[domain.ID]string
	verifiedOwnRoom bool
}

func mergeRestoreRevisions(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, now time.Time) error {
	verifiedRoomAuthors, err := backupVerifiedRoomAuthors(ctx, tx)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, restoreScript("revisions"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var group restoreRevisionSet
	for rows.Next() {
		var kind, parents, created, payload string
		var entity, current, revision domain.ID
		if err := rows.Scan(&kind, &entity, &current, &revision, &parents, &created, &payload); err != nil {
			return err
		}
		if group.kind != kind || group.entity != entity {
			if err := group.apply(ctx, q, now); err != nil {
				return err
			}
			group = restoreRevisionSet{kind: kind, entity: entity, current: current, payloads: map[domain.ID]string{},
				verifiedOwnRoom: kind == "message" && verifiedRoomAuthors[entity]}
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return err
		}
		r := domain.Revision{ID: revision, EntityID: entity, CreatedAt: at}
		if err := json.Unmarshal([]byte(parents), &r.Parents); err != nil {
			return err
		}
		group.revisions = append(group.revisions, r)
		group.payloads[revision] = payload
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return group.apply(ctx, q, now)
}

func backupVerifiedRoomAuthors(ctx context.Context, tx *sql.Tx) (map[domain.ID]bool, error) {
	rows, err := tx.QueryContext(ctx, restoreScript("verified_room_authors"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	verified := make(map[domain.ID]bool)
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		verified[id] = true
	}
	return verified, rows.Err()
}

func (g restoreRevisionSet) apply(ctx context.Context, q *dbgen.Queries, now time.Time) error {
	if len(g.revisions) == 0 {
		return nil
	}
	if _, ok := g.payloads[g.current]; !ok {
		return errors.New("backup entity references a missing current revision")
	}
	winner, err := domain.CurrentRevision(g.revisions)
	if err != nil || (winner.ID == g.current && !g.verifiedOwnRoom) {
		return err
	}
	payload := g.payloads[winner.ID]
	switch g.kind {
	case "account":
		var account domain.Account
		if err := json.Unmarshal([]byte(payload), &account); err != nil {
			return err
		}
		current, err := q.GetAccount(ctx, string(g.entity))
		if err != nil {
			return err
		}
		var prior domain.Account
		if err := json.Unmarshal([]byte(current), &prior); err != nil {
			return err
		}
		if account.ID != g.entity || account.RevisionID != winner.ID || account.Address != prior.Address || account.Protocol != prior.Protocol {
			return errors.New("invalid account revision in backup")
		}
		if err := q.UpdateAccountLabel(ctx, dbgen.UpdateAccountLabelParams{ID: string(g.entity), RevisionID: string(winner.ID), Label: account.Label}); err != nil {
			return err
		}
		_, err = change(ctx, q, "account.updated", account, now)
		return err
	case "message":
		return selectRestoredMessageRevision(ctx, q, g.entity, winner.ID, payload, g.verifiedOwnRoom, now)
	case "star":
		var star domain.MessageStar
		if err := json.Unmarshal([]byte(payload), &star); err != nil {
			return err
		}
		if star.MessageID != g.entity || star.RevisionID != winner.ID {
			return errors.New("invalid star revision in backup")
		}
		return q.PutMessageStar(ctx, dbgen.PutMessageStarParams{
			MessageID: string(g.entity), RevisionID: string(winner.ID), Starred: number(star.Starred),
			Payload: payload, UpdatedAt: stamp(winner.CreatedAt),
		})
	}
	return errors.New("unknown backup entity revision")
}

func selectRestoredMessageRevision(ctx context.Context, q *dbgen.Queries, id, revision domain.ID, payload string, verifiedOwnRoom bool, now time.Time) error {
	row, err := q.GetMessage(ctx, string(id))
	if err != nil {
		return err
	}
	current, err := decodeMessage(row)
	if err != nil {
		return err
	}
	var next domain.Message
	if revision == current.RevisionID {
		next = current
	} else if err := json.Unmarshal([]byte(payload), &next); err != nil {
		return err
	}
	if next.ID != id || next.RevisionID != revision || next.AccountID != current.AccountID || next.ConversationID != current.ConversationID {
		return errors.New("invalid message revision in backup")
	}
	if current.SendState == "unknown" && current.ProviderID == string(id) && !matchesUncertainNativeComposition(current, next) {
		// Keep the uncertain composition while retaining the incoming source
		// revision for history. Reapplying it must keep the same proof boundary.
		return nil
	}
	next.ProviderID, next.ArchiveID, next.ArchiveScope = current.ProviderID, current.ArchiveID, current.ArchiveScope
	if current.ProviderID == "legacy-direct-echo:"+string(id) {
		next.OwnOriginID = ""
	} else if current.OwnOriginID != "" {
		next.OwnOriginID = current.OwnOriginID
	}
	if current.WireMentions != "" && current.SourceBodyHash() == next.SourceBodyHash() &&
		(current.WireMentions == domain.WireMentionsAbsent || current.WireMentionDigest != "" || next.WireMentionDigest == "") {
		next.WireMentions, next.WireMentionDigest = current.WireMentions, current.WireMentionDigest
	}
	if next.IsOwn != current.IsOwn {
		next.Sender.ID = current.Sender.ID
	}
	next.SendState, next.IsOwn, next.Historical = current.SendState, current.IsOwn, current.Historical
	next.CreatedAt, next.TimelineAt = current.CreatedAt, current.TimelineAt
	if verifiedOwnRoom {
		// Verified room authorship is independent of the retained body revision.
		next.SendState, next.IsOwn, next.Sender.ID = "submitted", true, current.AccountID
		if err := q.ReconcileMessageAuthor(ctx, dbgen.ReconcileMessageAuthorParams{ID: string(id), RevisionID: string(revision), Payload: jsonText(next)}); err != nil {
			return err
		}
		if _, err := emitCanonicalMessageEvent(ctx, q, "message.updated", next, now); err != nil {
			return err
		}
	} else if err := q.UpdateMessage(ctx, dbgen.UpdateMessageParams{ID: string(id), RevisionID: string(revision), State: row.State, Payload: jsonText(next)}); err != nil {
		return err
	}
	return q.InvalidateSharedContent(ctx, string(id))
}
