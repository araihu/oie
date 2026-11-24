package xmpp

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/stanza"
)

var errOMEMOHistoryIncomplete = errors.New("some archived OMEMO messages could not be decrypted")

type archivedOMEMO struct {
	in       application.Incoming
	sender   string
	envelope *omemo.Envelope
	size     int
}

func archiveOMEMO(account domain.Account, r *archiveResult, at time.Time) (archivedOMEMO, bool) {
	msg := r.Forwarded.Message
	if msg == nil || msg.Encrypted == nil && msg.OMEMO2 == nil ||
		msg.Type != "" && msg.Type != stanza.ChatMessage && msg.Type != stanza.NormalMessage ||
		msg.From.Localpart() == "" || msg.To.Localpart() == "" || len(msg.Users) != 0 {
		return archivedOMEMO{}, false
	}
	from, to := msg.From.Bare().String(), msg.To.Bare().String()
	own := from == account.Address
	peer := from
	if own {
		peer = to
	} else if to != account.Address {
		return archivedOMEMO{}, false
	}
	if peer == account.Address {
		return archivedOMEMO{}, false
	}
	var size int
	if env := msg.Encrypted; env != nil {
		size = env.Size()
	}
	return archivedOMEMO{
		in: application.Incoming{AccountID: account.ID, Address: peer, Kind: "direct", IsOwn: own,
			Historical: true, Encrypted: true, CreatedAt: at.UTC(), ProviderID: msg.ID,
			ArchiveScope: account.Address, ArchiveID: r.ID},
		sender: from, envelope: msg.Encrypted, size: size,
	}, true
}

func (m *Manager) ingestOMEMOArchive(ctx context.Context, e *entry, page archivePage, now time.Time) (bool, error) {
	if len(page.Query.OMEMO) == 0 {
		return false, nil
	}
	self, _, err := e.omemoDevice.LocalIdentity()
	if err != nil {
		return false, err
	}
	gaps := false
	daysByPeer := map[string]int{}
	retention, checksRetention := m.host.(application.RetentionHost)
	for _, record := range page.Query.OMEMO {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		in, env := record.in, record.envelope
		if env != nil && in.IsOwn && env.Header.From == self {
			// Sent by this installation, which stores its cleartext on acceptance.
			continue
		}
		if env != nil && env.Payload == "" && !slices.ContainsFunc(env.Header.Keys, func(k omemo.WrappedKey) bool { return k.To == self }) {
			continue
		}
		if checksRetention {
			days, ok := daysByPeer[in.Address]
			if !ok {
				days, err = retention.RetentionDays(ctx, in.AccountID, in.Address, in.Kind)
				if err != nil {
					return false, err
				}
				daysByPeer[in.Address] = days
			}
			if days != 0 && in.CreatedAt.Before(domain.RetentionCutoff(now, days)) {
				continue
			}
		}
		if err := m.checkArchiveRetention(ctx, in.AccountID); err != nil {
			return false, err
		}
		if env == nil {
			gaps = true
			continue
		}
		legacyID := in.ProviderID
		in.ProviderID = env.ProviderID()
		retained, err := m.retainedOMEMO(ctx, in, "")
		if err != nil {
			return false, err
		}
		if retained {
			continue
		}
		var saveErr error
		err = e.omemoDevice.DecryptTo(ctx, record.sender, *env, func(text string) error {
			if !readOMEMOPayload(&in, text) {
				return nil
			}
			saveErr = m.host.Receive(ctx, in)
			return saveErr
		})
		if err == nil {
			m.refreshOMEMOPreKeys(e, *env)
			continue
		}
		var storageErr *fs.PathError
		if saveErr != nil && !errors.Is(saveErr, application.ErrInvalid) || errors.As(err, &storageErr) || ctx.Err() != nil {
			return false, err
		}
		// A live copy can commit while archive ingestion is waiting for the
		// ratchet lock. Older profiles used the originating stanza ID.
		retained, err = m.retainedOMEMO(ctx, in, legacyID)
		if err != nil {
			return false, err
		}
		gaps = gaps || !retained && env.Payload != ""
	}
	return gaps, nil
}

func (m *Manager) retainedOMEMO(ctx context.Context, in application.Incoming, legacyID string) (bool, error) {
	if host, ok := m.host.(application.OMEMOArchiveHost); ok {
		return host.RetainedOMEMO(ctx, in, legacyID)
	}
	return false, nil
}
