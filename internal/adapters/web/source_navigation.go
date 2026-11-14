package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func sourceFragmentRequest(r *http.Request, v *view) bool {
	fragment := r.Header.Get("X-Oie-Fragment")
	return r.URL.Path == "/" && r.Header.Get("HX-Request") == "true" &&
		(fragment == "source" || fragment == "source-pane") && v.Section == "chats" &&
		!v.InboxOnly && !v.GlobalMessageSearch && v.InfoView == "" && v.AccountPage == "" && v.ManagedAccount == "" &&
		r.URL.Query().Get("open") == "" && r.URL.Query().Get("editSchedule") == "" &&
		validOperationKey(v.AccountFilter) && validOperationKey(v.ChatID) && validOperationKey(v.FocusMessage)
}

// Older engines and missing sources fall through to the normal route, including
// its context-missing recovery. An unsuccessful aggregate never changes the view.
func (s *Server) loadSourceNavigation(ctx context.Context, r *http.Request, v *view, paneOnly bool) bool {
	response, err := s.client.GetMessageSourceContextWithResponse(ctx, v.ChatID, v.FocusMessage)
	if err != nil {
		v.Unavailable = true
		return true
	}
	if response.StatusCode() == http.StatusNotFound || response.StatusCode() == http.StatusMethodNotAllowed {
		return false
	}
	if response.JSON200 == nil {
		v.Unavailable = true
		return true
	}
	source := response.JSON200
	owner := source.Conversation
	if owner.Id != v.ChatID || owner.AccountId != v.AccountFilter || owner.Request != nil ||
		!validOperationKey(source.Engine.Id) || !validTimelineMessages(source.Context.Items, owner.AccountId, owner.Id, sourceContextPageSize) ||
		!v.applyMessageContext(source.Context, owner.AccountId) {
		v.Unavailable = true
		return true
	}
	v.EngineID, v.EventCursor, v.Role = source.Engine.Id, source.Engine.EventCursor, source.Engine.Role
	v.CanResetData = source.Engine.CanResetData != nil && *source.Engine.CanResetData
	v.Selected = &owner
	v.applyConversationDetails(source.Details)
	if v.ConversationInfo != nil {
		v.Connections[owner.AccountId] = connectionView{Known: true, Value: v.ConversationInfo.AccountConnection}
	}
	if source.Capabilities != nil && source.Capabilities.AccountId == owner.AccountId {
		v.MessagingCapabilities = source.Capabilities
	}
	if source.EditTarget != nil && validEditTarget(*source.EditTarget, owner.AccountId, owner.Id, "") {
		v.LatestEditTarget = source.EditTarget
	}
	if owner.Kind == "room" {
		if source.RoomSession != nil && validRoomSession(*source.RoomSession, owner.Id, owner.AccountId) {
			v.RoomSession = source.RoomSession
		}
		if source.RoomArchive != nil && source.RoomArchive.ConversationId == owner.Id {
			v.RoomArchive = source.RoomArchive
		}
	}
	v.ReuseInbox = !paneOnly && r.Header.Get("X-Oie-Inbox-Scope") == v.inboxCacheScope()

	// The account list, inbox and avatars are independent reads. Keep them off
	// each other's critical path and publish the view once all are available.
	var reads sync.WaitGroup
	var accountsOK, inboxOK bool
	limit := 100
	reads.Go(func() {
		accounts, err := s.client.ListAccountsWithResponse(ctx, &engineapi.ListAccountsParams{Limit: &limit})
		if err == nil && accounts.JSON200 != nil {
			v.Accounts = accounts.JSON200.Items
			accountsOK = v.account(owner.AccountId).Address != ""
		}
	})
	reads.Go(func() { s.loadAvatars(ctx, v, owner.AccountId) })
	if !paneOnly && !v.ReuseInbox {
		reads.Go(func() {
			// loadInbox mutates Unavailable on failure. Keep that state local
			// while the other reads are in flight.
			inbox := view{AccountFilter: v.AccountFilter, Section: v.Section, Filter: v.Filter, Query: v.Query}
			inboxOK = s.loadInbox(ctx, &inbox)
			v.Chats = inbox.Chats
		})
	} else {
		inboxOK = true
	}
	reads.Wait()
	if !accountsOK || !inboxOK {
		v.Unavailable = true
		return true
	}
	// Account rows already carry connection state. The selected conversation
	// supplies the full connection evidence used by its own actions.
	for _, account := range v.Accounts {
		if _, known := v.Connections[account.Id]; !known {
			state := engineapi.ConnectionState(account.ConnectionState)
			if state.Valid() {
				v.Connections[account.Id] = connectionView{Known: true, Value: engineapi.Connection{AccountId: account.Id, State: state}}
			}
		}
	}

	v.TimelineOlder, v.TimelineNewer = v.ContextHasOlder, v.ContextHasNewer
	v.setTimelineMessages(source.Context.Items, nil)
	v.applySourceMessageMetadata(source)
	v.formatTimelineMessages(ctx)
	v.CompactSource = true
	return true
}

func encodeSourceFragment(w http.ResponseWriter, r *http.Request, body bytes.Buffer) bytes.Buffer {
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsSourceGzip(r.Header.Values("Accept-Encoding")) {
		return body
	}
	var compressed bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if _, err := writer.Write(body.Bytes()); err != nil {
		return body
	}
	if err := writer.Close(); err != nil {
		return body
	}
	w.Header().Set("Content-Encoding", "gzip")
	return compressed
}

func acceptsSourceGzip(headers []string) bool {
	wildcard := false
	for _, header := range headers {
		for entry := range strings.SplitSeq(header, ",") {
			coding, parameter, hasParameter := strings.Cut(entry, ";")
			coding = strings.ToLower(strings.TrimSpace(coding))
			if coding != "gzip" && coding != "*" {
				continue
			}
			allowed := true
			if hasParameter {
				key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				allowed = ok && strings.EqualFold(strings.TrimSpace(key), "q") && err == nil && q > 0 && q <= 1
			}
			if coding == "gzip" {
				return allowed
			}
			wildcard = allowed
		}
	}
	return wildcard
}

func (v *view) applySourceMessageMetadata(source *engineapi.MessageSourceContext) {
	if source.Previews != nil {
		v.applyMessagePreviews(*source.Previews)
	}
	visible := make(map[string]messageView, len(v.Messages))
	for _, m := range v.Messages {
		visible[m.Id] = m
	}
	if source.Videos != nil {
		videos := map[string][]engineapi.SharedContentItem{}
		for _, item := range *source.Videos {
			m, found := visible[item.Message.Id]
			if found && validMessageVideo(item, m, v.Selected) && len(videos[m.Id]) < 3 {
				videos[m.Id] = append(videos[m.Id], item)
			}
		}
		v.applyMessageVideos(videos)
	}
	if source.Stars != nil {
		stars := make(map[string]engineapi.MessageStar, len(visible))
		for _, item := range *source.Stars {
			if _, found := visible[item.MessageId]; found && validTimelineStar(item, v.Selected.AccountId, v.ChatID) {
				stars[item.MessageId] = item
			}
		}
		if len(stars) == len(visible) {
			v.Stars, v.StarsKnown = stars, true
		}
	}
	if source.Reactions != nil {
		reactions := make(map[string]engineapi.MessageReactions, len(visible))
		for _, item := range *source.Reactions {
			if _, found := visible[item.MessageId]; found && validReactions(item, v.Selected.AccountId, v.ChatID, item.MessageId) {
				reactions[item.MessageId] = item
			}
		}
		if len(reactions) == len(visible) {
			v.Reactions = reactions
		}
	}
	for i := range v.Messages {
		v.Messages[i].PresentationKnown = v.PreviewsKnown
		v.Messages[i].VideosKnown = v.MessageVideos != nil
	}
}
