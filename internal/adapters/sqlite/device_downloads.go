package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) DownloadPolicy(ctx context.Context, device domain.ID) (domain.DownloadPolicy, error) {
	tx, q, err := beginRead(ctx, s)
	if err != nil {
		return domain.DownloadPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := q.GetDownloadDevice(ctx, string(device))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DownloadPolicy{}, nil
	}
	if err != nil {
		return domain.DownloadPolicy{}, err
	}
	count, err := q.CountDeviceDownloads(ctx, string(device))
	return domain.DownloadPolicy{Days: int(row.Days), Count: int(count), Generation: row.Generation}, err
}

func (s *Store) SetDownloadDays(ctx context.Context, device domain.ID, days int) error {
	return s.queries.SetDownloadDeviceDays(ctx, dbgen.SetDownloadDeviceDaysParams{DeviceID: string(device), Days: int64(days)})
}

func (s *Store) DeviceDownload(ctx context.Context, device, source, revision domain.ID) (domain.Download, error) {
	row, err := s.read.GetDeviceDownload(ctx, dbgen.GetDeviceDownloadParams{DeviceID: string(device), SourceID: string(source), SourceRevision: string(revision)})
	if err != nil {
		return domain.Download{}, notFound(err)
	}
	return domain.Download{
		DeviceID: device, SourceID: source, SourceRevision: revision, Kind: row.Kind, Hash: row.Hash,
		MimeType: row.MimeType, Size: row.Size, VerifiedAlgorithm: row.VerifiedAlgorithm,
		DownloadedAt: time.UnixMilli(row.DownloadedAtMs).UTC(),
	}, nil
}

func (s *Store) RetainedDownloadHashes(ctx context.Context) ([]string, error) {
	return s.read.ListRetainedDownloadHashes(ctx)
}

func (s *Store) SaveDownload(ctx context.Context, download domain.Download) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.EnsureDownloadDevice(ctx, string(download.DeviceID)); err != nil {
		return err
	}
	if download.Kind == "file" {
		previous, err := q.GetDeviceDownload(ctx, dbgen.GetDeviceDownloadParams{
			DeviceID: string(download.DeviceID), SourceID: string(download.SourceID), SourceRevision: string(download.SourceRevision),
		})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && previous.Hash != download.Hash {
			if err := q.QueueDownloadGarbage(ctx, dbgen.QueueDownloadGarbageParams{Kind: previous.Kind, Hash: previous.Hash}); err != nil {
				return err
			}
		}
	}
	saved, err := q.InsertDeviceDownload(ctx, dbgen.InsertDeviceDownloadParams{
		DeviceID: string(download.DeviceID), SourceID: string(download.SourceID), SourceRevision: string(download.SourceRevision),
		Kind: download.Kind, Hash: download.Hash, MimeType: download.MimeType,
		Size: download.Size, VerifiedAlgorithm: download.VerifiedAlgorithm, DownloadedAtMs: download.DownloadedAt.UnixMilli(),
	})
	if err != nil {
		return err
	}
	if saved > 0 {
		if err := q.PutDownloadMembershipCleanupRevision(ctx, dbgen.PutDownloadMembershipCleanupRevisionParams{
			DeviceID: string(download.DeviceID), SourceID: string(download.SourceID),
			SourceRevision: string(download.SourceRevision), Kind: download.Kind,
		}); err != nil {
			return err
		}
		if err := q.KeepLegacyDownloadMembership(ctx, dbgen.KeepLegacyDownloadMembershipParams{
			DeviceID: string(download.DeviceID), SourceID: string(download.SourceID),
			SourceRevision: string(download.SourceRevision), Kind: download.Kind,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveDownloadedPreview(ctx context.Context, p domain.MessagePreview, expected domain.ID, downloadedAt time.Time) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = updatePreview(ctx, q, p, expected); err != nil {
		return err
	}
	switch p.DownloadState {
	case "ready":
		if err := q.KeepLegacyImageCompletions(ctx, string(p.ID)); err != nil {
			return err
		}
		err = q.FinishImageDownloads(ctx, dbgen.FinishImageDownloadsParams{
			Hash: p.FileHash, MimeType: p.FileMimeType, Size: p.FileSize, VerifiedAlgorithm: p.VerifiedAlgorithm,
			DownloadedAtMs: downloadedAt.UnixMilli(), SourceID: string(p.ID),
		})
		if err != nil {
			return err
		}
	case "failed":
		if err := q.DiscardPendingImageDownloads(ctx, string(p.ID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PurgeDownloads(ctx context.Context, device domain.ID, now time.Time, newID application.NewID) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	var removed []dbgen.DeviceDownload
	if device != "" {
		revision, revisionErr := newID()
		if revisionErr != nil {
			return revisionErr
		}
		if err = q.AdvanceDownloadGeneration(ctx, string(device)); err != nil {
			return err
		}
		if err := q.PutDownloadCleanupParent(ctx, dbgen.PutDownloadCleanupParentParams{
			DeviceID: string(device), RevisionID: string(revision),
		}); err != nil {
			return err
		}
		if err = q.PutDownloadCleanupRevision(ctx, dbgen.PutDownloadCleanupRevisionParams{
			DeviceID: string(device), RevisionID: string(revision),
		}); err != nil {
			return err
		}
		removed, err = q.DropDeviceDownloads(ctx, string(device))
	} else {
		removed, err = q.DropExpiredDownloads(ctx, now.UnixMilli())
	}
	if err != nil {
		return err
	}
	images := map[string]bool{}
	for _, row := range removed {
		if row.Kind == "image" {
			if row.DeviceID != "" && row.DeviceID != "legacy" && row.Hash != "" {
				if err := q.RecordRetiredImageMembership(ctx, dbgen.RecordRetiredImageMembershipParams{
					SourceID: row.SourceID, SourceRevision: row.SourceRevision, Hash: row.Hash, Size: row.Size,
				}); err != nil {
					return err
				}
			}
			if !images[row.SourceID] {
				if err = detachDownloadedImage(ctx, q, row.SourceID, now, newID); err != nil {
					return err
				}
				images[row.SourceID] = true
			}
		}
		if row.Hash != "" {
			if err = q.QueueDownloadGarbage(ctx, dbgen.QueueDownloadGarbageParams{Kind: row.Kind, Hash: row.Hash}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func detachDownloadedImage(ctx context.Context, q *dbgen.Queries, source string, now time.Time, newID application.NewID) error {
	count, err := q.CountImageDownloadSources(ctx, source)
	if err != nil || count > 0 {
		return err
	}
	p, err := decodePreview(q.GetMessagePreview(ctx, source))
	if errors.Is(err, application.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	revision, err := newID()
	if err != nil {
		return err
	}
	old := p.RevisionID
	p.RevisionID, p.UpdatedAt = revision, now
	p.FileHash, p.FileMimeType, p.VerifiedAlgorithm, p.FileSize = "", "", "", 0
	p.DownloadState, p.LastErrorCode = "not_downloaded", ""
	return updatePreview(ctx, q, p, old)
}

func (s *Store) DownloadGarbage(ctx context.Context) ([]domain.DownloadGarbage, error) {
	items, err := s.read.ListDownloadGarbage(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DownloadGarbage, 0, len(items))
	for _, item := range items {
		out = append(out, domain.DownloadGarbage{Kind: item.Kind, Hash: item.Hash})
	}
	return out, nil
}

func (s *Store) ShouldDeleteDownload(ctx context.Context, item domain.DownloadGarbage) (bool, error) {
	count, err := s.read.CountDownloadsByHash(ctx, dbgen.CountDownloadsByHashParams{Kind: item.Kind, Hash: item.Hash})
	if err != nil || count > 0 {
		return false, err
	}
	if item.Kind == "image" {
		used, err := s.read.ImagePreviewStillUsesHash(ctx, item.Hash)
		return !used, err
	}
	return true, nil
}

func (s *Store) CompleteDownloadGarbage(ctx context.Context, item domain.DownloadGarbage, removed bool) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if item.Kind == "image" && removed {
		if err := q.DeleteDownloadImageFile(ctx, item.Hash); err != nil {
			return err
		}
	}
	if err := q.RemoveDownloadGarbage(ctx, dbgen.RemoveDownloadGarbageParams{Kind: item.Kind, Hash: item.Hash}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ClaimLegacyImageDownloads(ctx context.Context, device domain.ID) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.EnsureDownloadDevice(ctx, string(device)); err != nil {
		return err
	}
	claimed, err := q.CopyLegacyImageDownloads(ctx, string(device))
	if err != nil {
		return err
	}
	for _, download := range claimed {
		if err := q.KeepLegacyDownloadMembership(ctx, dbgen.KeepLegacyDownloadMembershipParams{
			DeviceID: string(device), SourceID: download.SourceID,
			SourceRevision: download.SourceRevision, Kind: download.Kind, ClaimedLegacy: 1,
		}); err != nil {
			return err
		}
	}
	if err := q.RecordClaimedLegacyImages(ctx, string(device)); err != nil {
		return err
	}
	if _, err = q.DropDeviceDownloads(ctx, "legacy"); err != nil {
		return err
	}
	if err = q.RemoveLegacyDownloadDevice(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
