package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stanza"
)

const archivePageSize = 50

type archivePageRequest struct {
	Start, End                  time.Time
	Before, After, Search, With string
	Backward                    bool
	Prepend                     bool
	Limit                       int
	ReactionAnchor              domain.ArchiveReactionAnchor
}

type archivePage struct {
	Query          *archiveQuery
	First, Last    string
	Complete       bool
	Prepend        bool
	ReactionAnchor domain.ArchiveReactionAnchor
}

func (r archivePageRequest) payload(id string) string {
	var form strings.Builder
	if !r.Start.IsZero() || !r.End.IsZero() || r.Search != "" || r.With != "" {
		form.WriteString(`<x xmlns="jabber:x:data" type="submit"><field var="FORM_TYPE" type="hidden"><value>` + mamNS + `</value></field>`)
		field := func(name, value string) {
			form.WriteString(`<field var="` + name + `"><value>` + escaped(value) + `</value></field>`)
		}
		if !r.Start.IsZero() {
			field("start", r.Start.UTC().Format(time.RFC3339Nano))
		}
		if !r.End.IsZero() {
			field("end", r.End.UTC().Format(time.RFC3339Nano))
		}
		if r.With != "" {
			field("with", r.With)
		}
		if r.Search != "" {
			field("{urn:xmpp:fulltext:0}fulltext", r.Search)
		}
		form.WriteString("</x>")
	}
	limit := r.Limit
	if limit == 0 {
		limit = archivePageSize
	}
	set := "<max>" + strconv.Itoa(limit) + "</max>"
	if r.Backward {
		set += "<before>" + escaped(r.Before) + "</before>"
	} else if r.After != "" {
		set += "<after>" + escaped(r.After) + "</after>"
	}
	return `<query xmlns="` + mamNS + `" queryid="` + id + `">` + form.String() + `<set xmlns="http://jabber.org/protocol/rsm">` + set + "</set></query>"
}

// The caller holds the archive domain's turn until the page has been processed.
func (m *Manager) readArchivePage(ctx context.Context, e *entry, s *mx.Session, scope string, request archivePageRequest) (archivePage, error) {
	q := &archiveQuery{ID: randomQueryID(), Scope: scope, Start: request.Start, End: request.End}
	if q.ID == "" {
		return archivePage{}, errArchive
	}
	e.mu.RLock()
	e.historyMu.Lock()
	if e.session != s || e.roomConfigurations[scope].pending {
		e.historyMu.Unlock()
		e.mu.RUnlock()
		return archivePage{}, errArchive
	}
	q.SIDVerified = e.sidScopes[scope]
	q.OccupantVerified = e.occupantScopes[scope]
	q.Semianonymous = e.semianonymousRooms[scope]
	if q.SIDVerified && q.OccupantVerified && q.Semianonymous {
		slot := e.rooms[scope]
		if e.session == s && slot != nil && slot.session == s && slot.snapshot.State == "joined" && !slot.leaving {
			q.OwnReactionActor = roomReactionActor(e.account.ID, scope, slot.ownOccupantID)
		}
	}
	if e.archives == nil {
		e.archives = make(map[string]*archiveQuery)
	}
	e.archives[q.ID] = q
	e.historyMu.Unlock()
	e.mu.RUnlock()
	payload, err := readArchiveIQ(ctx, e, s, scope, stanza.SetIQ, request.payload(q.ID))
	e.historyMu.Lock()
	delete(e.archives, q.ID)
	e.historyMu.Unlock()
	if err != nil {
		return archivePage{}, err
	}
	if q.Err != nil {
		return archivePage{}, q.Err
	}
	var result struct {
		Fin *struct {
			Complete bool   `xml:"complete,attr"`
			Stable   string `xml:"stable,attr"`
			Set      struct {
				First string `xml:"first"`
				Last  string `xml:"last"`
			} `xml:"http://jabber.org/protocol/rsm set"`
		} `xml:"urn:xmpp:mam:2 fin"`
	}
	if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &result) != nil || result.Fin == nil {
		return archivePage{}, errArchive
	}
	fin := result.Fin
	if fin.Stable == "false" {
		return archivePage{}, errUnstable
	}
	first, last := fin.Set.First, fin.Set.Last
	if first != q.FirstID || last != q.LastID || (!fin.Complete && (first == "" || last == "")) ||
		(request.Before != "" && first == request.Before) || (request.After != "" && last == request.After) {
		return archivePage{}, errArchive
	}
	return archivePage{Query: q, First: first, Last: last, Complete: fin.Complete, Prepend: request.Prepend, ReactionAnchor: request.ReactionAnchor}, nil
}

func (m *Manager) ingestArchivePage(ctx context.Context, page archivePage) error {
	// XEP-0313 gives these results a chronological order even when several
	// events have the same timestamp or the page was fetched backwards.
	type tie struct {
		ids    []string
		events []*application.IncomingReaction
	}
	ties := make(map[time.Time]*tie)
	for _, in := range page.Query.Messages {
		if r := in.Reaction; r != nil {
			group := ties[r.CreatedAt]
			if group == nil {
				group = &tie{}
				ties[r.CreatedAt] = group
			}
			group.ids = append(group.ids, r.ArchiveID)
			group.events = append(group.events, r)
		}
	}
	for at, group := range ties {
		anchor := page.ReactionAnchor
		offset := 0
		if anchor.ID != "" && anchor.At.Equal(at) && !slices.Contains(group.ids, anchor.ID) {
			if page.Prepend {
				group.ids = append(group.ids, anchor.ID)
			} else {
				group.ids = append([]string{anchor.ID}, group.ids...)
				offset = 1
			}
		}
		for i, r := range group.events {
			r.ArchivePrepend = page.Prepend
			r.ArchiveEarlier, r.ArchiveLater = group.ids[:i+offset], group.ids[i+offset+1:]
		}
	}
	receive := func(in application.Incoming) error {
		err := m.host.Receive(ctx, in)
		if in.Reaction != nil && errors.Is(err, application.ErrInvalid) {
			return nil
		}
		return err
	}
	for _, in := range page.Query.Messages {
		if !page.Prepend || in.Reaction == nil {
			if err := receive(in); err != nil {
				return err
			}
		}
	}
	if page.Prepend {
		// Start at the already retained end of an older page.
		for _, in := range slices.Backward(page.Query.Messages) {
			if in.Reaction != nil {
				if err := receive(in); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// A cursor can fall on text rather than a reaction. Carry the closest
// reaction through intervening pages as long as the archive stamp is tied.
func (page archivePage) nextReactionAnchor(backward bool) domain.ArchiveReactionAnchor {
	at := page.Query.LastAt
	if backward {
		at = page.Query.FirstAt
	}
	var anchor domain.ArchiveReactionAnchor
	if page.ReactionAnchor.At.Equal(at) {
		anchor = page.ReactionAnchor
	}
	for _, in := range page.Query.Messages {
		if r := in.Reaction; r != nil && r.CreatedAt.Equal(at) {
			anchor = domain.ArchiveReactionAnchor{At: at, ID: r.ArchiveID}
			if backward {
				break
			}
		}
	}
	return anchor
}
