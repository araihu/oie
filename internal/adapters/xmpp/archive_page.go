package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stanza"
)

const archivePageSize = 50

type archivePageRequest struct {
	Start, End            time.Time
	Before, After, Search string
	Backward              bool
	Limit                 int
}

type archivePage struct {
	Query       *archiveQuery
	First, Last string
	Complete    bool
}

func (r archivePageRequest) payload(id string) string {
	var form strings.Builder
	if !r.Start.IsZero() || !r.End.IsZero() || r.Search != "" {
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
	e.historyMu.Lock()
	q.SIDVerified = e.sidScopes[scope]
	q.OccupantVerified = e.occupantScopes[scope]
	if e.archives == nil {
		e.archives = make(map[string]*archiveQuery)
	}
	e.archives[q.ID] = q
	e.historyMu.Unlock()
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
	return archivePage{Query: q, First: first, Last: last, Complete: fin.Complete}, nil
}

func (m *Manager) ingestArchivePage(ctx context.Context, page archivePage) error {
	for _, in := range page.Query.Messages {
		if err := m.host.Receive(ctx, in); err != nil {
			if in.Reaction != nil && errors.Is(err, application.ErrInvalid) {
				continue
			}
			return err
		}
	}
	return nil
}
