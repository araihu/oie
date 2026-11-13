package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func messageTimelineTime(m engineapi.Message) time.Time {
	if m.TimelineAt != nil {
		return *m.TimelineAt
	}
	return m.CreatedAt
}

func timelineHiddenSchedule(m engineapi.Message) bool {
	if m.TimelineAt != nil {
		return m.SendState == "cancelled"
	}
	return m.ScheduledAt != nil && (m.SendState == "queued" || m.SendState == "dispatching" || m.SendState == "cancelled")
}

func timelinePositionMutable(m engineapi.Message) bool {
	return m.IsOwn && (m.SendState == "queued" || m.SendState == "dispatching")
}

func messageCompare(a, b engineapi.Message) int {
	if at, bt := messageTimelineTime(a).UnixMilli(), messageTimelineTime(b).UnixMilli(); at < bt {
		return -1
	} else if at > bt {
		return 1
	}
	if a.Id < b.Id {
		return -1
	}
	if a.Id > b.Id {
		return 1
	}
	return 0
}

func validTimelineMessages(items []engineapi.Message, account, conversation string, limit int) bool {
	if len(items) > limit {
		return false
	}
	seen := map[string]bool{}
	for i, m := range items {
		if !validOperationKey(m.Id) || !validOperationKey(m.RevisionId) || m.AccountId != account || m.ConversationId != conversation || m.CreatedAt.IsZero() || seen[m.Id] {
			return false
		}
		seen[m.Id] = true
		if i > 0 && messageCompare(items[i-1], m) <= 0 {
			return false
		}
	}
	return true
}

func validTimelinePage(page engineapi.MessagePage, account, conversation, direction string, anchor *engineapi.Message, limit int) bool {
	if !validTimelineMessages(page.Items, account, conversation, limit) {
		return false
	}
	if page.NextCursor != nil {
		if len(page.Items) == 0 || !validOperationKey(*page.NextCursor) {
			return false
		}
		edge := page.Items[len(page.Items)-1].Id
		if direction == "newer" {
			edge = page.Items[0].Id
		}
		if *page.NextCursor != edge {
			return false
		}
	}
	if anchor != nil {
		for _, m := range page.Items {
			order := messageCompare(m, *anchor)
			if direction == "newer" && order <= 0 || direction != "newer" && order >= 0 {
				return false
			}
		}
	}
	return true
}

func (v view) timelineEdge(direction string) string {
	if len(v.Messages) == 0 {
		return ""
	}
	if direction == "newer" {
		return v.Messages[len(v.Messages)-1].Id
	}
	return v.Messages[0].Id
}
func (v view) timelineEdgeMS(direction string) string {
	if len(v.Messages) == 0 {
		return ""
	}
	index := 0
	if direction == "newer" {
		index = len(v.Messages) - 1
	}
	return strconv.FormatInt(messageTimelineTime(v.Messages[index].Message).UnixMilli(), 10)
}
func (v view) timelinePageHref(direction string) string {
	return v.href("chat", v.ChatID, "account", selectedAccountID(v), "message", "", "timelineCursor", v.timelineEdge(direction), "timelineDirection", direction, "lang", v.Locale, "theme", v.Theme)
}
func timelineInitialParams(v view) *engineapi.ListMessagesParams {
	limit := 50
	p := &engineapi.ListMessagesParams{Limit: &limit}
	if validOperationKey(v.TimelineCursor) {
		p.Cursor = &v.TimelineCursor
		if v.TimelineDirection == "newer" {
			direction := engineapi.ListMessagesParamsDirectionNewer
			p.Direction = &direction
		}
	}
	return p
}
func (v view) messageVersion(m messageView) string {
	data, _ := json.Marshal([]any{m.Message, m.Presentation, m.PresentationKnown, v.messageVideos(m.Id), v.Reactions[m.Id]})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (m messageView) mentionsYou() bool {
	return !m.IsOwn && m.MentionsYou != nil && *m.MentionsYou
}

func (v view) timelineConversationRevision() string {
	if v.Selected == nil {
		return ""
	}
	return v.Selected.RevisionId
}

func (v view) timelineUnread() int {
	if v.Selected == nil {
		return 0
	}
	return v.Selected.UnreadCount
}

func (s *Server) formatTimeline(ctx context.Context, v *view, items []engineapi.Message, extras map[string]*mediaView) {
	slices.Reverse(items)
	for _, m := range items {
		v.Messages = append(v.Messages, messageView{Message: m, Attachment: extras[m.Id], Presentation: m.OriginalText})
	}
	// Each enrichment request stays within the public batch contract.
	for start := 0; start < len(v.Messages); start += 50 {
		part := *v
		part.Messages = v.Messages[start:min(start+50, len(v.Messages))]
		part.MessageVideos = nil
		s.loadMessagePreviews(ctx, &part)
		s.loadMessageVideos(ctx, &part)
		v.PreviewsKnown = v.PreviewsKnown || part.PreviewsKnown
		for i := range part.Messages {
			part.Messages[i].PresentationKnown = part.PreviewsKnown
			part.Messages[i].VideosKnown = part.MessageVideos != nil || v.Preview
		}
		if v.MessageVideos == nil {
			v.MessageVideos = map[string][]engineapi.SharedContentItem{}
		}
		for id, entries := range part.MessageVideos {
			v.MessageVideos[id] = entries
		}
	}
	s.loadMessageStars(ctx, v)
	s.loadReactions(ctx, v)
	for i := range v.Messages {
		m := &v.Messages[i]
		m.Presentation = videoPresentation(imagePresentation(m.displayBody(), v.MessagePreviews[m.Id]), v.messageVideos(m.Id))
		m.Formatted, m.RoomInvitations = formattedMessageWithInvitations(ctx, m.Presentation, invitationScope{m.AccountId, m.ConversationId, v.Locale})
	}
}

// timelinePage is an internal, same-origin HTML representation of canonical
// retained messages. It never calls protocol discovery or receipt operations.
func (s *Server) timelinePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	allowed := map[string]bool{"engineId": true, "accountId": true, "conversationId": true, "direction": true, "cursor": true, "mode": true, "oldest": true, "newest": true, "lang": true, "theme": true}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			w.WriteHeader(400)
			return
		}
	}
	engine, account, conversation := q.Get("engineId"), q.Get("accountId"), q.Get("conversationId")
	mode, direction, cursor := q.Get("mode"), q.Get("direction"), q.Get("cursor")
	if mode == "" {
		mode = "page"
	}
	if direction == "" {
		direction = "older"
	}
	if !validOperationKey(engine) || !validOperationKey(account) || !validOperationKey(conversation) || mode != "page" && mode != "window" && mode != "context" || direction != "older" && direction != "newer" || (mode == "page" || mode == "context") && !validOperationKey(cursor) || mode == "window" && (!validOperationKey(q.Get("oldest")) || !validOperationKey(q.Get("newest"))) {
		w.WriteHeader(400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	v := s.baseView(r)
	v.CSRF = csrfToken(w, r)
	v.ChatID = conversation
	v.AccountFilter = account
	v.TimelineMode = mode
	v.TimelineDirection = direction
	v.TimelineCursor = cursor
	v.TimelineRequestOldest = q.Get("oldest")
	v.TimelineRequestNewest = q.Get("newest")
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		w.WriteHeader(503)
		return
	}
	if current.JSON200.Id != engine {
		w.WriteHeader(409)
		return
	}
	v.EngineID = engine
	v.Preview = current.HTTPResponse.Header.Get("X-Oie-Mock") == "true"
	owner, err := s.client.GetConversationWithResponse(ctx, conversation)
	if err != nil || owner.JSON200 == nil {
		status := 503
		if err == nil && owner != nil && owner.StatusCode() == 404 {
			status = 404
		}
		w.WriteHeader(status)
		return
	}
	if owner.JSON200.Id != conversation || owner.JSON200.AccountId != account {
		w.WriteHeader(404)
		return
	}
	v.Selected = owner.JSON200
	a, err := s.client.GetAccountWithResponse(ctx, account)
	if err != nil || a.JSON200 == nil || a.JSON200.Id != account {
		w.WriteHeader(503)
		return
	}
	v.Accounts = []engineapi.Account{*a.JSON200}
	connection, err := s.client.GetAccountConnectionWithResponse(ctx, account)
	if err == nil && connection.JSON200 != nil && connection.JSON200.AccountId == account {
		v.Connections[account] = connectionView{Known: true, Value: *connection.JSON200}
	}
	s.loadMessaging(ctx, &v)
	s.loadAvatars(ctx, &v, account)
	v.TimelineAliases = map[string]messageView{}
	anchor := func(id string) (*engineapi.Message, int) {
		response, e := s.client.GetMessageWithResponse(ctx, id)
		if e != nil {
			return nil, 503
		}
		if response.JSON200 == nil {
			if response.StatusCode() == 404 {
				return nil, 404
			}
			return nil, 503
		}
		m := response.JSON200
		if m.AccountId != account || m.ConversationId != conversation {
			return nil, 404
		}
		if !validOperationKey(m.Id) || !validOperationKey(m.RevisionId) || m.CreatedAt.IsZero() {
			return nil, 503
		}
		if m.Id != id {
			var status int
			m, status = s.retainedReplySource(ctx, conversation, account, id)
			if m == nil {
				return nil, status
			}
		}
		delete(v.TimelineAliases, id)
		if !timelineHiddenSchedule(*m) && (m.Id != id || mode != "context" && m.TimelineAt != nil) {
			formatted, rooms := formattedMessageWithInvitations(ctx, m.OriginalText, invitationScope{m.AccountId, m.ConversationId, v.Locale})
			v.TimelineAliases[id] = messageView{Message: *m, Presentation: m.OriginalText, Formatted: formatted, RoomInvitations: rooms}
		}
		return m, 200
	}
	recheck := func(id string, previous *engineapi.Message) (*engineapi.Message, int) {
		if !timelinePositionMutable(*previous) {
			return previous, http.StatusOK
		}
		return anchor(id)
	}

	var items []engineapi.Message
	switch mode {
	case "context":
		bound, status := anchor(cursor)
		if bound == nil {
			w.WriteHeader(status)
			return
		}
		limit := 50
		response, e := s.client.GetMessageContextWithResponse(ctx, conversation, bound.Id, &engineapi.GetMessageContextParams{Limit: &limit})
		if e == nil && response.StatusCode() == http.StatusNotFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if e != nil || response.JSON200 == nil || !validContextTarget(*response.JSON200, bound.Id, conversation, account) || !validTimelineMessages(response.JSON200.Items, account, conversation, 50) {
			w.WriteHeader(503)
			return
		}
		items = response.JSON200.Items
		v.FocusMessage = response.JSON200.TargetId
		v.TimelineCanonicalCursor = response.JSON200.TargetId
		v.TimelineOlder = response.JSON200.HasOlder
		v.TimelineNewer = response.JSON200.HasNewer
	case "page":
		bound, status := anchor(cursor)
		if bound == nil {
			w.WriteHeader(status)
			return
		}
		limit := 50
		dir := engineapi.ListMessagesParamsDirection(direction)
		list := func(at *engineapi.Message) (*engineapi.ListMessagesResponse, error) {
			return s.client.ListMessagesWithResponse(ctx, conversation, &engineapi.ListMessagesParams{Cursor: &at.Id, Direction: &dir, Limit: &limit})
		}
		response, e := list(bound)
		latest, status := recheck(cursor, bound)
		if latest == nil {
			w.WriteHeader(status)
			return
		}
		if messageCompare(*latest, *bound) != 0 {
			// The page read can race the first anchor read. Retry once against
			// the current position and include its proof in the fragment.
			bound = latest
			response, e = list(bound)
		}
		v.TimelineCanonicalCursor = bound.Id
		if e != nil || response.JSON200 == nil || !validTimelinePage(*response.JSON200, account, conversation, direction, bound, limit) {
			w.WriteHeader(503)
			return
		}
		items = response.JSON200.Items
		v.TimelineOlder = direction == "newer" || response.JSON200.NextCursor != nil
		v.TimelineNewer = direction == "older" || response.JSON200.NextCursor != nil
	default:
		low, status := anchor(v.TimelineRequestOldest)
		if low == nil {
			w.WriteHeader(status)
			return
		}
		high := low
		if v.TimelineRequestOldest != v.TimelineRequestNewest {
			high, status = anchor(v.TimelineRequestNewest)
			if high == nil {
				w.WriteHeader(status)
				return
			}
		}
		v.TimelineHiddenOldest = timelineHiddenSchedule(*low)
		v.TimelineHiddenNewest = timelineHiddenSchedule(*high)
		if messageCompare(*low, *high) > 0 {
			if low.TimelineAt == nil && high.TimelineAt == nil {
				w.WriteHeader(400)
				return
			}
			// A released schedule can outgrow a previously mounted interval. The
			// anchor proof lets the client reconcile before asking for rows again.
			break
		}
		if !v.TimelineHiddenNewest {
			items = []engineapi.Message{*high}
		}
		v.TimelineOlder = true
		v.TimelineNewer = true
		if low.Id == high.Id {
			v.TimelineComplete = true
		} else {
			valid := true
			next := high.Id
			bound := high
			dir := engineapi.ListMessagesParamsDirectionOlder
			for page := 0; page < 2; page++ {
				limit := min(100, 200-len(items))
				response, e := s.client.ListMessagesWithResponse(ctx, conversation, &engineapi.ListMessagesParams{Cursor: &next, Direction: &dir, Limit: &limit})
				if e != nil || response.JSON200 == nil || !validTimelinePage(*response.JSON200, account, conversation, "older", bound, limit) {
					valid = false
					break
				}
				for _, m := range response.JSON200.Items {
					if messageCompare(m, *low) < 0 {
						v.TimelineComplete = true
						break
					}
					if m.Id == low.Id && v.TimelineHiddenOldest {
						v.TimelineComplete = true
						break
					}
					items = append(items, m)
					if m.Id == low.Id {
						v.TimelineComplete = true
						break
					}
				}
				if v.TimelineComplete || response.JSON200.NextCursor == nil {
					v.TimelineComplete = true
					break
				}
				if len(items) == 200 {
					break
				}
				next = *response.JSON200.NextCursor
				last := response.JSON200.Items[len(response.JSON200.Items)-1]
				bound = &last
			}
			latestLow, status := recheck(v.TimelineRequestOldest, low)
			if latestLow == nil {
				w.WriteHeader(status)
				return
			}
			latestHigh, status := recheck(v.TimelineRequestNewest, high)
			if latestHigh == nil {
				w.WriteHeader(status)
				return
			}
			if messageCompare(*latestLow, *low) != 0 || messageCompare(*latestHigh, *high) != 0 ||
				timelineHiddenSchedule(*latestLow) != v.TimelineHiddenOldest || timelineHiddenSchedule(*latestHigh) != v.TimelineHiddenNewest {
				v.TimelineHiddenOldest = timelineHiddenSchedule(*latestLow)
				v.TimelineHiddenNewest = timelineHiddenSchedule(*latestHigh)
				v.TimelineComplete = false
				items = nil
				break
			}
			if !valid {
				w.WriteHeader(503)
				return
			}
		}
	}
	s.formatTimeline(ctx, &v, items, nil)
	for requested, alias := range v.TimelineAliases {
		part := v
		part.Messages = nil
		part.MessagePreviews = map[string][]engineapi.MessagePreview{}
		part.MessageVideos = nil
		part.Stars = nil
		part.Reactions = nil
		s.formatTimeline(ctx, &part, []engineapi.Message{alias.Message}, nil)
		v.TimelineAliases[requested] = part.Messages[0]
		for id, entries := range part.MessagePreviews {
			v.MessagePreviews[id] = entries
		}
		for id, entries := range part.MessageVideos {
			v.MessageVideos[id] = entries
		}
		for id, state := range part.Reactions {
			if v.Reactions == nil {
				v.Reactions = map[string]engineapi.MessageReactions{}
			}
			v.Reactions[id] = state
		}
		for id, star := range part.Stars {
			if v.Stars == nil {
				v.Stars = map[string]engineapi.MessageStar{}
			}
			v.Stars[id] = star
		}
	}
	var output bytes.Buffer
	if err := timelineFragment(v).Render(presentationContext(ctx, v), &output); err != nil {
		w.WriteHeader(503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = output.WriteTo(w)
}
