package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web/bffapi"
)

var (
	ErrEngineModeChanged        = errors.New("engine mode changed")
	ErrEngineModeAuthentication = errors.New("engine access key required")
	ErrEngineModeKeyHTTPS       = errors.New("engine key requires HTTPS")
	ErrEngineModeSave           = errors.New("save desktop engine mode")
	errEngineUnavailable        = errors.New("engine unavailable")
)

type engineMode struct {
	selection, url string
	hasKey         bool
	save           func(context.Context, string, string) error
}

func WithEngineMode(selection, url string, hasKey bool, save func(context.Context, string, string) error) Option {
	return func(s *Server) {
		s.engineMode = &engineMode{selection: selection, url: url, hasKey: hasKey, save: save}
	}
}

// WithUnavailableEngine leaves the presentation reachable when an embedded
// engine could not start. It avoids sending any requests to a placeholder URL.
func WithUnavailableEngine() Option {
	return func(s *Server) { s.engineUnavailable = true }
}

type unavailableEngineTransport struct{}

func (unavailableEngineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errEngineUnavailable
}

func validEngineURL(address string) bool {
	u, err := url.Parse(address)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" &&
		u.User == nil && !u.ForceQuery && u.RawQuery == "" && !strings.Contains(address, "#")
}

// CheckEngine makes a bounded read of the engine descriptor before switching.
func (s *Server) CheckEngine(ctx context.Context) error {
	res, err := s.streamClient.GetEngine(ctx)
	if err != nil {
		return errEngineUnavailable
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusUnauthorized {
		return ErrEngineModeAuthentication
	}
	mediaType, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode != http.StatusOK || mediaType != "application/json" {
		return errEngineUnavailable
	}
	const maxDescriptorSize = 16 * 1024
	body, err := io.ReadAll(io.LimitReader(res.Body, maxDescriptorSize+1))
	var engine engineapi.Engine
	if err != nil || len(body) > maxDescriptorSize || json.Unmarshal(body, &engine) != nil ||
		!validOperationKey(engine.Id) || !engine.Role.Valid() {
		return errEngineUnavailable
	}
	return nil
}

func (s *Server) setEngineMode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.engineMode == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	text := s.catalogs[locale(r)]
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, text.EngineModeInvalid, http.StatusBadRequest)
		return
	}
	if !validCSRF(r) {
		http.Error(w, text.CSRFError, http.StatusForbidden)
		return
	}
	if r.PostForm.Get("selection") != s.engineMode.selection {
		http.Error(w, text.EngineModeChanged, http.StatusConflict)
		return
	}
	var address, key string
	switch bffapi.SetDesktopEngineModeFormdataBodyMode(r.PostForm.Get("mode")) {
	case bffapi.SetDesktopEngineModeFormdataBodyModeLocal:
	case bffapi.SetDesktopEngineModeFormdataBodyModeRemote:
		rawAddress := r.PostForm.Get("url")
		address = strings.TrimSpace(rawAddress)
		key = strings.TrimSpace(r.PostForm.Get("apiKey"))
		if len(key) > 512 || (key != "" && r.PostForm.Get("apiKeyAddress") != rawAddress) {
			http.Error(w, text.EngineModeAuthentication, http.StatusBadRequest)
			return
		}
		if len(address) > 2048 || !validEngineURL(address) {
			http.Error(w, text.EngineModeInvalid, http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, text.EngineModeInvalid, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	err := s.engineMode.save(ctx, address, key)
	switch {
	case errors.Is(err, ErrEngineModeChanged):
		http.Error(w, text.EngineModeChanged, http.StatusConflict)
	case errors.Is(err, ErrEngineModeKeyHTTPS):
		http.Error(w, text.EngineModeKeyHTTPS, http.StatusBadRequest)
	case errors.Is(err, ErrEngineModeAuthentication):
		http.Error(w, text.EngineModeAuthentication, http.StatusUnauthorized)
	case errors.Is(err, ErrEngineModeSave):
		http.Error(w, text.EngineModeSaveFailed, http.StatusInternalServerError)
	case err != nil:
		http.Error(w, text.EngineModeUnavailable, http.StatusServiceUnavailable)
	case r.Header.Get("Accept") == "application/json":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}
