package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

type AvatarFiles interface {
	Put(context.Context, []byte) (string, error)
	Read(context.Context, string) ([]byte, error)
}
type AvatarRepository interface {
	Avatar(context.Context, domain.ID, string) (domain.Avatar, error)
	Avatars(context.Context, domain.ID, domain.ID, int) ([]domain.Avatar, error)
	SaveAvatar(context.Context, domain.Avatar) error
	AvatarTargets(context.Context, domain.ID) ([]domain.AvatarTarget, error)
}
type AvatarHost interface {
	Avatar(context.Context, domain.ID, string) (domain.Avatar, error)
	AvatarTargets(context.Context, domain.ID) ([]domain.AvatarTarget, error)
	AvatarImage(context.Context, string) ([]byte, error)
	StoreAvatar(context.Context, domain.ID, string, string, []byte) error
}

func (e *Engine) SetAvatarFiles(files AvatarFiles) { e.avatarFiles = files }
func (e *Engine) Avatar(ctx context.Context, id domain.ID, address string) (domain.Avatar, error) {
	r, ok := e.repo.(AvatarRepository)
	if !ok {
		return domain.Avatar{}, ErrNotFound
	}
	return r.Avatar(ctx, id, address)
}
func (e *Engine) Avatars(ctx context.Context, id, after domain.ID, limit int) ([]domain.Avatar, error) {
	if limit < 1 || limit > 100 || (after != "" && !validID(after)) {
		return nil, ErrInvalid
	}
	if _, err := e.Account(ctx, id); err != nil {
		return nil, err
	}
	r, ok := e.repo.(AvatarRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r.Avatars(ctx, id, after, limit+1)
}
func (e *Engine) AvatarTargets(ctx context.Context, id domain.ID) ([]domain.AvatarTarget, error) {
	if _, err := e.Account(ctx, id); err != nil {
		return nil, err
	}
	r, ok := e.repo.(AvatarRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r.AvatarTargets(ctx, id)
}
func ValidImageHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (e *Engine) AvatarImage(ctx context.Context, hash string) ([]byte, error) {
	if !ValidImageHash(hash) {
		return nil, ErrInvalid
	}
	if e.avatarFiles == nil {
		return nil, ErrNotFound
	}
	return e.avatarFiles.Read(ctx, hash)
}
func (e *Engine) StoreAvatar(ctx context.Context, id domain.ID, address, source string, data []byte) error {
	a, err := e.Account(ctx, id)
	if err != nil {
		return err
	}
	address, err = e.canonical(a.Protocol, address)
	if err != nil {
		return err
	}
	if len(source) > 256 || !utf8.ValidString(source) || strings.ContainsRune(source, 0) {
		return ErrInvalid
	}
	hash := ""
	if data != nil {
		if e.avatarFiles == nil {
			return ErrProtocolUnavailable
		}
		hash, err = e.avatarFiles.Put(ctx, data)
		if err != nil {
			return err
		}
	}
	entity, err := e.newID()
	if err != nil {
		return err
	}
	revision, err := e.newID()
	if err != nil {
		return err
	}
	r, ok := e.repo.(AvatarRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	return r.SaveAvatar(ctx, domain.Avatar{ID: entity, RevisionID: revision, AccountID: id, Address: address, ImageHash: hash, SourceToken: source, UpdatedAt: e.now().UTC()})
}
