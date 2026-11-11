package apphost

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/araihu/oie/internal/adapters/avatarfiles"
	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/adapters/previewfetch"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/adapters/videothumb"
	xmpadapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

const resetPath = "/api/v1/engine/reset"

type engineHost struct {
	ctx      context.Context
	cfg      Config
	store    *sqlite.Store
	roots    *x509.CertPool
	failures chan error
	mu       sync.Mutex // protects instance and admission of API requests
	instance *engineInstance
}

type engineInstance struct {
	id        domain.ID
	ctx       context.Context
	cancel    context.CancelFunc
	handler   http.Handler
	protocols *xmpadapter.Manager
	done      chan struct{}
	requests  sync.WaitGroup
}

func newEngineHost(ctx context.Context, cfg Config, store *sqlite.Store) (*engineHost, error) {
	roots, err := certificateRoots(cfg.XMPPCAFile)
	if err != nil {
		return nil, err
	}
	host := &engineHost{ctx: ctx, cfg: cfg, store: store, roots: roots, failures: make(chan error, 1)}
	host.instance, err = host.start()
	if err != nil {
		return nil, err
	}
	return host, nil
}

func (h *engineHost) start() (_ *engineInstance, err error) {
	ctx, cancel := context.WithCancel(h.ctx)
	instance := &engineInstance{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	defer func() {
		if err != nil {
			cancel()
			if instance.protocols != nil {
				instance.protocols.Close()
			}
		}
	}()
	engine, err := application.New(h.store, h.cfg.Role, newID, time.Now)
	if err != nil {
		return nil, err
	}
	snapshot, err := engine.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	instance.id = snapshot.ID
	fetcher := previewfetch.New()
	dir := h.cfg.DataDir
	engine.SetAvatarFiles(avatarfiles.New(filepath.Join(dir, "media", "avatars")))
	engine.SetPreviewServices(fetcher, mediafiles.New(filepath.Join(dir, "media", "images")))
	engine.SetMediaReader(fetcher)
	engine.SetVideoThumbnailer(videothumb.New(h.cfg.FFmpeg))
	instance.protocols = xmpadapter.New(filepath.Join(dir, "private", "connections"), h.roots)
	engine.SetProtocols(instance.protocols)
	if err = instance.protocols.Start(ctx, engine); err != nil {
		return nil, err
	}
	instance.handler, err = httpapi.NewResettable(engine, h.reset)
	if err != nil {
		return nil, err
	}
	go func() {
		defer close(instance.done)
		err := engine.Run(ctx)
		if ctx.Err() == nil {
			h.failures <- err
		}
	}()
	return instance, nil
}

func (h *engineHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	instance := h.instance
	if instance == nil {
		h.mu.Unlock()
		httpapi.WriteProblem(w, application.ErrProtocolUnavailable)
		return
	}
	// The reset request coordinates draining and cannot itself be counted.
	if r.Method == http.MethodPost && r.URL.Path == resetPath {
		h.mu.Unlock()
		instance.handler.ServeHTTP(w, r)
		return
	}
	instance.requests.Add(1)
	h.mu.Unlock()
	defer instance.requests.Done()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(instance.ctx, cancel)
	defer stop()
	instance.handler.ServeHTTP(w, r.WithContext(ctx))
}

func (h *engineHost) reset(expected domain.ID) error {
	h.mu.Lock()
	current := h.instance
	if current == nil || current.id != expected {
		h.mu.Unlock()
		return application.ErrStateConflict
	}
	id, err := newID()
	if err != nil {
		h.mu.Unlock()
		return err
	}
	h.instance = nil
	h.mu.Unlock()

	current.close()
	if err := h.store.Reset(context.WithoutCancel(h.ctx), id); err != nil {
		slog.Error("delete engine data", "error", err)
		return err
	}
	next, err := h.start()
	if err != nil {
		slog.Error("start empty engine", "error", err)
		return err
	}
	h.mu.Lock()
	h.instance = next
	h.mu.Unlock()
	return nil
}

func (e *engineInstance) close() {
	e.cancel()
	e.requests.Wait()
	<-e.done
	e.protocols.Close()
}
