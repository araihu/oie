package web

import "github.com/araihu/oie/internal/adapters/httpapi/engineapi"

func (v view) requestKind() string {
	if v.Selected == nil || v.Selected.Request == nil {
		return ""
	}
	return string(*v.Selected.Request)
}

func (v view) requestLabel(c engineapi.Conversation) string {
	if c.Request != nil {
		switch *c.Request {
		case "checking":
			return v.Text.CheckingConversation
		case "message":
			return v.Text.MessageRequest
		case "room_invitation":
			return v.Text.RoomInvitation
		}
	}
	return c.LastMessage
}
