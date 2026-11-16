package apphost

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/diagnostics"
)

type desktopTarget struct {
	ctx       context.Context
	cancel    context.CancelFunc
	ui        http.Handler
	engine    *Server
	selection string
	local     bool
}

// Desktop keeps the native presentation origin stable while changing engines.
// Its engine listener exists only while the local engine is running.
type Desktop struct {
	ctx            context.Context
	cancel         context.CancelFunc
	dataDir        string
	xmppCAFile     string
	downloadDevice string
	save           func(string) error
	mu             sync.Mutex
	active         atomic.Pointer[desktopTarget]
	lastLocal      *Server
	diagnostics    *diagnostics.Logger
}

// StartDesktop uses an empty engine URL for local mode. The caller owns the
// per-profile preference and supplies the persistence function.
func StartDesktop(ctx context.Context, dataDir, xmppCAFile, engineURL string, logger *diagnostics.Logger, save func(string) error) (*Desktop, error) {
	ctx, cancel := context.WithCancel(ctx)
	d := &Desktop{ctx: ctx, cancel: cancel, dataDir: dataDir, xmppCAFile: xmppCAFile, save: save, diagnostics: logger, downloadDevice: desktopDownloadDevice(dataDir)}
	selection := rand.Text()
	target, err := d.prepare(ctx, engineURL, false, selection)
	if err != nil && ctx.Err() == nil && logger != nil {
		logger.Record(diagnostics.DesktopFailed, 0, 0)
		target, err = d.recovery(engineURL, selection)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	d.activate(target)
	return d, nil
}

func (d *Desktop) prepare(ctx context.Context, engineURL string, probe bool, selection string) (_ *desktopTarget, err error) {
	targetCtx, cancel := context.WithCancel(d.ctx)
	target := &desktopTarget{ctx: targetCtx, cancel: cancel, selection: selection, local: engineURL == ""}
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
			XMPPCAFile: d.xmppCAFile, PrivateAPI: true, engineOnly: true, Diagnostics: d.diagnostics,
		})
		if err != nil {
			return nil, err
		}
		d.lastLocal = target.engine
		baseURL = target.engine.URL()
		options = append(options, web.WithEngineRequestEditor(target.engine.editEngineRequest))
	}
	options = append(options, d.options(target, engineURL)...)
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

func (d *Desktop) options(target *desktopTarget, engineURL string) []web.Option {
	return []web.Option{
		web.WithDiagnostics(d.diagnostics, "", web.DiagnosticsNative),
		web.WithRemoteDiagnostics(engineURL),
		web.WithDownloadDevice(d.downloadDevice),
		web.WithEngineMode(target.selection, engineURL, func(ctx context.Context, url string) error {
			return d.selectEngine(ctx, target, url)
		}),
	}
}

func (d *Desktop) recovery(engineURL, selection string) (*desktopTarget, error) {
	ctx, cancel := context.WithCancel(d.ctx)
	target := &desktopTarget{ctx: ctx, cancel: cancel, selection: selection, local: engineURL == ""}
	options := append(d.options(target, engineURL), web.WithUnavailableEngine())
	ui, err := web.New("http://127.0.0.1", options...)
	if err != nil {
		cancel()
		return nil, err
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
	next, err := d.prepare(ctx, engineURL, true, rand.Text())
	if err != nil {
		d.diagnostics.Record(diagnostics.EngineSwitchError, 0, 0)
		return err
	}
	if err = ctx.Err(); err != nil {
		next.stop()
		return err
	}
	if err = d.save(engineURL); err != nil {
		next.stop()
		d.diagnostics.Record(diagnostics.PreferencesFailed, 0, 0)
		return errors.Join(web.ErrEngineModeSave, err)
	}
	d.activate(next)
	previous.stop()
	event := diagnostics.EngineRemote
	if engineURL == "" {
		event = diagnostics.EngineLocal
	}
	d.diagnostics.Record(event, 0, 0)
	return nil
}

// activate is called with mu held once the Desktop is serving requests.
func (d *Desktop) activate(target *desktopTarget) {
	d.active.Store(target)
	if target.local {
		go d.recoverLocal(target)
	}
}

func (d *Desktop) recoverLocal(target *desktopTarget) {
	const maxDelay = 30 * time.Second
	var delay time.Duration
	for target.ctx.Err() == nil {
		if target.engine != nil {
			started := time.Now()
			select {
			case <-target.ctx.Done():
				return
			case <-target.engine.stopped:
			}
			if target.ctx.Err() != nil || d.active.Load() != target {
				return
			}
			if time.Since(started) >= maxDelay {
				delay = 0
			}
			d.diagnostics.Record(diagnostics.DesktopFailed, 0, 0)
		}
		for target.ctx.Err() == nil {
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-target.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			delay = min(max(time.Second, delay*2), maxDelay)
			d.mu.Lock()
			if d.active.Load() != target || target.ctx.Err() != nil {
				d.mu.Unlock()
				return
			}
			next, err := d.prepare(target.ctx, "", false, target.selection)
			if err != nil {
				d.mu.Unlock()
				continue
			}
			if target.ctx.Err() != nil {
				next.stop()
				d.mu.Unlock()
				return
			}
			// Keep supervising the selection across short-lived replacements.
			d.active.Store(next)
			target.stop()
			target = next
			d.mu.Unlock()
			break
		}
	}
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

func desktopDownloadDevice(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	dir := filepath.Join(dataDir, "private")
	path := filepath.Join(dir, "download-device")
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	id, err := newID()
	if err != nil || os.MkdirAll(dir, 0700) != nil {
		return ""
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		data, _ := os.ReadFile(path)
		return string(data)
	}
	_, writeErr := file.WriteString(string(id))
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		_ = os.Remove(path)
		return ""
	}
	return string(id)
}
