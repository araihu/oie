package apphost

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/araihu/oie/internal/adapters/web"
)

type desktopTarget struct {
	ctx    context.Context
	cancel context.CancelFunc
	ui     http.Handler
	engine *Server
}

// Desktop keeps the native presentation origin stable while changing engines.
// Its engine listener exists only while the local engine is running.
type Desktop struct {
	ctx        context.Context
	cancel     context.CancelFunc
	dataDir    string
	xmppCAFile string
	save       func(string) error
	mu         sync.Mutex
	active     atomic.Pointer[desktopTarget]
	lastLocal  *Server
}

// StartDesktop uses an empty engine URL for local mode. The caller owns the
// per-profile preference and supplies the persistence function.
func StartDesktop(ctx context.Context, dataDir, xmppCAFile, engineURL string, save func(string) error) (*Desktop, error) {
	ctx, cancel := context.WithCancel(ctx)
	d := &Desktop{ctx: ctx, cancel: cancel, dataDir: dataDir, xmppCAFile: xmppCAFile, save: save}
	target, err := d.prepare(ctx, engineURL, false)
	if err != nil {
		cancel()
		return nil, err
	}
	d.active.Store(target)
	return d, nil
}

func (d *Desktop) prepare(ctx context.Context, engineURL string, probe bool) (_ *desktopTarget, err error) {
	targetCtx, cancel := context.WithCancel(d.ctx)
	target := &desktopTarget{ctx: targetCtx, cancel: cancel}
	defer func() {
		if err != nil {
			target.stop()
		}
	}()
	var options []web.Option
	baseURL := engineURL
	if engineURL == "" {
		if d.lastLocal != nil {
			select {
			case <-d.lastLocal.stopped:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		target.engine, err = Start(targetCtx, Config{
			Role: "local", Listen: "127.0.0.1:0", DataDir: d.dataDir,
			XMPPCAFile: d.xmppCAFile, PrivateAPI: true, engineOnly: true,
		})
		if err != nil {
			return nil, err
		}
		d.lastLocal = target.engine
		baseURL = target.engine.URL()
		options = append(options, web.WithEngineRequestEditor(target.engine.editEngineRequest))
	}
	options = append(options, web.WithEngineMode(rand.Text(), engineURL, func(ctx context.Context, url string) error {
		return d.selectEngine(ctx, target, url)
	}))
	ui, err := web.New(baseURL, options...)
	if err != nil {
		return nil, err
	}
	if probe && engineURL != "" {
		if err = ui.CheckEngine(ctx); err != nil {
			return nil, err
		}
	}
	target.ui = presentationHandler(ui)
	return target, nil
}

func (d *Desktop) selectEngine(ctx context.Context, previous *desktopTarget, engineURL string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active.Load() != previous {
		return web.ErrEngineModeChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if engineURL == "" && previous.engine != nil && previous.engine.ctx.Err() == nil {
		if err := d.save(""); err != nil {
			return errors.Join(web.ErrEngineModeSave, err)
		}
		return nil
	}
	next, err := d.prepare(ctx, engineURL, true)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		next.stop()
		return err
	}
	if err = d.save(engineURL); err != nil {
		next.stop()
		return errors.Join(web.ErrEngineModeSave, err)
	}
	d.active.Store(next)
	previous.stop()
	return nil
}

func (target *desktopTarget) stop() {
	if target.engine != nil {
		target.engine.beginClose()
	}
	target.cancel()
}

func (d *Desktop) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := d.active.Load()
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(target.ctx, cancel)
	defer stop()
	defer cancel()
	target.ui.ServeHTTP(w, r.WithContext(ctx))
}

func (d *Desktop) Close(ctx context.Context) error {
	d.cancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastLocal != nil {
		return d.lastLocal.Close(ctx)
	}
	return nil
}
