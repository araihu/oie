package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func decodePreview(row dbgen.MessagePreview, err error) (domain.MessagePreview, error) {
	if err != nil {
		return domain.MessagePreview{}, notFound(err)
	}
	var p domain.MessagePreview
	err = json.Unmarshal([]byte(row.Payload), &p)
	return p, err
}
func (s *Store) Preview(ctx context.Context, id domain.ID) (domain.MessagePreview, error) {
	return decodePreview(s.queries.GetMessagePreview(ctx, string(id)))
}
func (s *Store) EnsurePreview(ctx context.Context, p domain.MessagePreview) (domain.MessagePreview, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return p, err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := q.InsertMessagePreview(ctx, dbgen.InsertMessagePreviewParams{ID: string(p.ID), RevisionID: string(p.RevisionID), AccountID: string(p.AccountID), ConversationID: string(p.ConversationID), MessageID: string(p.MessageID), Url: p.URL, State: p.State, DownloadState: p.DownloadState, UpdatedAt: stamp(p.UpdatedAt), Payload: jsonText(p)})
	if err != nil {
		return p, err
	}
	if n == 0 {
		p, err = decodePreview(q.FindMessagePreview(ctx, dbgen.FindMessagePreviewParams{MessageID: string(p.MessageID), Url: p.URL}))
		if err != nil {
			return p, err
		}
	} else {
		if err = previewRevision(ctx, q, p, "[]"); err != nil {
			return p, err
		}
	}
	return p, tx.Commit()
}
func previewRevision(ctx context.Context, q *dbgen.Queries, p domain.MessagePreview, parents string) error {
	if err := q.InsertPreviewRevision(ctx, dbgen.InsertPreviewRevisionParams{ID: string(p.RevisionID), EntityID: string(p.ID), Parents: parents, Payload: jsonText(p), CreatedAt: stamp(p.UpdatedAt)}); err != nil {
		return err
	}
	_, err := change(ctx, q, "message.preview_changed", p, p.UpdatedAt)
	return err
}
func (s *Store) SavePreview(ctx context.Context, p domain.MessagePreview, expected domain.ID) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := q.UpdateMessagePreview(ctx, dbgen.UpdateMessagePreviewParams{RevisionID: string(p.RevisionID), State: p.State, DownloadState: p.DownloadState, UpdatedAt: stamp(p.UpdatedAt), Payload: jsonText(p), ID: string(p.ID), RevisionID_2: string(expected)})
	if err != nil {
		return err
	}
	if n != 1 {
		return application.ErrStateConflict
	}
	if err = previewRevision(ctx, q, p, jsonText([]domain.ID{expected})); err != nil {
		return err
	}
	return tx.Commit()
}
func previewRows(rows []dbgen.MessagePreview, err error) ([]domain.MessagePreview, error) {
	if err != nil {
		return nil, err
	}
	out := make([]domain.MessagePreview, 0, len(rows))
	for _, row := range rows {
		p, err := decodePreview(row, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Store) PreviewJobs(ctx context.Context, before time.Time, limit int) ([]domain.MessagePreview, error) {
	return previewRows(s.queries.PreviewJobs(ctx, dbgen.PreviewJobsParams{RetryBefore: stamp(before), JobLimit: int64(limit)}))
}
func (s *Store) InterruptedPreviewJobs(ctx context.Context, limit int) ([]domain.MessagePreview, error) {
	return previewRows(s.queries.InterruptedPreviewJobs(ctx, int64(limit)))
}
func (s *Store) SaveMediaFile(ctx context.Context, f domain.MediaFile, at time.Time) error {
	return s.queries.InsertMediaFile(ctx, dbgen.InsertMediaFileParams{Hash: f.Hash, MimeType: f.MimeType, Size: f.Size, CreatedAt: stamp(at)})
}
func (s *Store) MediaFile(ctx context.Context, hash string) (domain.MediaFile, error) {
	f, err := s.queries.GetMediaFile(ctx, hash)
	if err != nil {
		return domain.MediaFile{}, notFound(err)
	}
	return domain.MediaFile{Hash: f.Hash, MimeType: f.MimeType, Size: f.Size}, nil
}
