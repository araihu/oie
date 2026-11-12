package application

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

type RoomPreviewProvider interface {
	PreviewRoom(context.Context, domain.Account, string) (domain.RoomPreview, []byte, error)
}

type RoomCreator interface {
	CreateRoom(context.Context, domain.Account, domain.Conversation, string, string, domain.ID) (domain.RoomSession, error)
}

func (e *Engine) roomTarget(ctx context.Context, accountID domain.ID, address string) (domain.Account, string, error) {
	a, err := e.Account(ctx, accountID)
	if err != nil {
		return a, "", err
	}
	address = strings.TrimSpace(address)
	if a.Protocol != "xmpp" || strings.ContainsAny(address, "/?#") || strings.Count(address, "@") != 1 || len(address) > 2047 {
		return a, "", ErrInvalid
	}
	canonical, err := e.canonical(a.Protocol, address)
	return a, canonical, err
}

func (e *Engine) PreviewRoom(ctx context.Context, accountID domain.ID, address string) (domain.RoomPreview, error) {
	a, canonical, err := e.roomTarget(ctx, accountID, address)
	if err != nil {
		return domain.RoomPreview{}, err
	}
	p, ok := e.protocols.(RoomPreviewProvider)
	if !ok {
		return domain.RoomPreview{}, ErrProtocolUnavailable
	}
	preview, photo, err := p.PreviewRoom(ctx, a, canonical)
	if err != nil {
		return domain.RoomPreview{}, err
	}
	preview.Address = canonical
	if e.avatarFiles != nil && len(photo) > 0 {
		preview.AvatarHash, _ = e.avatarFiles.Put(ctx, photo)
	}
	return preview, nil
}

func validRoomNickname(value string) bool {
	return validRoomLabel(value) && !strings.ContainsRune(value, '/')
}

func validRoomLabel(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 100 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (e *Engine) OpenRoom(ctx context.Context, key, accountID domain.ID, address, nickname, name string, create bool) (ConversationResult, error) {
	nickname, name = strings.TrimSpace(nickname), strings.TrimSpace(name)
	if !validID(key) || !validRoomNickname(nickname) || !validRoomLabel(name) || (!create && name != "") {
		return ConversationResult{}, ErrInvalid
	}
	_, canonical, err := e.roomTarget(ctx, accountID, address)
	if err != nil {
		return ConversationResult{}, err
	}
	if _, ok := e.protocols.(RoomProtocol); !ok {
		return ConversationResult{}, ErrProtocolUnavailable
	}
	if create {
		if _, ok := e.protocols.(RoomCreator); !ok {
			return ConversationResult{}, ErrProtocolUnavailable
		}
	}
	repo, err := e.messaging()
	if err != nil {
		return ConversationResult{}, err
	}
	id, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	revision, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	peer, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	intentID, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	intentRevision, err := e.newID()
	if err != nil {
		return ConversationResult{}, err
	}
	now := e.now().UTC()
	conversation := domain.Conversation{ID: id, RevisionID: revision, AccountID: accountID, PeerID: peer, Kind: "room", Address: canonical, Title: canonical, UpdatedAt: now}
	intent := domain.RoomSession{AccountID: accountID, ConversationID: id, IntentID: intentID, IntentRevisionID: intentRevision, Nickname: nickname, Active: true, State: "not_joined", UpdatedAt: now}
	kind := "open_room"
	if create {
		kind = "create_room"
		intent.CreationName = &name
		if name != "" {
			conversation.Title = name
		}
	}
	result, err := repo.OpenConversation(ctx, ConversationMutation{
		Operation:    Operation{ID: key, ResourceID: id, CreatedAt: now, Kind: kind},
		Fingerprint:  fingerprint(kind, []string{string(accountID), canonical, nickname, name}),
		Conversation: conversation, RoomEntry: &intent,
	})
	if err == nil && !result.Replayed {
		e.wakeRooms()
		e.outgoingWorkCommitted()
	}
	return result, err
}
