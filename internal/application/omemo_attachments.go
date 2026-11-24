package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

type omemoAttachmentReader interface {
	OpenOMEMOAttachment(context.Context, domain.EncryptedAttachment) (MediaRead, error)
}

func (e *Engine) DownloadOMEMOAttachment(ctx context.Context, id domain.ID, index int) (MediaRead, error) {
	m, err := e.Message(ctx, id)
	if err != nil {
		return MediaRead{}, err
	}
	if m.ID != id || m.DirectEncryption != "omemo" || m.Retraction != nil || m.Moderation != nil ||
		index < 0 || index >= len(m.EncryptedAttachments) {
		return MediaRead{}, ErrNotFound
	}
	reader, ok := e.protocols.(omemoAttachmentReader)
	if !ok {
		return MediaRead{}, ErrProtocolUnavailable
	}
	result, err := reader.OpenOMEMOAttachment(ctx, m.EncryptedAttachments[index])
	if err != nil {
		return MediaRead{}, err
	}
	current, err := e.Message(ctx, id)
	if err != nil || current.RevisionID != m.RevisionID || current.Retraction != nil || current.Moderation != nil {
		_ = result.Body.Close()
		if err != nil {
			return MediaRead{}, err
		}
		return MediaRead{}, ErrNotFound
	}
	return result, nil
}
