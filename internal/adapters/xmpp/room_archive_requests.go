package xmpp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
)

func archiveSearchKey(c domain.Conversation, query string) string {
	key := sha256.Sum256([]byte(string(c.ID) + "\x00" + query))
	return hex.EncodeToString(key[:])
}

func (m *Manager) isolatedRoomPage(ctx context.Context, host application.RoomArchiveHost, e *entry, s *mx.Session, c domain.Conversation, result domain.RoomArchiveResult, request domain.RoomArchiveRequest) (domain.RoomArchiveResult, error) {
	req := archivePageRequest{Backward: true, Prepend: true}
	var searchKey string
	var anchorTime time.Time
	if request.Action == "search" {
		if result.Archive.TextSearch != "supported" {
			result.Outcome = "unsupported"
			return result, nil
		}
		searchKey = archiveSearchKey(c, request.Query)
		req.Search = request.Query
		if request.Cursor != "" {
			key, encoded, _ := strings.Cut(request.Cursor, ".")
			raw, err := base64.RawURLEncoding.DecodeString(encoded)
			if err != nil || key != searchKey || len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
				return result, application.ErrInvalid
			}
			req.Before = string(raw)
		}
	} else {
		anchor := request.Anchor
		anchorTime = anchor.TimelineTime().UTC()
		if anchor.ArchiveScope == c.Address {
			req.Before = anchor.ArchiveID
		}
		if req.Before == "" {
			req.End = anchorTime
		}
		if request.Action == "context" {
			req.Limit = archivePageSize / 2
		}
	}
	page, err := m.readArchivePage(ctx, e, s, c.Address, req)
	if errors.Is(err, errArchiveCursor) && request.Anchor != nil && req.Before != "" {
		req.Before, req.End = "", anchorTime
		page, err = m.readArchivePage(ctx, e, s, c.Address, req)
	}
	if err != nil {
		return result, err
	}
	if err := m.ingestIsolatedRoomPage(ctx, host, c, &result.Archive.RoomArchiveProgress, page, req.Search != ""); err != nil {
		return result, err
	}
	if request.Action == "context" {
		next := archivePageRequest{Limit: req.Limit, Start: anchorTime}
		if req.Before != "" {
			next.After, next.Start = req.Before, time.Time{}
			next.ReactionAnchor = page.nextReactionAnchor(false)
		}
		forward, err := m.readArchivePage(ctx, e, s, c.Address, next)
		if errors.Is(err, errArchiveCursor) && next.After != "" {
			next.After, next.Start, next.ReactionAnchor = "", anchorTime, domain.ArchiveReactionAnchor{}
			forward, err = m.readArchivePage(ctx, e, s, c.Address, next)
		}
		if err != nil {
			return result, err
		}
		if err := m.ingestIsolatedRoomPage(ctx, host, c, &result.Archive.RoomArchiveProgress, forward, false); err != nil {
			return result, err
		}
		result.Outcome = "stored"
		return result, nil
	}
	if !page.Complete || len(page.Query.Messages) != 0 {
		result.Outcome = "stored"
	}
	if request.Action != "search" {
		return result, nil
	}
	if !page.Complete {
		result.NextCursor = searchKey + "." + base64.RawURLEncoding.EncodeToString([]byte(page.First))
	}
	seen := make(map[domain.ID]bool)
	for _, in := range page.Query.Messages {
		if in.ArchiveID == "" {
			continue
		}
		message, err := host.ArchivedRoomMessage(ctx, c, in.ArchiveID)
		if errors.Is(err, application.ErrNotFound) {
			continue
		}
		if err != nil {
			return result, err
		}
		if !seen[message.ID] {
			seen[message.ID] = true
			result.Items = append(result.Items, message)
		}
	}
	return result, nil
}

func (m *Manager) ingestIsolatedRoomPage(ctx context.Context, host application.RoomArchiveHost, c domain.Conversation, p *domain.RoomArchiveProgress, page archivePage, filtered bool) error {
	// The boundaries of an independent context can cut through a group of
	// tied timestamps already read through another cursor. Resolve those
	// groups from the room's contiguous, unfiltered archive.
	page.Query.Messages = slices.DeleteFunc(page.Query.Messages, func(in application.Incoming) bool {
		r := in.Reaction
		if r == nil || (!filtered && !r.CreatedAt.Equal(page.Query.FirstAt) && !r.CreatedAt.Equal(page.Query.LastAt)) {
			return false
		}
		includeDeferredReaction(&page.Query.Deferred, r.CreatedAt)
		return true
	})
	if err := m.ingestArchivePage(ctx, page); err != nil {
		return err
	}
	if rememberDeferredRoomReactions(p, page.Query.Deferred) {
		if err := host.StoreRoomArchiveProgress(ctx, c.ID, *p); err != nil {
			return err
		}
		m.WakeRoomArchive(c.AccountID)
	}
	return nil
}
