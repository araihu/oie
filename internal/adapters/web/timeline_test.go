package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestTimelineHTMLIsScopedBoundedAndDirectional(t *testing.T) {
	messages := make([]engineapi.Message, 300)
	for i := range messages {
		messages[i] = engineapi.Message{Id: fmt.Sprintf("019a5df0-0000-7000-8000-%012d", 1000+299-i), RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat, OriginalText: fmt.Sprintf("Retained %d <script>", i), CreatedAt: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC).Add(time.Duration(i/3) * time.Millisecond), Sender: engineapi.Sender{Id: testAccount, DisplayName: "Sender"}, SendState: "received"}
	}
	slices.SortFunc(messages, messageCompare)
	position := messages[151].CreatedAt
	messages[151].CreatedAt = position.Add(-24 * time.Hour)
	messages[151].TimelineAt = &position
	messages[151].IsOwn = true
	hidden := messages[150]
	due := position.Add(time.Hour)
	hidden.ScheduledAt, hidden.SendState = &due, "queued"
	byID := map[string]engineapi.Message{}
	for _, m := range messages {
		byID[m.Id] = m
	}
	mode := "normal"
	releasedWhileListing := false
	movable := func(m engineapi.Message) engineapi.Message {
		if mode != "release_between_reads" || m.Id != messages[150].Id {
			return m
		}
		m.IsOwn, m.SendState = true, "queued"
		if releasedWhileListing {
			at := messages[len(messages)-1].CreatedAt.Add(time.Second)
			m.TimelineAt, m.SendState, m.RevisionId = &at, "submitted", testMessage
		}
		return m
	}
	lists, writes := 0, 0
	var limits []int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		send := func(status int, value any) { w.WriteHeader(status); _ = json.NewEncoder(w).Encode(value) }
		if r.Method != "GET" {
			writes++
			send(405, nil)
			return
		}
		switch r.URL.Path {
		case "/api/v1/engine":
			send(200, map[string]any{"id": testEngine, "role": "hosted"})
			return
		case "/api/v1/accounts/" + testAccount:
			send(200, engineapi.Account{Id: testAccount, Address: "alice@fixture"})
			return
		case "/api/v1/accounts/" + testAccount + "/connection":
			send(200, engineapi.Connection{AccountId: testAccount, State: "connected"})
			return
		case "/api/v1/conversations/" + testChat:
			if mode == "missing_conversation" {
				send(404, map[string]any{"status": 404})
				return
			}
			owner := testAccount
			if mode == "foreign_owner" {
				owner = testRevision
			}
			send(200, engineapi.Conversation{Id: testChat, AccountId: owner, Kind: "direct", Address: "bob@fixture"})
			return
		case "/api/v1/conversations/" + testChat + "/messages":
			lists++
			if mode == "release_between_reads" {
				releasedWhileListing = true
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			limits = append(limits, limit)
			if limit < 1 || limit > 100 {
				t.Errorf("unbounded list: %s", r.URL)
			}
			anchor, found := byID[r.URL.Query().Get("cursor")]
			if !found {
				send(404, nil)
				return
			}
			anchor = movable(anchor)
			direction := r.URL.Query().Get("direction")
			selected := []engineapi.Message{}
			for _, m := range messages {
				if mode == "hidden_schedule" && m.Id == hidden.Id {
					continue
				}
				m = movable(m)
				order := messageCompare(m, anchor)
				if direction == "newer" && order > 0 || direction != "newer" && order < 0 {
					selected = append(selected, m)
				}
			}
			if direction != "newer" {
				slices.Reverse(selected)
			}
			var next *string
			if len(selected) > limit {
				selected = selected[:limit]
				edge := selected[len(selected)-1].Id
				next = &edge
			}
			if direction == "newer" {
				slices.Reverse(selected)
			}
			if mode == "foreign_row" && len(selected) > 0 {
				selected[0].AccountId = testRevision
			}
			if mode == "bad_next" {
				bad := testRevision
				next = &bad
			}
			if mode == "unordered" && len(selected) > 1 {
				selected[0], selected[1] = selected[1], selected[0]
			}
			send(200, engineapi.MessagePage{Items: selected, NextCursor: next})
			return
		}
		if r.URL.Path == "/api/v1/conversations/"+testChat+"/messages/"+testRevision+"/context" {
			proof := testRevision
			if mode == "alias_wrong_proof" {
				proof = testMessage
			}
			send(200, engineapi.MessageContext{TargetId: messages[150].Id, RequestedTargetId: &proof, Items: []engineapi.Message{messages[150]}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/context") {
			parts := strings.Split(r.URL.Path, "/")
			target := parts[len(parts)-2]
			m, found := byID[target]
			if !found || mode == "hidden_schedule" && target == hidden.Id {
				send(404, nil)
				return
			}
			index := slices.IndexFunc(messages, func(item engineapi.Message) bool { return item.Id == m.Id })
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit != 50 {
				t.Errorf("context limit%d", limit)
			}
			start := max(0, index-24)
			end := min(len(messages), start+50)
			selected := append([]engineapi.Message(nil), messages[start:end]...)
			slices.Reverse(selected)
			send(200, engineapi.MessageContext{TargetId: m.Id, Items: selected, HasOlder: start > 0, HasNewer: end < len(messages)})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/messages/") {
			if mode == "anchor_unavailable" {
				send(503, map[string]any{"status": 503})
				return
			}
			if mode == "anchor_missing" {
				send(404, map[string]any{"status": 404})
				return
			}
			if strings.HasPrefix(mode, "alias_") && r.URL.Path == "/api/v1/messages/"+testRevision {
				send(200, messages[150])
				return
			}
			m, ok := byID[strings.TrimPrefix(r.URL.Path, "/api/v1/messages/")]
			if !ok {
				send(404, nil)
				return
			}
			if mode == "hidden_schedule" && m.Id == hidden.Id {
				m = hidden
			}
			send(200, movable(m))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/previews") || r.URL.Path == "/api/v1/shared-content" {
			if count := len(r.URL.Query()["messageId"]); count > 100 {
				t.Errorf("metadata batch %d", count)
			}
			send(200, map[string]any{"items": []any{}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/message-stars") {
			stars := []engineapi.MessageStar{}
			for _, id := range r.URL.Query()["messageId"] {
				stars = append(stars, engineapi.MessageStar{MessageId: id, AccountId: testAccount, ConversationId: testChat})
			}
			send(200, map[string]any{"items": stars})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/avatars") {
			send(200, map[string]any{"items": []any{}})
			return
		}
		if strings.Contains(r.URL.Path, "details") || strings.Contains(r.URL.Path, "history") {
			t.Errorf("timeline triggered unrelated protocol/history read %s", r.URL)
		}
		send(404, map[string]any{"status": 404})
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "lang": {"pt-BR"}}
	request := func(params url.Values) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", "/ui/timeline?"+params.Encode(), nil))
		return w
	}
	for _, direction := range []string{"older", "newer"} {
		t.Run(direction, func(t *testing.T) {
			q := timelineQueryClone(base)
			q.Set("direction", direction)
			q.Set("cursor", messages[150].Id)
			lists = 0
			w := request(q)
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			html := w.Body.String()
			boundary := messages[149]
			if direction == "newer" {
				boundary = messages[151]
			}
			if count := strings.Count(html, `data-message-source=`); count != 50 {
				t.Fatalf("rendered %d rows", count)
			}
			if !strings.Contains(html, `id="oie-star-data"`) || !strings.Contains(html, `id="live-timeline"`) || !strings.Contains(html, `data-created-ms="`+strconv.FormatInt(messageTimelineTime(boundary).UnixMilli(), 10)+`"`) {
				t.Fatal("missing authoritative metadata or exact millisecond boundary")
			}
			if direction == "newer" && (!strings.Contains(html, `data-timeline-release`) || strings.Contains(html, `data-created-ms="`+strconv.FormatInt(boundary.CreatedAt.UnixMilli(), 10)+`"`)) {
				t.Fatal("released schedule rendered its acceptance position")
			}
			if strings.Contains(html, "<script>") {
				t.Fatal("source escaped incorrectly")
			}
			if lists != 1 || limits[len(limits)-1] != 50 {
				t.Fatalf("page fanout=%d limits=%v", lists, limits)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private timeline cached")
			}
		})
	}
	t.Run("hidden schedule cannot be reintroduced by a stale window", func(t *testing.T) {
		mode = "hidden_schedule"
		defer func() { mode = "normal" }()
		q := timelineQueryClone(base)
		q.Set("mode", "context")
		q.Set("cursor", hidden.Id)
		if w := request(q); w.Code != 404 {
			t.Fatalf("hidden context status %d", w.Code)
		}
		for _, low := range []int{148, 150} {
			q := timelineQueryClone(base)
			q.Set("mode", "window")
			q.Set("oldest", messages[low].Id)
			q.Set("newest", hidden.Id)
			w := request(q)
			html := w.Body.String()
			if w.Code != 200 || strings.Contains(html, `data-message-id="`+hidden.Id+`"`) || strings.Count(html, `data-message-source=`) != 150-low {
				t.Fatalf("hidden anchor in window %d: status=%d rows=%d", low, w.Code, strings.Count(html, `data-message-source=`))
			}
			if !strings.Contains(html, `data-hidden-newest="true"`) || !strings.Contains(html, `data-hidden-oldest="`+strconv.FormatBool(low == 150)+`"`) || !strings.Contains(html, `data-complete="true"`) {
				t.Fatalf("hidden boundary omitted in window %d", low)
			}
		}
	})
	for _, direction := range []string{"older", "newer"} {
		t.Run("release between anchor and page reads "+direction, func(t *testing.T) {
			mode = "release_between_reads"
			defer func() { mode = "normal" }()
			releasedWhileListing = false
			lists = 0
			q := timelineQueryClone(base)
			q.Set("direction", direction)
			q.Set("cursor", messages[150].Id)
			w := request(q)
			if w.Code != 200 || lists != 2 || !strings.Contains(w.Body.String(), `data-timeline-release-boundary="`+messages[150].Id+`"`) {
				t.Fatalf("release did not revalidate the page: status=%d lists=%d", w.Code, lists)
			}
		})
	}
	t.Run("release during a window read invalidates the whole interval", func(t *testing.T) {
		mode = "release_between_reads"
		defer func() { mode = "normal" }()
		releasedWhileListing = false
		q := timelineQueryClone(base)
		q.Set("mode", "window")
		q.Set("oldest", messages[150].Id)
		q.Set("newest", messages[170].Id)
		w := request(q)
		html := w.Body.String()
		if w.Code != 200 || !strings.Contains(html, `data-timeline-release-boundary="`+messages[150].Id+`"`) || !strings.Contains(html, `data-complete="false"`) || !strings.Contains(html, `data-oldest=""`) {
			t.Fatalf("window used inconsistent boundaries: status=%d", w.Code)
		}
	})
	t.Run("released boundary proves an obsolete window", func(t *testing.T) {
		q := timelineQueryClone(base)
		q.Set("mode", "window")
		q.Set("oldest", messages[151].Id)
		q.Set("newest", messages[150].Id)
		w := request(q)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `data-timeline-release-boundary="`+messages[151].Id+`"`) || !strings.Contains(w.Body.String(), `data-oldest=""`) {
			t.Fatalf("obsolete window omitted its released boundary: %d", w.Code)
		}
	})
	for _, tc := range []struct {
		name      string
		low, high int
		complete  string
	}{{"full retained interval", 50, 249, "true"}, {"bounded larger interval", 0, 299, "false"}} {
		t.Run(tc.name, func(t *testing.T) {
			q := timelineQueryClone(base)
			q.Set("mode", "window")
			q.Set("oldest", messages[tc.low].Id)
			q.Set("newest", messages[tc.high].Id)
			lists = 0
			w := request(q)
			if w.Code != 200 {
				t.Fatalf("window status=%d", w.Code)
			}
			if strings.Count(w.Body.String(), `data-message-source=`) != 200 || lists != 2 {
				t.Fatalf("unbounded window rows/calls: %d/%d", strings.Count(w.Body.String(), `data-message-source=`), lists)
			}
			if !strings.Contains(w.Body.String(), `data-complete="`+tc.complete+`"`) {
				t.Fatal("incorrect coverage proof")
			}
		})
	}
	for _, tc := range []struct {
		mode   string
		status int
	}{{"anchor_unavailable", 503}, {"anchor_missing", 404}, {"missing_conversation", 404}, {"foreign_owner", 404}, {"foreign_row", 503}, {"bad_next", 503}, {"unordered", 503}} {
		t.Run(tc.mode, func(t *testing.T) {
			mode = tc.mode
			q := timelineQueryClone(base)
			q.Set("cursor", messages[150].Id)
			w := request(q)
			if w.Code != tc.status {
				t.Fatalf("status %d want%d", w.Code, tc.status)
			}
		})
	}
	for _, tc := range []struct {
		mode   string
		status int
	}{{"alias_proven", 200}, {"alias_wrong_proof", 503}} {
		t.Run(tc.mode, func(t *testing.T) {
			mode = tc.mode
			q := timelineQueryClone(base)
			q.Set("cursor", testRevision)
			w := request(q)
			if w.Code != tc.status {
				t.Fatalf("alias status%d want%d", w.Code, tc.status)
			}
			if w.Code == 200 {
				html := w.Body.String()
				if !strings.Contains(html, `data-cursor="`+testRevision+`"`) || !strings.Contains(html, `data-canonical-cursor="`+messages[150].Id+`"`) || !strings.Contains(html, `data-timeline-alias="`+testRevision+`"`) {
					t.Fatal("canonical alias proof/anchor missing")
				}
			}
		})
	}
	mode = "alias_proven"
	q := timelineQueryClone(base)
	q.Set("mode", "context")
	q.Set("cursor", testRevision)
	w := request(q)
	if w.Code != 200 {
		t.Fatalf("context recovery status%d", w.Code)
	}
	main := strings.Split(w.Body.String(), "<template data-timeline-alias")[0]
	if strings.Count(main, `data-message-source=`) != 50 || !strings.Contains(main, `data-canonical-cursor="`+messages[150].Id+`"`) {
		t.Fatal("context recovery is not bounded/canonical")
	}
	if writes != 0 {
		t.Fatalf("paging caused%d writes", writes)
	}
}

func timelineQueryClone(values url.Values) url.Values {
	out := url.Values{}
	for key, entries := range values {
		out[key] = append([]string(nil), entries...)
	}
	return out
}
