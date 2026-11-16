//go:build (linux || darwin) && cgo && !server

package desktop

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/araihu/oie/internal/apphost"
	"github.com/araihu/oie/internal/diagnostics"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed assets/icon.png
var icon []byte

//go:embed native.js
var nativeJS string

type runtimeService struct {
	config      Config
	outer       context.Context
	server      atomic.Pointer[apphost.Desktop]
	app         *application.App
	cookies     *nativeCookies
	labels      labels
	dialogMu    sync.Mutex
	alerts      desktopNotifications
	diagnostics *diagnostics.Logger
}

func (s *runtimeService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	if err := s.outer.Err(); err != nil {
		return err
	}
	selection, err := desktopEngineSelection(s.config)
	if err != nil {
		return err
	}
	server, err := apphost.StartDesktop(ctx, s.config.DataDir, s.config.XMPPCAFile, selection, s.config.EngineTokenFile, s.diagnostics, func(selected apphost.DesktopEngine) error {
		return writePrivateJSON(engineModePath(s.config.DataDir), selected)
	})
	if err != nil {
		return err
	}
	s.server.Store(server)
	s.diagnostics.Record(diagnostics.Ready, 0, 0)
	go func() {
		select {
		case <-s.outer.Done():
			s.app.Quit()
		case <-ctx.Done():
		}
	}()
	return nil
}
func (s *runtimeService) ServiceShutdown() error {
	s.alerts.close()
	if server := s.server.Load(); server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Close(ctx)
	}
	return nil
}

// SaveFile opens the platform file chooser for an existing BFF download.
func (s *runtimeService) SaveFile(ctx context.Context, address, filename string) (bool, error) {
	if _, err := nativeDownloadURL(address); err != nil {
		return false, err
	}
	server := s.server.Load()
	if server == nil {
		return false, errors.New("desktop runtime unavailable")
	}
	s.dialogMu.Lock()
	defer s.dialogMu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return nativeSaveAction(ctx, s.cookies.handler(server), address, filename, func(name string) (string, error) {
		dialog := s.app.Dialog.SaveFile()
		dialog.SetOptions(&application.SaveFileDialogOptions{Title: s.labels.SaveFile, Filename: name, CanCreateDirectories: true, AllowOtherFileTypes: true, Window: s.app.Window.Current()})
		return dialog.PromptForSingleSelection()
	})
}

func (s *runtimeService) serve(w http.ResponseWriter, r *http.Request) {
	server := s.server.Load()
	if server == nil {
		http.Error(w, "", http.StatusServiceUnavailable)
		return
	}
	server.ServeHTTP(w, r)
}

// Run keeps one embedded runtime alive when its window is hidden. Explicit Quit
// stops the runtime through the Wails service lifecycle before process teardown.
func Run(ctx context.Context, c Config) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := normalizeConfig(c)
	dir := ""
	if config.DataDir != "" {
		dir = diagnosticsDir(config.DataDir)
	}
	logger := diagnostics.New(dir, os.Stderr, diagnostics.Desktop, diagnostics.IsDebug())
	diagnostics.UseStandardLogger(logger)
	logger.Record(diagnostics.Starting, 0, 0)
	defer func() {
		if err != nil {
			logger.Record(diagnostics.DesktopFailed, 0, 0)
		} else {
			logger.Record(diagnostics.Stopped, 0, 0)
		}
	}()
	if err != nil {
		return err
	}
	// Resolve the actual created profile once more before acquiring ownership.
	// macOS aliases /var and other existing parents through /private.
	if err = os.MkdirAll(config.DataDir, 0700); err != nil {
		return err
	}
	config.DataDir, err = filepath.EvalSymlinks(config.DataDir)
	if err != nil {
		return err
	}
	if err = prepareWebviewStorage(config.DataDir); err != nil {
		return err
	}
	cookies, err := newNativeCookies(config.DataDir)
	if err != nil {
		return err
	}
	menuLanguage := config.Language
	if (menuLanguage == "" || menuLanguage == "auto") && cookies.values["oie_lang"] != "" {
		menuLanguage = cookies.values["oie_lang"]
	}
	text, err := localizedLabels(deviceLanguage(menuLanguage))
	if err != nil {
		return err
	}
	cookies.diagnostics = logger
	service := &runtimeService{config: config, outer: ctx, cookies: cookies, labels: text, diagnostics: logger}
	saveFailed, _ := json.Marshal(text.SaveFailed)
	initialJS := strings.Replace(nativeJS, "'__OIE_SAVE_FAILED__'", string(saveFailed), 1)
	initialJS = strings.Replace(initialJS, "'__OIE_MAC_SOUND__'", strconv.FormatBool(runtime.GOOS == "darwin"), 1)
	var window *application.WebviewWindow
	var app *application.App
	var windowMu sync.Mutex
	reopen := func() {
		windowMu.Lock()
		defer windowMu.Unlock()
		if window != nil {
			application.InvokeSync(app.Show)
			window.Show()
			window.Restore()
			window.Focus()
		}
	}
	appID := desktopAppID()
	initialJS = strings.Replace(initialJS, "'__OIE_FLATPAK__'", strconv.FormatBool(appID == flatpakAppID), 1)
	app = application.New(application.Options{
		Name: "Oiê", Icon: icon, LogLevel: slog.LevelWarn, Logger: slog.New(slog.DiscardHandler),
		DisableDefaultSignalHandler: true,
		Mac:                         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Linux:                       application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ApplicationID: appID, ProgramName: appID},
		Services:                    []application.Service{application.NewService(service)},
		SingleInstance:              &application.SingleInstanceOptions{UniqueID: instanceID(config.DataDir), ExitCode: 0, OnSecondInstanceLaunch: func(application.SecondInstanceData) { reopen() }},
		Assets:                      application.AssetOptions{Handler: cookies.handler(http.HandlerFunc(service.serve)), DisableLogging: true},
	})
	service.app = app
	windowMu.Lock()
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "oie-main", Title: "Oiê", Width: 1200, Height: 820, MinWidth: 360, MinHeight: 500, URL: "/", JS: initialJS, ZoomControlEnabled: true, UseApplicationMenu: true})
	windowMu.Unlock()
	service.alerts.open = func(ids []string) {
		args, err := json.Marshal(ids)
		if err == nil {
			reopen()
			window.ExecJS("window.OieNotifications?.open(..." + string(args) + ")")
		}
	}
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if app.Context().Err() == nil {
			window.Hide()
			e.Cancel()
		}
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { reopen() })
	menu := app.Menu.New()
	menuTitle := text.File
	if runtime.GOOS == "darwin" {
		menuTitle = "Oiê"
	}
	file := menu.AddSubmenu(menuTitle)
	file.Add(text.Show).OnClick(func(*application.Context) { reopen() })
	file.AddSeparator()
	file.Add(text.Quit).SetAccelerator("CmdOrCtrl+q").OnClick(func(*application.Context) { app.Quit() })
	edit := menu.AddSubmenu(text.Edit)
	addEditRole := func(role application.Role, label string) {
		edit.AddRole(role)
		if item := edit.FindByRole(role); item != nil {
			item.SetLabel(label)
		}
	}
	addEditRole(application.Undo, text.Undo)
	addEditRole(application.Redo, text.Redo)
	edit.AddSeparator()
	addEditRole(application.Cut, text.Cut)
	addEditRole(application.Copy, text.Copy)
	addEditRole(application.Paste, text.Paste)
	edit.AddSeparator()
	addEditRole(application.SelectAll, text.SelectAll)
	app.Menu.Set(menu)
	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("Oiê")
	trayMenu := app.Menu.New()
	trayMenu.Add(text.Show).OnClick(func(*application.Context) { reopen() })
	trayMenu.AddSeparator()
	trayMenu.Add(text.Quit).OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(trayMenu)
	tray.OnClick(reopen)
	return app.Run()
}

func prepareWebviewStorage(dataDir string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	// WebKitGTK's default network session otherwise shares its default store with
	// unrelated applications using the same custom-scheme origin.
	for name, dir := range map[string]string{"XDG_DATA_HOME": filepath.Join(dataDir, "webview", "data"), "XDG_CACHE_HOME": filepath.Join(dataDir, "webview", "cache")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.Setenv(name, dir); err != nil {
			return err
		}
	}
	return nil
}
