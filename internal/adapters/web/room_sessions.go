package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) loadMessaging(ctx context.Context, v *view) {
	if v.Selected == nil || v.Preview {
		return
	}
	if v.CapabilityEvidence != nil && v.CapabilityEvidence.AccountId == v.Selected.AccountId {
		v.MessagingCapabilities = v.CapabilityEvidence
	} else if response, err := s.client.GetAccountCapabilitiesWithResponse(ctx, v.Selected.AccountId); err == nil && response.JSON200 != nil && response.JSON200.AccountId == v.Selected.AccountId {
		v.MessagingCapabilities = response.JSON200
	}
	if v.Selected.Kind == "room" {
		if response, err := s.client.GetRoomSessionWithResponse(ctx, v.Selected.Id); err == nil && response.JSON200 != nil && validRoomSession(*response.JSON200, v.Selected.Id, v.Selected.AccountId) {
			v.RoomSession = response.JSON200
		}
	}
}
func validRoomSession(value engineapi.RoomSession, conversation, account string) bool {
	return value.ConversationId == conversation && value.AccountId == account && value.State.Valid() && (value.IntentRevisionId == nil || validOperationKey(*value.IntentRevisionId))
}
func (v view) messagingImplemented(feature string) bool {
	if v.MessagingCapabilities == nil {
		return false
	}
	for _, row := range v.MessagingCapabilities.Features {
		if string(row.Id) == feature {
			return (row.AppSupport == "supported" || row.AppSupport == "partial") && (row.ProtocolSupport == "supported" || row.ProtocolSupport == "not_required")
		}
	}
	return false
}
func (v view) composeAllowed() bool {
	return !v.Preview && !v.Unavailable && v.Selected != nil && v.account(v.Selected.AccountId).Address != "" && (v.Selected.Kind == "direct" || v.RoomSession != nil)
}
func (v view) attachmentAllowed() bool {
	if !v.scheduleAllowed() || !v.sendAllowed() {
		return false
	}
	for _, feature := range v.MessagingCapabilities.Features {
		if feature.Id == "file_upload" {
			return (feature.AppSupport == "supported" || feature.AppSupport == "partial") &&
				feature.ProtocolSupport == "supported" && feature.ServerSupport != "not_advertised" && feature.ServerSupport != "unsupported"
		}
	}
	return false
}
func (v view) replyAllowed() bool {
	return v.composeAllowed() && v.messagingImplemented("send_replies")
}
func (v view) roomLabel() string {
	if v.RoomSession == nil {
		return v.Text.RoomReadOnly
	}
	switch v.RoomSession.State {
	case "joined":
		return v.Text.RoomJoined
	case "joining":
		return v.Text.RoomJoining
	case "failed":
		return v.Text.RoomJoinFailed
	default:
		return v.Text.RoomNotJoined
	}
}
func (v view) roomError() string {
	if v.RoomSession == nil {
		return ""
	}
	if v.RoomSession.CreationError != nil {
		switch *v.RoomSession.CreationError {
		case "room_exists":
			return v.Text.RoomExists
		case "creation_failed":
			return v.Text.RoomCreationFailed
		}
	}
	if v.RoomSession.LastErrorCode == nil {
		return ""
	}
	switch *v.RoomSession.LastErrorCode {
	case "nickname_conflict":
		return v.Text.RoomNicknameConflict
	case "forbidden", "removed":
		return v.Text.RoomForbidden
	case "not_authorized", "registration_required":
		return v.Text.RoomNotAuthorized
	case "not_connected":
		return v.Text.NotConnected
	case "timeout":
		return v.Text.RoomJoinTimeout
	default:
		return v.Text.RoomUnavailable
	}
}
func roomRevision(value *engineapi.RoomSession) string {
	if value != nil && value.IntentRevisionId != nil {
		return *value.IntentRevisionId
	}
	return ""
}
func (s *Server) changeRoom(ctx context.Context, r *http.Request, text copy, leave bool) (int, mutationResult) {
	conversation, account, key, expected := r.Form.Get("conversationId"), r.Form.Get("accountId"), r.Form.Get("operationKey"), r.Form.Get("expectedRevisionId")
	if !validOperationKey(conversation) || !validOperationKey(account) || !validOperationKey(key) || !r.Form.Has("expectedRevisionId") || (expected != "" && !validOperationKey(expected)) {
		return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	owner, err := s.client.GetConversationWithResponse(ctx, conversation)
	if err != nil || owner.JSON200 == nil {
		return 503, mutationResult{Message: text.RoomCommandUnknown}
	}
	if owner.JSON200.AccountId != account || owner.JSON200.Kind != "room" {
		return 409, mutationResult{Message: text.OperationConflict, Rejected: true}
	}
	var revision *string
	if expected != "" {
		revision = &expected
	}
	var result *engineapi.RoomSession
	var status int
	var body []byte
	if leave {
		response, callErr := s.client.LeaveRoomWithResponse(ctx, conversation, &engineapi.LeaveRoomParams{IdempotencyKey: key}, engineapi.LeaveRoom{ExpectedRevisionId: revision})
		err = callErr
		if response != nil {
			status = response.StatusCode()
			body = response.Body
			result = response.JSON200
		}
	} else {
		nickname := r.Form.Get("nickname")
		if len([]rune(nickname)) > 100 || strings.ContainsAny(nickname, "\r\n") {
			return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
		}
		var nick *string
		if nickname != "" {
			nick = &nickname
		}
		join := engineapi.JoinRoom{ExpectedRevisionId: revision, Nickname: nick}
		if expectedChat := r.Form.Get("expectedConversationRevisionId"); expectedChat != "" {
			if !validOperationKey(expectedChat) || len(r.Form["expectedConversationRevisionId"]) != 1 {
				return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
			}
			join.ExpectedConversationRevisionId = &expectedChat
		}
		response, callErr := s.client.JoinRoomWithResponse(ctx, conversation, &engineapi.JoinRoomParams{IdempotencyKey: key}, join)
		err = callErr
		if response != nil {
			status = response.StatusCode()
			body = response.Body
			result = response.JSON200
		}
	}
	if err != nil {
		return 503, mutationResult{Message: text.RoomCommandUnknown}
	}
	if result == nil {
		out := problemResult(text, status, body)
		out.Message = text.RoomCommandFailed
		if status == 409 && out.Code != "idempotency_conflict" {
			out.Message = text.RoomConflict
			out.Rejected = true
		}
		return status, out
	}
	if !validRoomSession(*result, conversation, account) {
		return 503, mutationResult{Message: text.RoomCommandUnknown}
	}
	message := text.RoomJoinRequested
	if leave {
		message = text.RoomLeaveRequested
	}
	return 200, mutationResult{OK: true, Message: message, RoomSession: result, NextKey: newOperationKey()}
}

func (v view) roomState() string {
	if v.RoomSession != nil {
		return string(v.RoomSession.State)
	}
	return "unknown"
}
func roomNickname(v view) string {
	if v.FormTarget == "room-"+v.ChatID {
		return v.field("nickname")
	}
	if v.RoomSession != nil && v.RoomSession.Nickname != nil {
		return *v.RoomSession.Nickname
	}
	return ""
}
func (v view) roomVersion() string {
	data, _ := json.Marshal([]any{v.RoomSession, v.MessagingCapabilities, v.Unavailable})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
