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

func (m *Manager) beginArchiveRetention(ctx context.Context, account domain.ID) (context.Context, error) {
	if host, ok := m.host.(application.RetentionHost); ok {
		revision, err := host.ArchiveRetentionRevision(ctx, account)
		if err != nil {
			return ctx, err
		}
		ctx = application.WithArchiveRetentionRevision(ctx, revision)
	}
	return ctx, nil
}

func (m *Manager) checkArchiveRetention(ctx context.Context, account domain.ID) error {
	expected, guarded := application.ArchiveRetentionRevisionFrom(ctx)
	host, ok := m.host.(application.RetentionHost)
	if !guarded || !ok {
		return nil
	}
	revision, err := host.ArchiveRetentionRevision(ctx, account)
	if err != nil {
		return err
	}
	if revision != expected {
		return application.ErrStateConflict
	}
	return nil
}

func (m *Manager) recoverDirectArchives(ctx context.Context, host application.HistoryHost, e *entry, s *mx.Session) error {
	checkpointPrefix := ""
	if e.omemoDevice != nil {
		checkpointPrefix = "omemo:"
	}
	retention, ok := m.host.(application.RetentionHost)
	if !ok {
		return m.recoverArchiveRange(ctx, host, e, s, e.account.Address, checkpointPrefix+e.account.Address, "", nil)
	}
	ctx, err := m.beginArchiveRetention(ctx, e.account.ID)
	if err != nil {
		return err
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
		checkpoint := "retention-peer:" + strconv.Itoa(peer.Days) + ":" + checkpointPrefix + peer.Address
		scoped := context.WithValue(ctx, archiveRetentionKey{}, archiveRetention{account: e.account.ID, days: peer.Days, now: time.Now().UTC()})
		err := m.recoverArchiveRange(scoped, host, e, s, e.account.Address, checkpoint, peer.Address, nil)
		if errors.Is(err, application.ErrStateConflict) {
			return err
		}
		if err != nil {
			failures = errors.Join(failures, err)
		}
		if err == nil || errors.Is(err, errOMEMOHistoryIncomplete) {
			known[peer.Address] = true
		}
	}
	return errors.Join(failures, m.recoverArchiveRange(ctx, host, e, s, e.account.Address, checkpointPrefix+e.account.Address, "", known))
}

func filterKnownDirectPeers(page *archivePage, known map[string]bool) {
	page.Query.OMEMO = slices.DeleteFunc(page.Query.OMEMO, func(record archivedOMEMO) bool {
		return known[record.in.Address]
	})
	page.Query.Messages = slices.DeleteFunc(page.Query.Messages, func(in application.Incoming) bool {
		if in.Reaction != nil {
			return in.Reaction.Kind == "direct" && known[in.Reaction.Address]
		}
		return in.Kind == "direct" && known[in.Address]
	})
}
