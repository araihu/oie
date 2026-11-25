package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/araihu/goshtoso/components/badge"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) loadConversationDetails(ctx context.Context, v *view) {
	if v.Selected == nil {
		return
	}
	v.ConversationInfo = s.conversationDetails(ctx, *v.Selected)
}

func (s *Server) conversationDetails(ctx context.Context, chat engineapi.Conversation) *engineapi.ConversationDetails {
	response, err := s.client.GetConversationDetailsWithResponse(ctx, chat.Id)
	if err != nil || response.JSON200 == nil {
		return nil
	}
	return validConversationDetails(chat, response.JSON200)
}

func (v *view) applyConversationDetails(d *engineapi.ConversationDetails) {
	if v.Selected != nil {
		v.ConversationInfo = validConversationDetails(*v.Selected, d)
	}
}

func validConversationDetails(chat engineapi.Conversation, d *engineapi.ConversationDetails) *engineapi.ConversationDetails {
	if d == nil || d.ConversationId != chat.Id || d.AccountId != chat.AccountId || d.AccountConnection.AccountId != chat.AccountId || !d.AccountConnection.State.Valid() || d.Address != chat.Address || string(d.Kind) != string(chat.Kind) {
		return nil
	}
	if (d.Kind == "room" && d.Participants.Scope != "occupants") || (d.Kind == "direct" && d.Participants.Scope != "peer") {
		return nil
	}
	return d
}

func (v view) detailState(state string) string {
	switch state {
	case "pending":
		return v.Text.DetailsPending
	case "known":
		return v.Text.DetailsKnown
	case "partial":
		return v.Text.DetailsPartial
	case "forbidden":
		return v.Text.DetailsForbidden
	case "unavailable":
		return v.Text.DetailsUnavailable
	case "not_connected":
		return v.Text.DetailsNotConnected
	case "unsupported":
		return v.Text.DetailsUnsupported
	default:
		return v.Text.DetailsUnknown
	}
}
func (v view) presenceLabel(state string) string {
	switch state {
	case "available":
		return v.Text.PresenceAvailable
	case "away":
		return v.Text.PresenceAway
	case "busy":
		return v.Text.PresenceBusy
	case "extended_away":
		return v.Text.PresenceExtendedAway
	case "chat":
		return v.Text.PresenceChat
	case "unavailable":
		return v.Text.PresenceUnavailable
	default:
		return v.Text.PresenceUnknown
	}
}
func presenceTone(state string) badge.Tone {
	switch state {
	case "available", "chat":
		return badge.ToneSuccess
	case "away", "extended_away":
		return badge.ToneWarning
	case "busy":
		return badge.ToneDanger
	default:
		return badge.ToneDefault
	}
}
func (v view) detailsAccountView() view {
	if v.ConversationInfo == nil {
		return v
	}
	current := v
	current.Connections = map[string]connectionView{v.Selected.AccountId: {Known: true, Value: v.ConversationInfo.AccountConnection}}
	return current
}
func (v view) roomSessionLabel() string {
	if v.RoomSession != nil {
		return v.roomLabel()
	}
	if v.ConversationInfo != nil && v.ConversationInfo.RoomSession != nil && *v.ConversationInfo.RoomSession == "not_joined" {
		return v.Text.RoomNotJoined
	}
	return v.Text.DetailsUnknown
}
func (v view) roomFeatureLabel(feature string) string {
	labels := map[string]string{
		"public": v.Text.RoomPublic, "hidden": v.Text.RoomHidden, "members_only": v.Text.RoomMembersOnly, "open": v.Text.RoomOpen,
		"persistent": v.Text.RoomPersistent, "temporary": v.Text.RoomTemporary, "moderated": v.Text.RoomModerated, "unmoderated": v.Text.RoomUnmoderated,
		"password_protected": v.Text.RoomPasswordProtected, "unsecured": v.Text.RoomUnsecured, "anonymous": v.Text.RoomAnonymous, "non_anonymous": v.Text.RoomNonAnonymous,
	}
	if label := labels[feature]; label != "" {
		return label
	}
	return feature
}

// Section versions avoid rebuilding avatars and focused content for unrelated
// observations or browser-local timestamp formatting.
func (v view) detailsVersion(section string) string {
	var value any
	switch section {
	case "identity":
		if v.Selected != nil {
			value = []any{v.Selected.Id, v.infoTitle(), v.Selected.Address, v.avatarSource(v.Selected.AccountId, v.Selected.Address)}
			if v.ConversationInfo != nil {
				value = []any{value, v.ConversationInfo.OccupantCount, v.ConversationInfo.PeerPresence}
			}
		}
	case "account":
		if v.Selected != nil {
			current := v.detailsAccountView()
			value = []any{v.account(v.Selected.AccountId), current.Connections[v.Selected.AccountId]}
		}
	case "description":
		if v.ConversationInfo != nil {
			value = []any{v.ConversationInfo.Description, v.ConversationInfo.Topic}
		}
	case "metadata":
		if v.ConversationInfo != nil {
			metadata := *v.ConversationInfo
			metadata.Participants = engineapi.ConversationParticipants{}
			value = metadata
		}
	}
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
