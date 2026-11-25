package application

import (
	"context"
	"strings"

	"github.com/araihu/oie/internal/domain"
)

// RoomOMEMOEcho links a server-confirmed room reference to an encrypted
// wire attempt, whose cleartext is already retained by the sender.
type RoomOMEMOEcho struct {
	AccountID, MessageID domain.ID
	Room, Author         string
	StanzaID, ArchiveID  string
	EnvelopeID           string
	Part                 int
}

type RoomOMEMOEchoHost interface {
	BindRoomOMEMOEcho(context.Context, RoomOMEMOEcho) error
}

type roomOMEMOEchoRepository interface {
	BindRoomOMEMOEcho(context.Context, RoomOMEMOEcho) (bool, error)
}

type RetainedRoomOMEMOArchiveHost interface {
	BindRetainedRoomOMEMOArchive(context.Context, Incoming) error
}

type retainedRoomOMEMOArchiveRepository interface {
	BindRetainedRoomOMEMOArchive(context.Context, Incoming) (bool, error)
}

func ValidOMEMOEnvelopeID(id string) bool {
	digest, ok := strings.CutPrefix(id, "omemo:")
	return ok && ValidImageHash(digest)
}

func (e *Engine) BindRoomOMEMOEcho(ctx context.Context, in RoomOMEMOEcho) error {
	r, ok := e.repo.(roomOMEMOEchoRepository)
	if !ok {
		return nil
	}
	if !validID(in.AccountID) || !validID(in.MessageID) || !validText(in.Room, 1023) ||
		!strings.HasPrefix(in.Author, in.Room+"/") || !validText(in.Author, 4096) ||
		!ValidOMEMOEnvelopeID(in.EnvelopeID) || in.Part < 0 || in.Part > MaxMessageAttachments ||
		(in.StanzaID == "" && in.ArchiveID == "") || in.StanzaID != "" && !validText(in.StanzaID, 4096) ||
		in.ArchiveID != "" && !validText(in.ArchiveID, 4096) {
		return ErrInvalid
	}
	changed, err := r.BindRoomOMEMOEcho(ctx, in)
	if err == nil && changed {
		e.notifyEvents()
	}
	return err
}

func (e *Engine) BindRetainedRoomOMEMOArchive(ctx context.Context, in Incoming) error {
	r, ok := e.repo.(retainedRoomOMEMOArchiveRepository)
	if !ok {
		return nil
	}
	if in.SenderAddress == "" {
		return nil
	}
	if !validID(in.AccountID) || in.Kind != "room" || !in.Encrypted || !in.Historical ||
		!validText(in.Address, 1023) || in.ArchiveScope != in.Address || !validText(in.ArchiveID, 4096) ||
		!ValidOMEMOEnvelopeID(in.ProviderID) || !validText(in.SenderAddress, 1023) || !validText(in.SenderName, 1023) ||
		len(in.References) > 1 {
		return ErrInvalid
	}
	if len(in.References) == 1 {
		ref := in.References[0]
		if ref.Scope != "room:"+in.Address || ref.Author != in.Address+"/"+in.SenderName || !validText(ref.ID, 4096) {
			return ErrInvalid
		}
	}
	changed, err := r.BindRetainedRoomOMEMOArchive(ctx, in)
	if err == nil && changed {
		e.notifyEvents()
	}
	return err
}

// OMEMOArchiveHost identifies messages already accepted from an encrypted
// envelope, including live copies which may have consumed its message key.
type OMEMOArchiveHost interface {
	RetainedOMEMO(ctx context.Context, in Incoming, legacyID string) (bool, error)
}

type omemoArchiveRepository interface {
	RetainedOMEMO(ctx context.Context, account domain.ID, kind, address, providerID string, own bool) (bool, error)
}

func (e *Engine) RetainedOMEMO(ctx context.Context, in Incoming, legacyID string) (bool, error) {
	r, ok := e.repo.(omemoArchiveRepository)
	if !ok {
		return false, nil
	}
	retained, err := r.RetainedOMEMO(ctx, in.AccountID, in.Kind, in.Address, in.ProviderID, in.IsOwn)
	if err != nil || retained || legacyID == "" || len(legacyID) > 4096 {
		return retained, err
	}
	return r.RetainedOMEMO(ctx, in.AccountID, in.Kind, in.Address, legacyID, in.IsOwn)
}
