package xmpp

import (
	"context"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
)

type archiveRecovery struct {
	supported, partial, discoveryFailed bool
	err                                 error
}

func (m *Manager) recoverAccountArchives(ctx context.Context, host application.HistoryHost, e *entry, s *mx.Session) []archiveRecovery {
	cutoff := time.Now().UTC()
	groups := make(map[string][]domain.Conversation)
	roomHost, ok := m.host.(application.RoomArchiveHost)
	if ok {
		rooms, err := roomHost.ListRoomArchiveConversations(ctx, e.account.ID)
		if err != nil {
			return []archiveRecovery{{partial: true, discoveryFailed: true, err: err}}
		}
		for _, room := range rooms {
			address, err := jid.Parse(room.Address)
			if err == nil && address.Localpart() != "" {
				groups[address.Domainpart()] = append(groups[address.Domainpart()], room)
			}
		}
	}
	outcomes := make(chan archiveRecovery, len(groups)+1)
	for _, rooms := range groups {
		go func() { outcomes <- m.recoverRoomGroup(ctx, roomHost, e, s, rooms, cutoff) }()
	}
	go func() {
		supported, err := m.supportsArchive(ctx, e, s, e.account.Address)
		if err != nil || !supported {
			outcomes <- archiveRecovery{partial: true, discoveryFailed: err != nil}
			return
		}
		err = m.recoverArchive(ctx, host, e, s, e.account.Address)
		outcomes <- archiveRecovery{supported: true, partial: err != nil, err: err}
	}()
	results := make([]archiveRecovery, 0, cap(outcomes))
	for range cap(outcomes) {
		results = append(results, <-outcomes)
	}
	return results
}

func (m *Manager) recoverRoomGroup(ctx context.Context, host application.RoomArchiveHost, e *entry, s *mx.Session, rooms []domain.Conversation, cutoff time.Time) archiveRecovery {
	var status archiveRecovery
	// Give each room one turn per round. Repeat the bootstrap rounds before
	// spending time on history older than the initial target.
	for _, initial := range []bool{true, false} {
		pending := append([]domain.Conversation(nil), rooms...)
		for len(pending) != 0 && ctx.Err() == nil {
			next := pending[:0]
			for _, room := range pending {
				if ctx.Err() != nil {
					return status
				}
				a, err := host.RoomArchive(ctx, room.ID)
				if err != nil {
					status.partial, status.err = true, err
					continue
				}
				if initial && a.InitialComplete {
					continue
				}
				if !initial && a.InitialComplete && (a.Mode == "on_demand" || a.OlderComplete) && !a.CaughtUpThrough.Before(cutoff) && pendingRoomReactionReplay(e, s, room.Address) == nil {
					status.supported = status.supported || a.Availability == "supported"
					continue
				}
				result, err := m.roomArchiveTurn(ctx, ctx, host, e, s, room, domain.RoomArchiveRequest{}, archiveAutomatic, cutoff)
				switch {
				case err != nil:
					status.partial, status.err = true, err
					status.discoveryFailed = status.discoveryFailed || result.Archive.Availability == "unknown"
				case result.Outcome == "unsupported" || result.Outcome == "unavailable":
					status.partial = true
				default:
					status.supported = true
					if result.Outcome == "stored" && (!initial || !result.Archive.InitialComplete) {
						next = append(next, room)
					}
				}
			}
			pending = next
		}
	}
	return status
}
