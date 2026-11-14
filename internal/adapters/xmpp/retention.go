package xmpp

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
)

type archiveRetentionKey struct{}

type archiveRetention struct {
	account domain.ID
	days    int
	now     time.Time
}

func updateArchiveRetention(progress *domain.RoomArchiveProgress, days int, now time.Time) bool {
	if progress.RetentionDays != nil && *progress.RetentionDays == days {
		return false
	}
	cutoff := domain.RetentionCutoff(now, days)
	if progress.RetentionDays != nil {
		previous := *progress.RetentionDays
		if previous > 0 && (days == 0 || days > previous) {
			if progress.OlderComplete && !progress.OlderCoveredThrough.IsZero() {
				progress.ResumeEnd = progress.OlderCoveredThrough
				if oldCutoff := domain.RetentionCutoff(now, previous); oldCutoff.After(progress.ResumeEnd) {
					progress.ResumeEnd = oldCutoff
				}
			}
			progress.OlderComplete = false
		}
	}
	if !cutoff.IsZero() && (!progress.Boundary.IsZero() && progress.Boundary.Before(cutoff) ||
		progress.OlderComplete && (progress.OlderCoveredThrough.IsZero() || progress.OlderCoveredThrough.Before(cutoff))) {
		progress.Boundary = cutoff
		progress.ResumeEnd = cutoff
		progress.Cursor = ""
		progress.OlderReactionAnchor = domain.ArchiveReactionAnchor{}
		if progress.RecentComplete {
			progress.OlderComplete, progress.InitialComplete = true, true
			progress.OlderCoveredThrough = cutoff
		}
	}
	progress.RetentionDays = &days
	return true
}

func (m *Manager) recoverDirectArchives(ctx context.Context, host application.HistoryHost, e *entry, s *mx.Session) error {
	retention, ok := m.host.(application.RetentionHost)
	if !ok {
		return m.recoverArchive(ctx, host, e, s, e.account.Address)
	}
	peers, err := retention.DirectRetentionPeers(ctx, e.account.ID)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(peers))
	var failures error
	for _, peer := range peers {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		checkpoint := "retention-peer:" + strconv.Itoa(peer.Days) + ":" + peer.Address
		scoped := context.WithValue(ctx, archiveRetentionKey{}, archiveRetention{account: e.account.ID, days: peer.Days, now: time.Now().UTC()})
		err := m.recoverArchiveRange(scoped, host, e, s, e.account.Address, checkpoint, peer.Address, nil)
		if err != nil {
			failures = errors.Join(failures, err)
		} else {
			known[peer.Address] = true
		}
	}
	return errors.Join(failures, m.recoverArchiveRange(ctx, host, e, s, e.account.Address, e.account.Address, "", known))
}

func filterKnownDirectPeers(page *archivePage, known map[string]bool) {
	page.Query.Messages = slices.DeleteFunc(page.Query.Messages, func(in application.Incoming) bool {
		if in.Reaction != nil {
			return in.Reaction.Kind == "direct" && known[in.Reaction.Address]
		}
		return in.Kind == "direct" && known[in.Address]
	})
}
