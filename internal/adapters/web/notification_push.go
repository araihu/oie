package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/araihu/oie/internal/adapters/browserpush"
	"github.com/araihu/oie/internal/adapters/hostauth"
	"github.com/araihu/oie/internal/adapters/web/bffapi"
	"github.com/google/uuid"
)

const browserPushPath = "/ui/notification-push"
const browserPushForegroundPath = browserPushPath + "/foreground"

func WithBrowserPush(auth *hostauth.Auth, service *browserpush.Service) Option {
	return func(s *Server) {
		s.pushAuth = auth
		s.pushService = service
	}
}

func (s *Server) notificationPush(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	session := hostauth.BrowserSessionID(r)
	if s.pushAuth == nil || session == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && !validCSRF(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == browserPushForegroundPath {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request bffapi.BrowserPushForeground
		if !readPushJSON(w, r, &request) || !validOperationKey(request.EngineId.String()) || !validOperationKey(request.MessageId.String()) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !s.pushService.ClaimForeground(session, request.EngineId.String(), request.MessageId.String()) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ctx := r.Context()
	if r.Method == http.MethodDelete {
		if err := s.pushAuth.SaveBrowserPush(ctx, session, nil, false); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil || !validOperationKey(engine.JSON200.Id) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	id := engine.JSON200.Id
	if r.Method == http.MethodGet {
		key, lang, err := s.pushAuth.BrowserPushStatus(ctx, session, id)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bffapi.BrowserPushStatus{EngineId: uuid.MustParse(id), PublicKey: key, Enabled: lang != "", Lang: &lang})
		return
	}
	var request bffapi.BrowserPushSubscription
	if !readPushJSON(w, r, &request) || !request.Lang.Valid() || !request.RoomSound.Valid() ||
		!validOperationKey(request.EngineId.String()) || len(request.MutedConversations) > 1024 ||
		!browserpush.ValidSubscription(request.Endpoint, request.Keys.Auth, request.Keys.P256dh) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.EngineId.String() != id {
		w.WriteHeader(http.StatusConflict)
		return
	}
	mutes := make([]string, 0, len(request.MutedConversations))
	for _, conversation := range request.MutedConversations {
		if !validOperationKey(conversation.String()) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mutes = append(mutes, conversation.String())
	}
	slices.Sort(mutes)
	err = s.pushAuth.SaveBrowserPush(ctx, session, &hostauth.PushSubscription{
		Endpoint: request.Endpoint, Auth: request.Keys.Auth, PublicKey: request.Keys.P256dh,
		EngineID: id, Language: string(request.Lang), MutedConversations: slices.Compact(mutes),
		Sound: request.Sound, DirectSound: request.DirectSound, RoomSound: string(request.RoomSound), ArchivedSound: request.ArchivedSound,
	}, request.Existing != nil && *request.Existing)
	if errors.Is(err, hostauth.ErrPushChanged) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readPushJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst) == nil && decoder.Decode(new(any)) == io.EOF
}
