package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

// OMEMOArchiveHost identifies messages already accepted from an encrypted
// envelope, including live copies which may have consumed its message key.
type OMEMOArchiveHost interface {
	RetainedOMEMO(ctx context.Context, in Incoming, legacyID string) (bool, error)
}

type omemoArchiveRepository interface {
	RetainedOMEMO(ctx context.Context, account domain.ID, address, providerID string, own bool) (bool, error)
}

func (e *Engine) RetainedOMEMO(ctx context.Context, in Incoming, legacyID string) (bool, error) {
	r, ok := e.repo.(omemoArchiveRepository)
	if !ok {
		return false, nil
	}
	retained, err := r.RetainedOMEMO(ctx, in.AccountID, in.Address, in.ProviderID, in.IsOwn)
	if err != nil || retained || legacyID == "" || len(legacyID) > 4096 {
		return retained, err
	}
	return r.RetainedOMEMO(ctx, in.AccountID, in.Address, legacyID, in.IsOwn)
}
