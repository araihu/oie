package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) Avatar(ctx context.Context, id domain.ID, address string) (domain.Avatar, error) {
	row, err := s.read.FindAvatar(ctx, dbgen.FindAvatarParams{AccountID: string(id), Address: address})
	if err != nil {
		return domain.Avatar{}, notFound(err)
	}
	var a domain.Avatar
	err = json.Unmarshal([]byte(row.Payload), &a)
	return a, err
}
func (s *Store) Avatars(ctx context.Context, id, after domain.ID, limit int) ([]domain.Avatar, error) {
	rows, err := s.read.ListAvatars(ctx, dbgen.ListAvatarsParams{AccountID: string(id), ID: string(after), Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Avatar, 0, len(rows))
	for _, row := range rows {
		var a domain.Avatar
		if err = json.Unmarshal([]byte(row.Payload), &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
func (s *Store) AvatarTargets(ctx context.Context, id domain.ID) ([]domain.AvatarTarget, error) {
	rows, err := s.queries.ListAvatarTargets(ctx, string(id))
	if err != nil {
		return nil, err
	}
	out := make([]domain.AvatarTarget, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AvatarTarget{Address: row.Address, Kind: row.Kind})
	}
	return out, nil
}
func (s *Store) SaveAvatar(ctx context.Context, a domain.Avatar) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	parents := "[]"
	row, err := q.FindAvatar(ctx, dbgen.FindAvatarParams{AccountID: string(a.AccountID), Address: a.Address})
	if err == nil {
		var old domain.Avatar
		if err = json.Unmarshal([]byte(row.Payload), &old); err != nil {
			return err
		}
		if old.ImageHash == a.ImageHash && old.SourceToken == a.SourceToken {
			return nil
		}
		a.ID = old.ID
		parents = jsonText([]domain.ID{old.RevisionID})
		err = q.UpdateAvatar(ctx, dbgen.UpdateAvatarParams{RevisionID: string(a.RevisionID), Payload: jsonText(a), ID: string(a.ID)})
	} else if errors.Is(err, sql.ErrNoRows) {
		err = q.InsertAvatar(ctx, dbgen.InsertAvatarParams{ID: string(a.ID), RevisionID: string(a.RevisionID), AccountID: string(a.AccountID), Address: a.Address, Payload: jsonText(a)})
	}
	if err != nil {
		return err
	}
	if err = q.InsertAvatarRevision(ctx, dbgen.InsertAvatarRevisionParams{ID: string(a.RevisionID), EntityID: string(a.ID), Parents: parents, Payload: jsonText(a), CreatedAt: stamp(a.UpdatedAt)}); err != nil {
		return err
	}
	if _, err = change(ctx, q, "profile.avatar_changed", a, a.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}
