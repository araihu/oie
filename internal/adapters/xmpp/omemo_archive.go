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
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

var errOMEMOHistoryIncomplete = errors.New("some archived OMEMO messages could not be decrypted")

type archivedOMEMO struct {
	in       application.Incoming
	sender   string
	envelope *omemo.Envelope
	origin   string
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

func archiveRoomOMEMO(account domain.Account, query *archiveQuery, r *archiveResult, at time.Time) (archivedOMEMO, bool) {
	msg := r.Forwarded.Message
	if msg == nil || (msg.Encrypted == nil && msg.OMEMO2 == nil) || msg.Type != stanza.GroupChatMessage ||
		msg.From.Bare().String() != query.Scope || msg.From.Resourcepart() == "" {
		return archivedOMEMO{}, false
	}
	// The authenticated room supplies exactly one original affiliation,JID pair.
	// A current nickname is not evidence for a previous archived message.
	record := archivedOMEMO{in: application.Incoming{AccountID: account.ID, Kind: "room", Address: query.Scope,
		SenderName: msg.From.Resourcepart(), Historical: true, Encrypted: true, CreatedAt: at,
		ProviderID: msg.ID, ArchiveScope: query.Scope, ArchiveID: r.ID}}
	if len(msg.Users) == 1 && len(msg.Users[0].Items) == 1 {
		item := msg.Users[0].Items[0]
		if author, err := jid.Parse(item.JID); err == nil && author.Localpart() != "" && author.Bare().String() != query.Scope && omemoAffiliation(item.Affiliation) {
			record.sender = author.Bare().String()
			record.in.IsOwn = record.sender == account.Address
			record.in.SenderAddress = record.sender
			if query.SIDVerified {
				stanzaID := roomStanzaID(msg.From, msg.Stable)
				if stanzaID == "" && usableID(r.ID) && missingRoomStanzaID(msg.From, msg.Stable) {
					stanzaID = r.ID
				}
				if stanzaID != "" {
					record.in.References = []domain.MessageReference{{Scope: "room:" + query.Scope, Author: msg.From.String(), ID: stanzaID}}
				}
			}
		}
	}
	record.envelope = msg.Encrypted
	record.origin = msg.originID()
	if len(msg.Origins) == 0 {
		record.origin = msg.ID
	}
	if record.envelope != nil {
		record.size = record.envelope.Size()
	}
	return record, true
}

func (m *Manager) ingestOMEMOArchive(ctx context.Context, e *entry, page archivePage, now time.Time) (domain.ArchiveTimeRange, error) {
	var gaps domain.ArchiveTimeRange
	if len(page.Query.OMEMO) == 0 {
		return gaps, nil
	}
	self, _, err := e.omemoDevice.LocalIdentity()
	if err != nil {
		return gaps, err
	}
	daysByPeer := map[string]int{}
	retention, checksRetention := m.host.(application.RetentionHost)
	for _, record := range page.Query.OMEMO {
		if err := ctx.Err(); err != nil {
			return gaps, err
		}
		in, env := record.in, record.envelope
		if page.Query.RoomOMEMO {
			e.mu.RLock()
			authority, ok := roomOMEMOAuthorityLocked(e, page.omemoSession, in.Address)
			e.mu.RUnlock()
			if !ok || authority.slot != page.omemoAuthority.slot || authority.revision != page.omemoAuthority.revision {
				return gaps, errArchive
			}
		}
		if env != nil && in.IsOwn && env.Header.From == self {
			// This installation already retains the content. Match the
			// original ciphertext before adding a reference to its row.
			if page.Query.RoomOMEMO {
				var stableID string
				if len(in.References) == 1 {
					stableID = in.References[0].ID
				}
				if err := m.bindRoomOMEMOEcho(ctx, in.AccountID, in.Address, in.Address+"/"+in.SenderName, record.origin, env.ProviderID(), stableID, in.ArchiveID); err != nil {
					return gaps, err
				}
			}
			continue
		}
		if page.Query.RoomOMEMO && env == nil {
			// There is no legacy envelope to retry for this entry.
			continue
		}
		if env != nil && (page.Query.RoomOMEMO || env.Payload == "") && !slices.ContainsFunc(env.Header.Keys, func(k omemo.WrappedKey) bool { return k.To == self }) {
			continue
		}
		if checksRetention {
			days, ok := daysByPeer[in.Address]
			if !ok {
				days, err = retention.RetentionDays(ctx, in.AccountID, in.Address, in.Kind)
				if err != nil {
					return gaps, err
				}
				daysByPeer[in.Address] = days
			}
			if days != 0 && in.CreatedAt.Before(domain.RetentionCutoff(now, days)) {
				continue
			}
		}
		if err := m.checkArchiveRetention(ctx, in.AccountID); err != nil {
			return gaps, err
		}
		stamp := domain.ArchiveTimeRange{Start: in.CreatedAt, End: in.CreatedAt}
		if env == nil {
			gaps.Include(stamp)
			continue
		}
		legacyID := in.ProviderID
		in.ProviderID = env.ProviderID()
		retained, err := m.retainedOMEMO(ctx, in, "")
		if err != nil {
			return gaps, err
		}
		if retained {
			if err := m.bindRetainedRoomOMEMOArchive(ctx, page, in); err != nil {
				return gaps, err
			}
			continue
		}
		if record.sender == "" {
			gaps.Include(stamp)
			continue
		}
		var saveErr error
		message := omemo.Pending{Peer: record.sender, Envelope: *env}
		if page.Query.RoomOMEMO {
			message.Room = in.Address
		}
		err = e.omemoDevice.Receive(ctx, message, func(text string) error {
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
		if errors.Is(err, omemo.ErrRoomEncryptionDisabled) {
			return gaps, application.ErrStateConflict
		}
		var storageErr *fs.PathError
		if saveErr != nil && !errors.Is(saveErr, application.ErrInvalid) || errors.As(err, &storageErr) || ctx.Err() != nil {
			return gaps, err
		}
		// A live copy can commit while archive ingestion is waiting for the
		// ratchet lock. Older profiles used the originating stanza ID.
		retained, err = m.retainedOMEMO(ctx, in, legacyID)
		if err != nil {
			return gaps, err
		}
		if retained {
			if err := m.bindRetainedRoomOMEMOArchive(ctx, page, in); err != nil {
				return gaps, err
			}
		}
		if !retained && env.Payload != "" {
			gaps.Include(stamp)
		}
	}
	return gaps, nil
}

func (m *Manager) bindRetainedRoomOMEMOArchive(ctx context.Context, page archivePage, in application.Incoming) error {
	if host, ok := m.host.(application.RetainedRoomOMEMOArchiveHost); ok && page.Query.RoomOMEMO && in.SenderAddress != "" {
		err := host.BindRetainedRoomOMEMOArchive(ctx, in)
		if !errors.Is(err, application.ErrInvalid) {
			return err
		}
	}
	return nil
}

func (m *Manager) retainedOMEMO(ctx context.Context, in application.Incoming, legacyID string) (bool, error) {
	if host, ok := m.host.(application.OMEMOArchiveHost); ok {
		return host.RetainedOMEMO(ctx, in, legacyID)
	}
	return false, nil
}
