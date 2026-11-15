package apphost

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/diagnostics"
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
	ctx          context.Context
	cancel       context.CancelFunc
	dataDir      string
	xmppCAFile   string
	keyEditor    engineapi.RequestEditorFn
	keyEngineURL string
	save         func(string) error
	mu           sync.Mutex
	active       atomic.Pointer[desktopTarget]
	lastLocal    *Server
	diagnostics  *diagnostics.Logger
}

// StartDesktop uses an empty engine URL for local mode. The caller owns the
// per-profile preference and supplies the persistence function.
func StartDesktop(ctx context.Context, dataDir, xmppCAFile, engineURL, engineKeyFile string, logger *diagnostics.Logger, save func(string) error) (*Desktop, error) {
	editor, err := desktopKeyEditor(engineURL, engineKeyFile)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	d := &Desktop{ctx: ctx, cancel: cancel, dataDir: dataDir, xmppCAFile: xmppCAFile, save: save, keyEditor: editor, keyEngineURL: strings.TrimSuffix(engineURL, "/"), diagnostics: logger}
	target, err := d.prepare(ctx, engineURL, false)
	if err != nil && ctx.Err() == nil && logger != nil {
		logger.Record(diagnostics.DesktopFailed, 0, 0)
		target, err = d.recovery(engineURL)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	d.active.Store(target)
	return d, nil
}

func desktopKeyEditor(address, filename string) (engineapi.RequestEditorFn, error) {
	if filename == "" {
		return nil, nil
	}
	origin, err := url.Parse(address)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil ||
		(origin.Path != "" && origin.Path != "/") || origin.RawPath != "" || origin.ForceQuery ||
		origin.RawQuery != "" || strings.Contains(address, "#") {
		return nil, errors.New("the remote engine token file requires a hosted HTTPS origin")
	}
	info, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return nil, errors.New("engine token file must be a private regular file (0600)")
	}
	body, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(body))
	if token == "" || strings.ContainsAny(token, " \t\r\n\x00") {
		return nil, errors.New("engine token file must contain one API key")
	}
	return func(_ context.Context, request *http.Request) error {
		if request.URL.Scheme == "https" && strings.EqualFold(request.URL.Host, origin.Host) {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		return nil
	}, nil
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
	var options []web.Option
	if d.keyEditor != nil && strings.EqualFold(strings.TrimSuffix(engineURL, "/"), d.keyEngineURL) {
		options = append(options, web.WithEngineRequestEditor(d.keyEditor))
	}
	return append(options,
		web.WithDiagnostics(d.diagnostics, "", web.DiagnosticsNative),
		web.WithRemoteDiagnostics(engineURL),
		web.WithEngineMode(rand.Text(), engineURL, func(ctx context.Context, url string) error {
			return d.selectEngine(ctx, target, url)
		}),
	)
}

func (d *Desktop) recovery(engineURL string) (*desktopTarget, error) {
	ctx, cancel := context.WithCancel(d.ctx)
	target := &desktopTarget{ctx: ctx, cancel: cancel}
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
	next, err := d.prepare(ctx, engineURL, true)
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
	d.active.Store(next)
	previous.stop()
	event := diagnostics.EngineRemote
	if engineURL == "" {
		event = diagnostics.EngineLocal
	}
	d.diagnostics.Record(event, 0, 0)
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
