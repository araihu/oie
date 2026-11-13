package application

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

type RoomManagementProtocol interface {
	RoomManagement(context.Context, domain.Account, domain.Conversation, string) (domain.RoomManagement, error)
	ManageRoom(context.Context, domain.Account, domain.Conversation, domain.ID, domain.RoomManagementCommand, domain.Message) string
}

type RoomManagementRepository interface {
	ClaimRoomManagement(context.Context, Operation, string) (string, error)
	FinishRoomManagement(context.Context, Operation, string, string, domain.ID) error
}

const RoomManagementOperation = "manage_room"

func (e *Engine) managementRoom(ctx context.Context, id domain.ID) (domain.Account, domain.Conversation, error) {
	c, err := e.Conversation(ctx, id)
	if err != nil {
		return domain.Account{}, c, err
	}
	if c.Kind != "room" || c.Request != "" {
		return domain.Account{}, c, ErrStateConflict
	}
	a, err := e.Account(ctx, c.AccountID)
	return a, c, err
}

func (e *Engine) RoomManagement(ctx context.Context, id domain.ID, affiliation string) (domain.RoomManagement, error) {
	return e.RoomManagementForMessage(ctx, id, affiliation, "")
}

func (e *Engine) RoomManagementForMessage(ctx context.Context, id domain.ID, affiliation string, messageID domain.ID) (domain.RoomManagement, error) {
	out := domain.RoomManagement{State: "not_joined", Actions: []string{}, Occupants: []domain.RoomManagedOccupant{}, Affiliations: []string{}}
	if affiliation != "" && !roomAffiliation(affiliation) {
		return out, ErrInvalid
	}
	a, c, err := e.managementRoom(ctx, id)
	if err != nil {
		return out, err
	}
	p, ok := e.protocols.(RoomManagementProtocol)
	if !ok {
		out.State = "unavailable"
		return out, nil
	}
	session, err := e.RoomSession(ctx, id)
	if err != nil {
		return out, err
	}
	if session.LastErrorCode == "not_connected" {
		out.State = "not_connected"
	}
	if !session.Active || session.State != "joined" {
		return out, nil
	}
	out, err = p.RoomManagement(ctx, a, c, affiliation)
	if err != nil || messageID == "" || !slices.Contains(out.Actions, "moderate_message") {
		return out, err
	}
	message, err := e.Message(ctx, messageID)
	if err != nil {
		return out, err
	}
	if message.AccountID != a.ID || message.ConversationID != c.ID {
		return out, ErrNotFound
	}
	if !roomModerationEligible(c, message) {
		out.Actions = slices.DeleteFunc(out.Actions, func(action string) bool { return action == "moderate_message" })
	}
	return out, nil
}

func roomModerationEligible(c domain.Conversation, message domain.Message) bool {
	return !message.IsOwn && message.SendState == "received" && message.Moderation == nil &&
		slices.ContainsFunc(message.References, func(ref domain.MessageReference) bool { return ref.Scope == "room:"+c.Address })
}

func roomAffiliation(value string) bool {
	return slices.Contains([]string{"none", "member", "admin", "owner", "outcast"}, value)
}

func roomManagementInput(c domain.RoomManagementCommand) bool {
	if len(c.ExpectedRevision) == 0 || len(c.ExpectedRevision) > 128 || !roomManagementText(c.ExpectedRevision, 128) ||
		!roomManagementText(c.Target, 2047) || !roomManagementText(c.Reason, 500) {
		return false
	}
	switch c.Action {
	case "rename":
		return c.MessageID == "" && c.Target == "" && c.Reason == "" && c.Value != "" && validRoomLabel(c.Value)
	case "topic":
		return c.MessageID == "" && c.Target == "" && c.Reason == "" && roomManagementText(c.Value, 2000)
	case "invite":
		return c.MessageID == "" && c.Target != "" && c.Value == ""
	case "role":
		return c.MessageID == "" && strings.HasPrefix(c.Target, "occupant:") && slices.Contains([]string{"none", "visitor", "participant", "moderator"}, c.Value)
	case "affiliation":
		return c.MessageID == "" && c.Target != "" && roomAffiliation(c.Value)
	case "moderate_message":
		return validID(c.MessageID) && c.Target == "" && c.Value == ""
	default:
		return false
	}
}

func roomManagementText(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (e *Engine) ManageRoom(ctx context.Context, key, id domain.ID, cmd domain.RoomManagementCommand) (string, error) {
	cmd.Target, cmd.Value, cmd.Reason = strings.TrimSpace(cmd.Target), strings.TrimSpace(cmd.Value), strings.TrimSpace(cmd.Reason)
	if !validID(key) || !validID(id) || !roomManagementInput(cmd) {
		return "", ErrInvalid
	}
	repo, ok := e.repo.(RoomManagementRepository)
	p, supported := e.protocols.(RoomManagementProtocol)
	if !ok || !supported {
		return "", ErrProtocolUnavailable
	}
	a, c, err := e.managementRoom(ctx, id)
	if err != nil {
		return "", err
	}
	if cmd.Action == "invite" || cmd.Action == "affiliation" && !strings.HasPrefix(cmd.Target, "occupant:") {
		_, address, err := e.roomTarget(ctx, a.ID, cmd.Target)
		if err != nil || address == a.Address || address == c.Address {
			return "", ErrInvalid
		}
		cmd.Target = address
	}
	var message domain.Message
	if cmd.Action == "moderate_message" {
		message, err = e.Message(ctx, cmd.MessageID)
		if err != nil {
			return "", err
		}
		if message.ConversationID != c.ID || message.AccountID != a.ID || message.IsOwn || message.SendState != "received" {
			return "", ErrInvalid
		}
	}
	fp := fingerprint(RoomManagementOperation, []any{id, cmd})
	op := Operation{ID: key, ResourceID: id, Kind: RoomManagementOperation, CreatedAt: e.now().UTC()}
	state, err := repo.ClaimRoomManagement(ctx, op, fp)
	if err != nil || state != "" {
		return state, err
	}
	// Claim before the wire attempt. A crash or lost reply leaves the receipt
	// uncertain, so replay can never repeat a provider mutation.
	state = "stale"
	if intent, err := e.roomIntent(ctx, id); err == nil && intent.Active && intent.CreationName == nil {
		state = p.ManageRoom(ctx, a, c, intent.IntentRevisionID, cmd, message)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var name string
	var revision domain.ID
	if state == "confirmed" && cmd.Action == "rename" {
		name = cmd.Value
		revision, err = e.newID()
		if err != nil {
			return "unknown", nil
		}
	}
	if err := repo.FinishRoomManagement(persist, op, state, name, revision); err != nil {
		return "unknown", nil
	}
	if name != "" {
		e.notifyEvents()
	}
	return state, nil
}
