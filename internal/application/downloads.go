package application

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/araihu/oie/internal/domain"
)

const MaxDownloadRetentionDays = 3650

type DownloadRepository interface {
	DownloadPolicy(context.Context, domain.ID) (domain.DownloadPolicy, error)
	SetDownloadDays(context.Context, domain.ID, int) error
	ClaimLegacyImageDownloads(context.Context, domain.ID) error
	DeviceDownload(context.Context, domain.ID, domain.ID, domain.ID) (domain.Download, error)
	SaveDownload(context.Context, domain.Download) error
	SaveDownloadedPreview(context.Context, domain.MessagePreview, domain.ID, time.Time) error
	PurgeDownloads(context.Context, domain.ID, time.Time, NewID) error
	DownloadGarbage(context.Context) ([]domain.DownloadGarbage, error)
	ShouldDeleteDownload(context.Context, domain.DownloadGarbage) (bool, error)
	CompleteDownloadGarbage(context.Context, domain.DownloadGarbage, bool) error
}

// DownloadStage is opened privately and removed on close. Publish makes the
// completed bytes available by their SHA-256 without moving the active reader.
type DownloadStage interface {
	io.ReadWriteSeeker
	io.Closer
	Publish(string) error
}
type DownloadFiles interface {
	Stage(context.Context) (DownloadStage, error)
	Open(context.Context, domain.Download) (io.ReadSeekCloser, error)
	Remove(string) error
}

func (e *Engine) SetDownloadFiles(files DownloadFiles) { e.downloadFiles = files }

func (e *Engine) DownloadPolicy(ctx context.Context, device domain.ID) (domain.DownloadPolicy, error) {
	if !validID(device) {
		return domain.DownloadPolicy{}, ErrInvalid
	}
	repo, ok := e.repo.(DownloadRepository)
	if !ok {
		return domain.DownloadPolicy{}, ErrProtocolUnavailable
	}
	return repo.DownloadPolicy(ctx, device)
}

func (e *Engine) SetDownloadPolicy(ctx context.Context, device domain.ID, days int, clear bool) (domain.DownloadPolicy, error) {
	if !validID(device) || days < 0 || days > MaxDownloadRetentionDays {
		return domain.DownloadPolicy{}, ErrInvalid
	}
	repo, ok := e.repo.(DownloadRepository)
	if !ok {
		return domain.DownloadPolicy{}, ErrProtocolUnavailable
	}
	e.downloadMu.Lock()
	defer e.downloadMu.Unlock()
	if e.role == "local" {
		if err := repo.ClaimLegacyImageDownloads(ctx, device); err != nil {
			return domain.DownloadPolicy{}, err
		}
	}
	target := domain.ID("")
	if clear {
		target = device
	} else if err := repo.SetDownloadDays(ctx, device, days); err != nil {
		return domain.DownloadPolicy{}, err
	}
	if err := e.cleanDownloads(ctx, repo, target); err != nil {
		return domain.DownloadPolicy{}, err
	}
	return repo.DownloadPolicy(ctx, device)
}

func (e *Engine) runDownloadRetention(ctx context.Context, repo DownloadRepository) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for ctx.Err() == nil {
		e.downloadMu.Lock()
		err := e.cleanDownloads(ctx, repo, "")
		e.downloadMu.Unlock()
		if err != nil && ctx.Err() == nil && e.diagnostics != nil {
			e.diagnostics.ReportRetentionFailure()
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
	return ctx.Err()
}

func (e *Engine) cleanDownloads(ctx context.Context, repo DownloadRepository, device domain.ID) error {
	if err := repo.PurgeDownloads(ctx, device, e.now().UTC(), e.newID); err != nil {
		return err
	}
	garbage, err := repo.DownloadGarbage(ctx)
	if err != nil {
		return err
	}
	for _, item := range garbage {
		remove, err := repo.ShouldDeleteDownload(ctx, item)
		if err != nil {
			return err
		}
		if remove {
			files := e.downloadFiles
			if item.Kind == "image" {
				if images, ok := e.mediaFiles.(interface{ Remove(string) error }); ok {
					if err := images.Remove(item.Hash); err != nil {
						return err
					}
				} else {
					continue
				}
			} else if files == nil {
				continue
			} else if err := files.Remove(item.Hash); err != nil {
				return err
			}
		}
		if err := repo.CompleteDownloadGarbage(ctx, item, remove); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) reuseDownload(ctx context.Context, repo DownloadRepository, device domain.ID, item domain.SharedContentItem, source domain.Message) (MediaRead, int64, error) {
	e.downloadMu.Lock()
	defer e.downloadMu.Unlock()
	policy, err := repo.DownloadPolicy(ctx, device)
	if err != nil {
		return MediaRead{}, 0, err
	}
	saved, err := repo.DeviceDownload(ctx, device, item.ID, source.RevisionID)
	if err != nil {
		return MediaRead{}, policy.Generation, err
	}
	body, err := e.downloadFiles.Open(ctx, saved)
	if err != nil {
		return MediaRead{}, policy.Generation, err
	}
	return MediaRead{
		Body: body, FileName: item.FileName, MediaType: saved.MimeType,
		Length: saved.Size, Total: saved.Size, SHA256: saved.Hash, VerifiedAlgorithm: saved.VerifiedAlgorithm,
	}, policy.Generation, nil
}

func (e *Engine) completeDownload(ctx context.Context, result MediaRead, expected []domain.FileDigest, ref domain.Download, generation int64) (MediaRead, error) {
	upstream := result.Body
	defer func() { _ = upstream.Close() }()
	if e.downloadFiles == nil {
		return MediaRead{}, ErrProtocolUnavailable
	}
	stage, err := e.downloadFiles.Stage(ctx)
	if err != nil {
		return MediaRead{}, err
	}
	expose := false
	defer func() {
		if !expose {
			_ = stage.Close()
		}
	}()
	hashes := newDownloadHashes()
	n, err := io.Copy(io.MultiWriter(stage, hashes), io.LimitReader(upstream, result.Length+1))
	if err != nil || n != result.Length {
		return MediaRead{}, MediaFailure{Code: "media_fetch_failed"}
	}
	algorithm, err := hashes.verify(expected)
	if err != nil {
		return MediaRead{}, err
	}
	result.SHA256, result.VerifiedAlgorithm = hex.EncodeToString(hashes.sha256.Sum(nil)), algorithm
	if _, err := stage.Seek(0, io.SeekStart); err != nil {
		return MediaRead{}, err
	}
	if ref.DeviceID != "" {
		ref.Hash, ref.VerifiedAlgorithm = result.SHA256, algorithm
		ref.MimeType, ref.Size = result.MediaType, result.Length
		ref.DownloadedAt = e.now().UTC()
		if err := e.retainDownload(ctx, ref, stage, generation); err != nil {
			return MediaRead{}, err
		}
	}
	result.Body = stage
	result.RangeSupported = false
	expose = true
	return result, nil
}

func (e *Engine) retainDownload(ctx context.Context, ref domain.Download, stage DownloadStage, generation int64) error {
	repo, ok := e.repo.(DownloadRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	e.downloadMu.Lock()
	defer e.downloadMu.Unlock()
	policy, err := repo.DownloadPolicy(ctx, ref.DeviceID)
	if err != nil || policy.Generation != generation {
		return err
	}
	if previous, err := repo.DeviceDownload(ctx, ref.DeviceID, ref.SourceID, ref.SourceRevision); err == nil {
		if file, err := e.downloadFiles.Open(ctx, previous); err == nil {
			return file.Close()
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := stage.Publish(ref.Hash); err != nil {
		return err
	}
	return repo.SaveDownload(ctx, ref)
}
