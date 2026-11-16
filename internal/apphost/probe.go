package apphost

import (
	"context"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/probeapi"
)

type probes struct {
	engine *engineHost
}

func probeHandler(engine *engineHost) http.Handler {
	handler := probeapi.Handler(probeapi.NewStrictHandler(probes{engine: engine}, nil))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		handler.ServeHTTP(w, r)
	})
}

func (probes) GetLiveness(ctx context.Context, _ probeapi.GetLivenessRequestObject) (probeapi.GetLivenessResponseObject, error) {
	if ctx.Err() != nil {
		return probeapi.GetLiveness503TextResponse(http.StatusText(http.StatusServiceUnavailable)), nil
	}
	return probeapi.GetLiveness200TextResponse("ok"), nil
}

func (p probes) GetReadiness(ctx context.Context, _ probeapi.GetReadinessRequestObject) (probeapi.GetReadinessResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if !p.engine.ready(ctx) {
		return probeapi.GetReadiness503TextResponse(http.StatusText(http.StatusServiceUnavailable)), nil
	}
	return probeapi.GetReadiness200TextResponse("ok"), nil
}

func (h *engineHost) ready(ctx context.Context) bool {
	h.mu.Lock()
	instance := h.instance
	if instance == nil || instance.ctx.Err() != nil {
		h.mu.Unlock()
		return false
	}
	// Reset waits for this read before closing or replacing the database.
	instance.requests.Add(1)
	h.mu.Unlock()
	defer instance.requests.Done()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(instance.ctx, cancel)
	defer stop()
	select {
	case <-instance.done:
		return false
	default:
		return h.store.Ping(ctx) == nil && ctx.Err() == nil && instance.ctx.Err() == nil
	}
}
