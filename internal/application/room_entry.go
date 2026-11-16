package application

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

var ErrRoomAddressConflict = errors.New("room address is already used by a direct conversation with retained content")

type roomNicknameValidator interface {
	ValidateRoomNickname(domain.Account, string) error
}

func (e *Engine) checkRoomNickname(account domain.Account, nickname string) error {
	if validator, ok := e.protocols.(roomNicknameValidator); ok {
		return validator.ValidateRoomNickname(account, nickname)
	}
	return nil
}

type RoomPreviewProvider interface {
	PreviewRoom(context.Context, domain.Account, string, bool) (domain.RoomPreview, []byte, error)
}

var (
	ErrInvalidRoomService      = errors.New("address domain is not an XMPP room service")
	ErrRoomCreationUnavailable = errors.New("could not verify the XMPP room address")
	ErrRoomAlreadyExists       = errors.New("room already exists")
)

type RoomCreator interface {
	CheckRoomCreation(context.Context, domain.Account, string) error
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

func (e *Engine) PreviewRoom(ctx context.Context, accountID domain.ID, address string, discoveredServiceOnly bool) (domain.RoomPreview, error) {
	a, canonical, err := e.roomTarget(ctx, accountID, address)
	if err != nil {
		return domain.RoomPreview{}, err
	}
	p, ok := e.protocols.(RoomPreviewProvider)
	if !ok {
		return domain.RoomPreview{}, ErrProtocolUnavailable
	}
	preview, photo, err := p.PreviewRoom(ctx, a, canonical, discoveredServiceOnly)
	if err != nil {
		return domain.RoomPreview{}, err
	}
	preview.Address = canonical
	if e.avatarFiles != nil && len(photo) > 0 {
		preview.AvatarHash, _ = e.avatarFiles.Put(ctx, photo)
	}
	return preview, nil
}

func validRoomPassword(value string) bool {
	return len(value) <= 1023 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validRoomNickname(value string) bool {
	return validRoomLabel(value) && !strings.ContainsRune(value, '/')
}

func validRoomLabel(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 100 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (e *Engine) OpenRoom(ctx context.Context, key, accountID domain.ID, address, nickname, name string, create bool, passwords ...string) (ConversationResult, error) {
	var password string
	if len(passwords) > 0 {
		password = passwords[0]
	}
	if !validRoomPassword(password) || create && password != "" {
		return ConversationResult{}, ErrInvalid
	}
	nickname, name = strings.TrimSpace(nickname), strings.TrimSpace(name)
	if !validID(key) || !validRoomNickname(nickname) || !validRoomLabel(name) || (!create && name != "") {
		return ConversationResult{}, ErrInvalid
	}
	account, canonical, err := e.roomTarget(ctx, accountID, address)
	if err != nil {
		return ConversationResult{}, err
	}
	if _, ok := e.protocols.(RoomProtocol); !ok {
		return ConversationResult{}, ErrProtocolUnavailable
	}
	if err := e.checkRoomNickname(account, nickname); err != nil {
		return ConversationResult{}, err
	}
	repo, err := e.messaging()
	if err != nil {
		return ConversationResult{}, err
	}
	if create {
		creator, ok := e.protocols.(RoomCreator)
		if !ok {
			return ConversationResult{}, ErrProtocolUnavailable
		}
		if err := e.checkRoomCreation(ctx, creator, key, account, canonical); err != nil {
			return ConversationResult{}, err
		}
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
	parts := []string{string(accountID), canonical, nickname, name}
	if password != "" {
		parts = append(parts, password)
	}
	result, err := repo.OpenConversation(ctx, ConversationMutation{
		Operation:    Operation{ID: key, ResourceID: id, CreatedAt: now, Kind: kind},
		Fingerprint:  fingerprint(kind, parts),
		Conversation: conversation, RoomEntry: &intent, RoomPassword: password,
	})
	if err == nil && !result.Replayed {
		e.wakeRooms()
		e.outgoingWorkCommitted()
	}
	return result, err
}

// Already accepted requests replay through OpenConversation even if the service
// is now unreachable or the first attempt has already created the room.
func (e *Engine) checkRoomCreation(ctx context.Context, creator RoomCreator, key domain.ID, account domain.Account, address string) error {
	if _, err := e.Operation(ctx, key); !errors.Is(err, ErrNotFound) {
		return err
	}
	err := creator.CheckRoomCreation(ctx, account, address)
	if err != nil {
		if _, replayErr := e.Operation(ctx, key); replayErr == nil {
			return nil
		}
	}
	return err
}
