package xmpp

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stanza"
)

const roomBootstrapMessages = 128

type roomArchiveCapability struct {
	mam, textSearch bool
}

func (m *Manager) WakeRoomArchive(account domain.ID) {
	m.mu.RLock()
	e := m.entries[account]
	m.mu.RUnlock()
	if e == nil {
		return
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	select {
	case e.historyWake <- struct{}{}:
	default:
	}
}

func (m *Manager) roomArchiveSupport(ctx context.Context, e *entry, s *mx.Session, address string) (roomArchiveCapability, error) {
	e.historyMu.Lock()
	capability, checked := e.archiveCapabilities[address]
	configuration := e.roomConfigurations[address]
	e.historyMu.Unlock()
	if checked {
		return capability, nil
	}
	body, err := readArchiveIQ(ctx, e, s, address, stanza.GetIQ, discoInfoQuery)
	if err != nil {
		return capability, err
	}
	info, err := m.decodeDiscoInfo(ctx, e, s, address, body, configuration.revision)
	recordDiscovery(e, s, discovered("room", address, info, err))
	if err != nil {
		return capability, err
	}
	for _, feature := range info.Features {
		capability.mam = capability.mam || feature.Var == mamNS
		capability.textSearch = capability.textSearch || feature.Var == "urn:xmpp:fulltext:0"
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.session != s || ctx.Err() != nil {
		return roomArchiveCapability{}, application.ErrProtocolUnavailable
	}
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	if e.roomConfigurations[address].revision != configuration.revision {
		return roomArchiveCapability{}, errArchive
	}
	if e.archiveCapabilities == nil {
		e.archiveCapabilities = make(map[string]roomArchiveCapability)
	}
	e.archiveCapabilities[address] = capability
	return capability, nil
}

func (m *Manager) RequestRoomArchive(ctx context.Context, a domain.Account, c domain.Conversation, request domain.RoomArchiveRequest) (domain.RoomArchiveResult, error) {
	host, ok := m.host.(application.RoomArchiveHost)
	if !ok {
		return domain.RoomArchiveResult{}, application.ErrProtocolUnavailable
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e != nil {
		e.mu.RLock()
		s, sessionCtx := e.session, e.archiveSessionCtx
		e.mu.RUnlock()
		if s != nil && sessionCtx != nil && sessionCtx.Err() == nil {
			result, err := m.roomArchiveTurn(ctx, sessionCtx, host, e, s, c, request, archiveInteractive, time.Now().UTC())
			if err == nil || errors.Is(err, application.ErrInvalid) || ctx.Err() != nil {
				return result, err
			}
		}
	}
	archive, err := host.RoomArchive(ctx, c.ID)
	return domain.RoomArchiveResult{Archive: archive, Outcome: "unavailable"}, err
}

// Use the request context while queued. Once dispatched, keep the turn until
// the session has finished collecting the page, even if the browser moves on.
func (m *Manager) roomArchiveTurn(waitCtx, sessionCtx context.Context, host application.RoomArchiveHost, e *entry, s *mx.Session, c domain.Conversation, request domain.RoomArchiveRequest, priority archivePriority, cutoff time.Time) (domain.RoomArchiveResult, error) {
	release, err := m.acquireArchive(waitCtx, c.Address, priority)
	if err != nil {
		return domain.RoomArchiveResult{}, err
	}
	defer release()
	if err := sessionCtx.Err(); err != nil {
		return domain.RoomArchiveResult{}, err
	}
	a, err := host.RoomArchive(sessionCtx, c.ID)
	if err != nil {
		return domain.RoomArchiveResult{}, err
	}
	result := domain.RoomArchiveResult{Archive: a, Outcome: "complete"}
	var reactionReplay *roomSlot
	if (a.InitialComplete || !a.DeferredReactions.Start.IsZero()) && (priority == archiveAutomatic || request.Action == "focus") {
		reactionReplay = pendingRoomReactionReplay(e, s, c.Address, !a.DeferredReactions.Start.IsZero())
	}
	if priority == archiveAutomatic && a.InitialComplete && (a.OlderComplete || a.Mode == "on_demand") && !a.CaughtUpThrough.Before(cutoff) && reactionReplay == nil && !currentRoomConfiguration(e, c.Address).pending {
		return result, nil
	}
	capability, err := m.roomArchiveSupport(sessionCtx, e, s, c.Address)
	p := &result.Archive.RoomArchiveProgress
	availability, search := "supported", "unsupported"
	switch {
	case errors.Is(err, errDetailPermission):
		availability = "unavailable"
	case err != nil:
		return result, err
	case !capability.mam:
		availability = "unsupported"
	case capability.textSearch:
		search = "supported"
	}
	if p.Availability != availability || p.TextSearch != search {
		p.Availability, p.TextSearch = availability, search
		if err := host.StoreRoomArchiveProgress(sessionCtx, c.ID, *p); err != nil {
			return result, err
		}
	}
	if availability != "supported" {
		result.Outcome = availability
		return result, nil
	}
	if (priority == archiveAutomatic || request.Action == "focus") && (p.InitialComplete || !p.DeferredReactions.Start.IsZero()) {
		reactionReplay = pendingRoomReactionReplay(e, s, c.Address, !p.DeferredReactions.Start.IsZero())
		if reactionReplay != nil && !p.DeferredReactions.Start.IsZero() {
			stored, err := m.recoverDeferredRoomReactions(sessionCtx, host, e, s, c, p, reactionReplay)
			if stored {
				result.Outcome = "stored"
			}
			return result, err
		}
		if reactionReplay != nil {
			if err := m.recoverRecentRoomReactions(sessionCtx, e, s, c.Address, cutoff, reactionReplay); err != nil {
				return result, err
			}
		}
	}
	if request.Action == "search" || request.Action == "context" || request.Action == "older" && request.Anchor != nil &&
		(p.Boundary.IsZero() || request.Anchor.CreatedAt.UnixMilli() < p.Boundary.UnixMilli()) {
		return m.isolatedRoomPage(sessionCtx, host, e, s, c, result, request)
	}
	if p.WindowEnd.IsZero() {
		p.WindowEnd = time.Now().UTC()
		p.WindowStart = p.WindowEnd.Add(-48 * time.Hour)
		p.CaughtUpThrough = p.WindowEnd
		if err := host.StoreRoomArchiveProgress(sessionCtx, c.ID, *p); err != nil {
			return result, err
		}
	}
	switch {
	case priority == archiveAutomatic && p.InitialComplete && p.CaughtUpThrough.Before(cutoff):
		err = m.catchUpRoomArchive(sessionCtx, host, e, s, c, p, cutoff)
	case request.Action == "focus" && p.InitialComplete:
		return result, nil
	case p.OlderComplete || priority == archiveAutomatic && p.InitialComplete && a.Mode == "on_demand":
		return result, nil
	default:
		err = m.extendRoomArchive(sessionCtx, host, e, s, c, p)
	}
	if err == nil {
		result.Outcome = "stored"
	}
	return result, err
}

func pendingRoomReactionReplay(e *entry, session *mx.Session, room string, deferred bool) *roomSlot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	slot, semianonymous, verified := roomReactionSupport(e, room)
	if slot == nil || slot.session != session || !semianonymous || !verified || slot.ownOccupantID == "" || (slot.recentReactionsRecovered && !deferred) {
		return nil
	}
	return slot
}

// A room can be caught up before self-presence identifies our occupant. Read
// its latest page again once per join, without advancing the text checkpoints.
func (m *Manager) recoverRecentRoomReactions(ctx context.Context, e *entry, s *mx.Session, room string, cutoff time.Time, slot *roomSlot) error {
	page, err := m.readArchivePage(ctx, e, s, room, archivePageRequest{Backward: true, End: cutoff})
	if err != nil || page.Query.OwnReactionActor == "" {
		return err
	}
	if err = m.ingestRoomReactionPage(ctx, page); err != nil {
		return err
	}
	markRoomReactionReplay(e, s, room, slot, page.Query.OwnReactionActor)
	return nil
}

func (m *Manager) ingestRoomReactionPage(ctx context.Context, page archivePage) error {
	page.Query.Messages = slices.DeleteFunc(page.Query.Messages, func(in application.Incoming) bool {
		return in.Reaction == nil
	})
	return m.ingestArchivePage(ctx, page)
}

func markRoomReactionReplay(e *entry, s *mx.Session, room string, slot *roomSlot, actor string) {
	e.mu.Lock()
	if e.rooms[room] == slot && slot.session == s && actor == roomReactionActor(e.account.ID, room, slot.ownOccupantID) {
		slot.recentReactionsRecovered = true
	}
	e.mu.Unlock()
}

func rememberDeferredRoomReactions(p *domain.RoomArchiveProgress, skipped domain.DeferredRoomReactions) bool {
	if skipped.Start.IsZero() {
		return false
	}
	r := &p.DeferredReactions
	previous := *r
	if r.Start.IsZero() || skipped.Start.Before(r.Start) {
		r.Start = skipped.Start
	}
	if skipped.End.After(r.End) {
		r.End = skipped.End
	}
	if !r.Boundary.IsZero() && !skipped.Start.After(r.Boundary) {
		r.Boundary, r.Cursor = time.Time{}, ""
	}
	return previous != *r
}

func (m *Manager) recoverDeferredRoomReactions(ctx context.Context, host application.RoomArchiveHost, e *entry, s *mx.Session, c domain.Conversation, p *domain.RoomArchiveProgress, slot *roomSlot) (bool, error) {
	r := &p.DeferredReactions
	req := archivePageRequest{Start: r.Start, End: r.End, After: r.Cursor}
	page, err := m.readArchivePage(ctx, e, s, c.Address, req)
	if errors.Is(err, errArchiveCursor) && req.After != "" && !r.Boundary.IsZero() {
		req.After, req.Start = "", r.Boundary
		page, err = m.readArchivePage(ctx, e, s, c.Address, req)
		if err == nil && !page.Complete && page.Last == r.Cursor {
			return false, errArchive
		}
	}
	if err != nil || page.Query.OwnReactionActor == "" {
		return false, err
	}
	if err := m.ingestRoomReactionPage(ctx, page); err != nil {
		return false, err
	}
	if page.Complete {
		*r = domain.DeferredRoomReactions{}
	} else {
		r.Boundary, r.Cursor = page.Query.LastAt, page.Last
	}
	if err := host.StoreRoomArchiveProgress(ctx, c.ID, *p); err != nil {
		return false, err
	}
	if page.Complete {
		markRoomReactionReplay(e, s, c.Address, slot, page.Query.OwnReactionActor)
	}
	return true, nil
}

func (m *Manager) extendRoomArchive(ctx context.Context, host application.RoomArchiveHost, e *entry, s *mx.Session, c domain.Conversation, p *domain.RoomArchiveProgress) error {
	req := archivePageRequest{Backward: true, Before: p.Cursor, End: p.WindowStart, Prepend: p.Cursor != "" || p.RecentComplete}
	if !p.RecentComplete {
		req.Start, req.End = p.WindowStart, p.WindowEnd
	} else if !p.InitialComplete {
		count, err := host.CountRoomArchiveMessages(ctx, c.ID, p.Boundary, p.WindowEnd)
		if err != nil {
			return err
		}
		if count >= roomBootstrapMessages {
			p.InitialComplete = true
			return host.StoreRoomArchiveProgress(ctx, c.ID, *p)
		}
		req.Limit = min(archivePageSize, roomBootstrapMessages-count)
	}
	if !p.ResumeEnd.IsZero() {
		req.End = p.ResumeEnd
	}
	page, err := m.readArchivePage(ctx, e, s, c.Address, req)
	if errors.Is(err, errArchiveCursor) && req.Before != "" && !p.Boundary.IsZero() {
		req.Before, req.End = "", p.Boundary
		page, err = m.readArchivePage(ctx, e, s, c.Address, req)
		if err == nil && !page.Complete && page.First == p.Cursor {
			return errArchive
		}
		p.ResumeEnd = req.End
	}
	if err != nil {
		return err
	}
	if err := m.ingestArchivePage(ctx, page); err != nil {
		return err
	}
	rememberDeferredRoomReactions(p, page.Query.Deferred)
	if !page.Query.FirstAt.IsZero() {
		p.Boundary, p.Cursor = page.Query.FirstAt, page.First
	}
	if page.Complete {
		if p.RecentComplete {
			p.OlderComplete, p.InitialComplete = true, true
		} else {
			p.RecentComplete = true
			p.Boundary = p.WindowStart
		}
		p.Cursor, p.ResumeEnd = "", time.Time{}
	}
	if p.RecentComplete && !p.InitialComplete {
		count, err := host.CountRoomArchiveMessages(ctx, c.ID, p.Boundary, p.WindowEnd)
		if err != nil {
			return err
		}
		p.InitialComplete = count >= roomBootstrapMessages
	}
	return host.StoreRoomArchiveProgress(ctx, c.ID, *p)
}

func (m *Manager) catchUpRoomArchive(ctx context.Context, host application.RoomArchiveHost, e *entry, s *mx.Session, c domain.Conversation, p *domain.RoomArchiveProgress, cutoff time.Time) error {
	if p.CatchupEnd.IsZero() {
		p.CatchupStart, p.CatchupEnd = p.CaughtUpThrough, cutoff
	}
	req := archivePageRequest{Start: p.CatchupStart, End: p.CatchupEnd, After: p.CatchupCursor}
	page, err := m.readArchivePage(ctx, e, s, c.Address, req)
	if errors.Is(err, errArchiveCursor) && req.After != "" && !p.CatchupBoundary.IsZero() {
		req.After, req.Start = "", p.CatchupBoundary
		page, err = m.readArchivePage(ctx, e, s, c.Address, req)
		if err == nil && !page.Complete && page.Last == p.CatchupCursor {
			return errArchive
		}
		p.CatchupStart = req.Start
	}
	if err != nil {
		return err
	}
	if err := m.ingestArchivePage(ctx, page); err != nil {
		return err
	}
	rememberDeferredRoomReactions(p, page.Query.Deferred)
	if page.Complete {
		p.CaughtUpThrough = p.CatchupEnd
		p.CatchupStart, p.CatchupEnd, p.CatchupBoundary, p.CatchupCursor = time.Time{}, time.Time{}, time.Time{}, ""
	} else {
		p.CatchupBoundary, p.CatchupCursor = page.Query.LastAt, page.Last
	}
	return host.StoreRoomArchiveProgress(ctx, c.ID, *p)
}
