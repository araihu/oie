package web

import (
	_ "embed"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // The hosted scratch image has no system zoneinfo.
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"golang.org/x/text/cases"
)

// The toolbar composes public Goshtoso textinput, checkbox and button controls.
// Search result identity, paging and bounded timeline focus remain app-owned.
//
//go:embed conversation-search.js
var conversationSearchScript []byte

//go:embed conversation-search.css
var conversationSearchStylesheet []byte

func (v view) conversationSearchCloseHref() string {
	return v.href("chatSearch", "", "chatQuery", "", "chatSender", "", "chatFromDate", "", "chatToDate", "", "chatIncludeHistory", "", "chatIncludeRetracted", "", "chatSearchRevision", "")
}

type conversationSearchFilters struct {
	Sender, FromDate, ToDate, TimeZone string
}

func (f conversationSearchFilters) active() bool {
	return f.Sender != "" || f.FromDate != "" || f.ToDate != ""
}
func (f conversationSearchFilters) writeQuery(q url.Values) {
	for key, value := range map[string]string{"sender": f.Sender, "fromDate": f.FromDate, "toDate": f.ToDate, "timeZone": f.TimeZone} {
		if value != "" {
			q.Set(key, value)
		}
	}
}
func (f conversationSearchFilters) bounds() (from, before *time.Time, valid bool) {
	if !utf8.ValidString(f.Sender) || strings.ContainsRune(f.Sender, 0) || utf8.RuneCountInString(f.Sender) > 200 || f.FromDate != "" && f.ToDate != "" && f.FromDate > f.ToDate {
		return nil, nil, false
	}
	if f.FromDate == "" && f.ToDate == "" {
		return nil, nil, true
	}
	if len(f.TimeZone) > 255 || f.TimeZone == "Local" {
		return nil, nil, false
	}
	loc, err := time.LoadLocation(f.TimeZone)
	if err != nil {
		return nil, nil, false
	}
	from, valid = calendarBoundary(f.FromDate, loc, false)
	if !valid {
		return nil, nil, false
	}
	before, valid = calendarBoundary(f.ToDate, loc, true)
	return from, before, valid
}

// A civil day may start with a clock jump or a repeated midnight. Find the
// first instant at or after the boundary across adjacent UTC-offset periods.
func calendarBoundary(value string, loc *time.Location, nextDay bool) (*time.Time, bool) {
	if value == "" {
		return nil, true
	}
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, false
	}
	if nextDay {
		day = day.AddDate(0, 0, 1)
	}
	for start := day.Add(-24 * time.Hour); ; {
		local := start.In(loc)
		_, offset := local.Zone()
		_, end := local.ZoneBounds()
		instant := day.Add(-time.Duration(offset) * time.Second)
		if instant.Before(start) {
			instant = start
		}
		if end.IsZero() || instant.Before(end) || !end.After(start) {
			utc := instant.UTC()
			return &utc, utc.Year() >= 0 && utc.Year() <= 9999
		}
		start = end
	}
}

func readConversationFilters(q url.Values) conversationSearchFilters {
	return conversationSearchFilters{Sender: strings.TrimSpace(q.Get("sender")), FromDate: q.Get("fromDate"), ToDate: q.Get("toDate"), TimeZone: q.Get("timeZone")}
}

func matchesConversationFilters(m engineapi.Message, sender string, from, before *time.Time) bool {
	at := m.CreatedAt
	if m.TimelineAt != nil {
		at = *m.TimelineAt
	}
	return (from == nil || !at.Before(*from)) && (before == nil || at.Before(*before)) && (sender == "" || strings.Contains(cases.Fold().String(m.Sender.DisplayName), cases.Fold().String(sender)))
}
