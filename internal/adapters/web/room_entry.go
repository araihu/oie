package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"mellium.im/xmpp/jid"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) roomPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !validCSRF(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	account, address := r.Form.Get("accountId"), roomFormAddress(r.Form.Get("address"))
	if !validOperationKey(account) || address == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" || engine.JSON200.Id != r.Form.Get("engineId") {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	presentation := r.Form.Get("presentation")
	params := engineapi.PreviewRoomParams{Address: address}
	if presentation == "message" {
		discoveredServiceOnly := true
		params.DiscoveredServiceOnly = &discoveredServiceOnly
	}
	response, err := s.client.PreviewRoomWithResponse(ctx, account, &params)
	if err != nil || response.JSON200 == nil || !response.JSON200.State.Valid() {
		if response != nil && response.StatusCode() == http.StatusBadRequest {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !sameRoomAddress(response.JSON200.Address, address) {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	v := s.baseView(r)
	if localized, ok := s.catalogs[r.Form.Get("lang")]; ok {
		v.Text = localized
	}
	if presentation == "message" {
		if response.JSON200.State != "known" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = roomInvitationSummary(v.Text, *response.JSON200).Render(r.Context(), w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = roomPreviewContent(v.Text, *response.JSON200, address).Render(r.Context(), w)
}

func sameRoomAddress(value, requested string) bool {
	actual, err := jid.Parse(value)
	if err != nil || actual.Localpart() == "" || actual.Resourcepart() != "" {
		return false
	}
	want, err := jid.Parse(requested)
	return err == nil && actual.Equal(want)
}

func (s *Server) openRoom(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	account, key := r.Form.Get("accountId"), r.Form.Get("operationKey")
	address := roomFormAddress(r.Form.Get("address"))
	action := engineapi.OpenRoomAction(r.Form.Get("roomAction"))
	if !validOperationKey(account) || !validOperationKey(key) || address == "" || !action.Valid() {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	nickname, name := r.Form.Get("nickname"), r.Form.Get("name")
	response, err := s.client.OpenRoomWithResponse(ctx, account, &engineapi.OpenRoomParams{IdempotencyKey: key},
		engineapi.OpenRoom{Address: address, Action: action, Nickname: &nickname, Name: &name})
	if err != nil {
		return http.StatusServiceUnavailable, mutationResult{Message: text.RoomCommandUnknown}
	}
	c := response.JSON202
	if c == nil {
		result := problemResult(text, response.StatusCode(), response.Body)
		switch result.Code {
		case "state_conflict":
			result.Message, result.Rejected = text.RoomConflict, true
			var problem engineapi.Problem
			if json.Unmarshal(response.Body, &problem) == nil && problem.RoomAddressInUse != nil && *problem.RoomAddressInUse {
				result.Message = text.RoomAddressConflict
			}
		case "idempotency_conflict":
			result.NextKey = newOperationKey()
		}
		return response.StatusCode(), result
	}
	if c.Kind != "room" || c.AccountId != account || !validOperationKey(c.Id) || !sameRoomAddress(c.Address, address) {
		return http.StatusServiceUnavailable, mutationResult{Message: text.RoomCommandUnknown}
	}
	q := url.Values{"chat": {c.Id}, "account": {c.AccountId}, "open": {"room-session"}}
	if back, err := url.Parse(safeReturn(r.Form.Get("returnTo"))); err == nil {
		for _, name := range []string{"lang", "theme"} {
			if value := back.Query().Get(name); value != "" {
				q.Set(name, value)
			}
		}
	}
	return http.StatusOK, mutationResult{OK: true, Redirect: "/?" + q.Encode(), NextKey: newOperationKey()}
}
