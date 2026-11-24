package xmpp

import (
	"context"
	"errors"
	"strconv"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
)

func (m *Manager) bindRoomOMEMOEcho(ctx context.Context, account domain.ID, room, author, origin, envelope, stanzaID, archiveID string) error {
	host, ok := m.host.(application.RoomOMEMOEchoHost)
	const uuidLength = 36
	if !ok || len(origin) < uuidLength || (stanzaID == "" && archiveID == "") {
		return nil
	}
	id, err := domain.ParseID(origin[:uuidLength])
	if err != nil {
		return nil
	}
	part := 0
	if suffix := origin[uuidLength:]; suffix != "" {
		if len(suffix) < 2 || suffix[0] != '-' {
			return nil
		}
		part, err = strconv.Atoi(suffix[1:])
		if err != nil || part < 1 || part > application.MaxMessageAttachments || strconv.Itoa(part) != suffix[1:] {
			return nil
		}
	}
	err = host.BindRoomOMEMOEcho(ctx, application.RoomOMEMOEcho{
		AccountID: account, MessageID: id, Room: room, Author: author, Part: part,
		EnvelopeID: envelope, StanzaID: stanzaID, ArchiveID: archiveID,
	})
	if errors.Is(err, application.ErrInvalid) {
		return nil
	}
	return err
}

func rememberRoomOMEMOGaps(p *domain.RoomArchiveProgress, gaps domain.ArchiveTimeRange) bool {
	if gaps.Start.IsZero() {
		return false
	}
	var pending domain.DeferredRoomOMEMO
	if p.DeferredOMEMO != nil {
		pending = *p.DeferredOMEMO
	}
	pending.Pending.Include(gaps)
	// A normal page or an isolated context can find a gap on the already
	// retried side of this pass. Keep that portion for the next pass.
	if !pending.Boundary.IsZero() && !gaps.Start.After(pending.Boundary) {
		if gaps.End.After(pending.Boundary) {
			gaps.End = pending.Boundary
		}
		pending.Remaining.Include(gaps)
	}
	p.DeferredOMEMO = &pending
	return true
}

func (m *Manager) retryRoomOMEMOArchive(ctx context.Context, host application.RoomArchiveHost, e *entry, s *mx.Session, c domain.Conversation, p *domain.RoomArchiveProgress) (bool, error) {
	work := *p.DeferredOMEMO
	req := archivePageRequest{Start: work.Pending.Start, End: work.Pending.End, After: work.Cursor}
	page, err := m.readArchivePage(ctx, e, s, c.Address, req)
	if errors.Is(err, errArchiveCursor) && req.After != "" && !work.Boundary.IsZero() {
		req.After, req.Start = "", work.Boundary
		page, err = m.readArchivePage(ctx, e, s, c.Address, req)
		if err == nil && !page.Complete && page.Last == work.Cursor {
			return false, errArchive
		}
	}
	if err != nil {
		return false, err
	}
	if err := m.ingestArchivePage(ctx, page); err != nil {
		return false, err
	}
	work.Remaining.Include(page.Query.OMEMOGaps)
	if page.Complete {
		if work.Remaining.Start.IsZero() {
			p.DeferredOMEMO = nil
		} else {
			p.DeferredOMEMO = &domain.DeferredRoomOMEMO{Pending: work.Remaining}
		}
	} else {
		work.Cursor, work.Boundary = page.Last, page.Query.LastAt
		p.DeferredOMEMO = &work
	}
	if err := host.StoreRoomArchiveProgress(ctx, c.ID, *p); err != nil {
		return false, err
	}
	if page.Complete && p.DeferredOMEMO != nil {
		return true, errOMEMOHistoryIncomplete
	}
	return true, nil
}
