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
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/diagnostics"
)

// DesktopEngine is the selection saved privately for one desktop profile.
type DesktopEngine struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey,omitempty"`
}

type desktopTarget struct {
	selection DesktopEngine
	token     string
	ctx       context.Context
	cancel    context.CancelFunc
	ui        http.Handler
	engine    *Server
}

// Desktop keeps the native presentation origin stable while changing engines.
// Its engine listener exists only while the local engine is running.
type Desktop struct {
	ctx         context.Context
	cancel      context.CancelFunc
	dataDir     string
	xmppCAFile  string
	save        func(DesktopEngine) error
	mu          sync.Mutex
	active      atomic.Pointer[desktopTarget]
	lastLocal   *Server
	diagnostics *diagnostics.Logger
}

// StartDesktop uses an empty engine URL for local mode. The caller owns the
// per-profile preference and supplies the persistence function.
func StartDesktop(ctx context.Context, dataDir, xmppCAFile string, selection DesktopEngine, engineKeyFile string, logger *diagnostics.Logger, save func(DesktopEngine) error) (*Desktop, error) {
	if engineKeyFile != "" {
		key, err := desktopKeyFromFile(engineKeyFile)
		if err != nil {
			return nil, err
		}
		selection.APIKey = key
	}
	ctx, cancel := context.WithCancel(ctx)
	d := &Desktop{ctx: ctx, cancel: cancel, dataDir: dataDir, xmppCAFile: xmppCAFile, save: save, diagnostics: logger}
	token := rand.Text()
	target, err := d.prepare(ctx, selection, false, token)
	if err != nil && !errors.Is(err, web.ErrEngineModeKeyHTTPS) && ctx.Err() == nil && logger != nil {
		logger.Record(diagnostics.DesktopFailed, 0, 0)
		target, err = d.recovery(selection, token)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	d.activate(target)
	return d, nil
}

func desktopKeyFromFile(filename string) (string, error) {
	info, err := os.Stat(filename)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return "", errors.New("engine token file must be a private regular file (0600)")
	}
	body, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(body))
	if token == "" || !validDesktopKey(token) {
		return "", errors.New("engine token file must contain one API key")
	}
	return token, nil
}

func validDesktopKey(token string) bool {
	return len(token) <= 4096 && !strings.ContainsFunc(token, func(r rune) bool { return r < 0x21 || r > 0x7e })
}

func desktopKeyEditor(selection DesktopEngine) (engineapi.RequestEditorFn, error) {
	if selection.APIKey == "" {
		return nil, nil
	}
	origin, err := url.Parse(selection.URL)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil ||
		(origin.Path != "" && origin.Path != "/") || origin.RawPath != "" || origin.ForceQuery ||
		origin.RawQuery != "" || strings.Contains(selection.URL, "#") {
		return nil, web.ErrEngineModeKeyHTTPS
	}
	if !validDesktopKey(selection.APIKey) {
		return nil, web.ErrEngineModeAuthentication
	}
	return func(_ context.Context, request *http.Request) error {
		if request.URL.Scheme == "https" && strings.EqualFold(request.URL.Host, origin.Host) {
			request.Header.Set("Authorization", "Bearer "+selection.APIKey)
		}
		return nil
	}, nil
}

func (d *Desktop) prepare(ctx context.Context, selection DesktopEngine, probe bool, token string) (_ *desktopTarget, err error) {
	editor, err := desktopKeyEditor(selection)
	if err != nil {
		return nil, err
	}
	targetCtx, cancel := context.WithCancel(d.ctx)
	target := &desktopTarget{ctx: targetCtx, cancel: cancel, selection: selection, token: token}
	defer func() {
		if err != nil {
			target.stop()
		}
	}()
	var options []web.Option
	baseURL := selection.URL
	if selection.URL == "" {
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
	options = append(options, web.WithEngineRequestEditor(editor))
	options = append(options, d.options(target)...)
	ui, err := web.New(baseURL, options...)
	if err != nil {
		return nil, err
	}
	if probe && selection.URL != "" {
		if err = ui.CheckEngine(ctx); err != nil {
			return nil, err
		}
	}
	target.ui = presentationHandler(ui)
	return target, nil
}

func (d *Desktop) options(target *desktopTarget) []web.Option {
	selection := target.selection
	return []web.Option{
		web.WithDiagnostics(d.diagnostics, "", web.DiagnosticsNative),
		web.WithRemoteDiagnostics(selection.URL),
		web.WithEngineMode(target.token, selection.URL, selection.APIKey != "", func(ctx context.Context, address, key string) error {
			return d.selectEngine(ctx, target, DesktopEngine{URL: address, APIKey: key})
		}),
	}
}

func (d *Desktop) recovery(selection DesktopEngine, token string) (*desktopTarget, error) {
	ctx, cancel := context.WithCancel(d.ctx)
	target := &desktopTarget{ctx: ctx, cancel: cancel, selection: selection, token: token}
	options := append(d.options(target), web.WithUnavailableEngine())
	ui, err := web.New("http://127.0.0.1", options...)
	if err != nil {
		cancel()
		return nil, err
	}
	target.ui = presentationHandler(ui)
	return target, nil
}

func (d *Desktop) selectEngine(ctx context.Context, previous *desktopTarget, selection DesktopEngine) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active.Load() != previous {
		return web.ErrEngineModeChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if selection.URL == "" && previous.engine != nil && previous.engine.ctx.Err() == nil {
		if err := d.save(selection); err != nil {
			return errors.Join(web.ErrEngineModeSave, err)
		}
		return nil
	}
	if selection.APIKey == "" && strings.EqualFold(strings.TrimSuffix(selection.URL, "/"), strings.TrimSuffix(previous.selection.URL, "/")) {
		selection.APIKey = previous.selection.APIKey
	}
	next, err := d.prepare(ctx, selection, true, rand.Text())
	if err != nil {
		d.diagnostics.Record(diagnostics.EngineSwitchError, 0, 0)
		return err
	}
	if err = ctx.Err(); err != nil {
		next.stop()
		return err
	}
	if err = d.save(selection); err != nil {
		next.stop()
		d.diagnostics.Record(diagnostics.PreferencesFailed, 0, 0)
		return errors.Join(web.ErrEngineModeSave, err)
	}
	d.activate(next)
	previous.stop()
	event := diagnostics.EngineRemote
	if selection.URL == "" {
		event = diagnostics.EngineLocal
	}
	d.diagnostics.Record(event, 0, 0)
	return nil
}

// activate is called with mu held once the Desktop is serving requests.
func (d *Desktop) activate(target *desktopTarget) {
	d.active.Store(target)
	if target.selection.URL == "" {
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
			next, err := d.prepare(target.ctx, target.selection, false, target.token)
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
