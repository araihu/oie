package web

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// Match the room management upload limit in the engine API.
func (s *Server) roomManagementFragment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	id, account, messageID := q.Get("chat"), q.Get("account"), q.Get("message")
	if !validOperationKey(id) || !validOperationKey(account) || messageID != "" && !validOperationKey(messageID) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	v := s.baseView(r)
	v.CSRF = csrfToken(w, r)
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil || engine.JSON200.Id != q.Get("engine") || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	v.EngineID = engine.JSON200.Id
	owner, err := s.client.GetConversationWithResponse(ctx, id)
	if err != nil || owner.JSON200 == nil || owner.JSON200.AccountId != account || owner.JSON200.Kind != "room" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	v.Selected = owner.JSON200
	invite := q.Get("invite") == "true"
	profile := q.Get("profile")
	bannedProfile := strings.HasPrefix(profile, "room-banned-")
	bansOnly := q.Get("affiliation") == "outcast"
	participantsOnly := q.Get("participants") == "true"
	if (profile != "" || bansOnly || participantsOnly) && (messageID != "" || invite) ||
		participantsOnly && (bansOnly || profile != "") ||
		profile != "" && (bansOnly || !bannedProfile && !strings.HasPrefix(profile, "room-participant-")) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if messageID == "" && !invite && profile == "" && !bansOnly && !participantsOnly {
		s.loadConversationDetails(ctx, &v)
	}
	var params engineapi.GetRoomManagementParams
	if bansOnly || bannedProfile {
		affiliation := engineapi.RoomAffiliationOutcast
		params.Affiliation = &affiliation
	}
	if messageID != "" {
		params.MessageId = &messageID
	}
	result, err := s.client.GetRoomManagementWithResponse(ctx, id, &params)
	if err != nil || result.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	management := *result.JSON200
	if (bansOnly || profile != "" || participantsOnly) && management.State == "unavailable" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if bansOnly || profile != "" || participantsOnly {
		if management.State != "available" || management.Revision == nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if participantsOnly {
			_ = roomManagementParticipants(v, management).Render(ctx, w)
			return
		}
		if bansOnly || bannedProfile {
			if !slices.Contains(management.Actions, engineapi.Affiliation) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if bansOnly {
				_ = roomBanList(v, management, true).Render(ctx, w)
				return
			}
			for _, address := range management.Affiliations {
				if roomBannedID(address) == profile {
					_ = roomBannedDetails(v, *management.Revision, address, roomBanNickname(management, address)).Render(ctx, w)
					return
				}
			}
		} else {
			for _, occupant := range management.Occupants {
				if roomParticipantID(occupant) == profile {
					_ = roomParticipantDetails(v, *management.Revision, occupant).Render(ctx, w)
					return
				}
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = roomManagementContent(v, management, messageID, invite).Render(ctx, w)
}

func (s *Server) manageRoom(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id, account, key := r.Form.Get("conversationId"), r.Form.Get("accountId"), r.Form.Get("operationKey")
	action := engineapi.RoomManagementAction(r.Form.Get("managementAction"))
	if !validOperationKey(id) || !validOperationKey(account) || !validOperationKey(key) || !action.Valid() {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	owner, err := s.client.GetConversationWithResponse(ctx, id)
	if err != nil || owner.JSON200 == nil {
		return http.StatusServiceUnavailable, mutationResult{Message: text.RoomCommandUnknown}
	}
	if owner.JSON200.Kind != "room" || owner.JSON200.AccountId != account {
		return http.StatusConflict, mutationResult{Message: text.RoomManagementConflict, Rejected: true}
	}
	var message *string
	if id := r.Form.Get("messageId"); id != "" {
		message = &id
	}
	target, value, reason := r.Form.Get("target"), r.Form.Get("value"), r.Form.Get("reason")
	command := engineapi.ManageRoom{
		Action: action, ExpectedRevision: r.Form.Get("expectedRevision"), Target: &target, Value: &value, Reason: &reason, MessageId: message,
	}
	if action == engineapi.SetAvatar {
		data, err := readAvatarUpload(r)
		if err != nil {
			return http.StatusBadRequest, mutationResult{Message: text.RoomAvatarInvalid, Rejected: true}
		}
		command.Image = &data
	} else if r.MultipartForm != nil && len(r.MultipartForm.File) > 0 {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	if action == engineapi.UpdateDetails {
		command.Name = changedRoomField(r, "name", "originalName")
		command.Topic = changedRoomField(r, "topic", "originalTopic")
		if command.Name == nil && command.Topic == nil {
			return http.StatusOK, mutationResult{OK: true, Message: text.RoomDetailsUnchanged}
		}
	}
	result, err := s.client.ManageRoomWithResponse(ctx, id, &engineapi.ManageRoomParams{IdempotencyKey: key}, command)
	if err != nil {
		return http.StatusServiceUnavailable, mutationResult{Message: text.RoomCommandUnknown}
	}
	if result.JSON200 == nil {
		if action == engineapi.SetAvatar && result.StatusCode() == http.StatusBadRequest {
			return http.StatusBadRequest, mutationResult{Message: text.RoomAvatarInvalid, Rejected: true}
		}
		return result.StatusCode(), problemResult(text, result.StatusCode(), result.Body)
	}
	switch result.JSON200.State {
	case "confirmed":
		if action == engineapi.Destroy {
			return http.StatusOK, mutationResult{OK: true, Message: text.RoomDestroyed, State: "confirmed"}
		}
		return http.StatusOK, mutationResult{OK: true, Message: text.RoomManagementConfirmed, State: "confirmed"}
	case "submitted":
		return http.StatusOK, mutationResult{OK: true, Message: text.RoomManagementSubmitted, State: "submitted"}
	case "name_confirmed":
		return http.StatusConflict, mutationResult{Message: text.RoomDetailsPartial, State: "name_confirmed", Rejected: true}
	case "denied":
		return http.StatusForbidden, mutationResult{Message: text.RoomManagementDenied, Rejected: true}
	case "stale":
		return http.StatusConflict, mutationResult{Message: text.RoomManagementConflict, Rejected: true}
	case "unsupported", "unavailable":
		return http.StatusServiceUnavailable, mutationResult{Message: text.RoomManagementUnavailable, Rejected: true}
	default:
		return http.StatusServiceUnavailable, mutationResult{Message: text.RoomCommandUnknown}
	}
}

func changedRoomField(r *http.Request, field, original string) *string {
	if !r.Form.Has(field) {
		return nil
	}
	normalize := strings.TrimSpace
	if field == "topic" {
		lineEndings := strings.NewReplacer("\r\n", "\n", "\r", "\n")
		normalize = func(value string) string { return lineEndings.Replace(strings.TrimSpace(value)) }
	}
	value := normalize(r.Form.Get(field))
	if r.Form.Has(original) && value == normalize(r.Form.Get(original)) {
		return nil
	}
	return &value
}

func (v view) canInviteToRoom() bool {
	return v.roomState() == "joined" && v.ConversationInfo != nil &&
		v.ConversationInfo.CanInviteToRoom != nil && *v.ConversationInfo.CanInviteToRoom
}

func roomManagementLabel(text copy, value string) string {
	switch value {
	case "moderator":
		return text.RoomRoleModerator
	case "participant":
		return text.RoomRoleParticipant
	case "visitor":
		return text.RoomRoleVisitor
	case "owner":
		return text.RoomAffiliationOwner
	case "admin":
		return text.RoomAffiliationAdmin
	case "member":
		return text.RoomAffiliationMember
	case "outcast":
		return text.RoomAffiliationOutcast
	default:
		return text.RoomAffiliationNone
	}
}
