package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

const (
	downloadRetentionPath = "/ui/settings/download-retention"
	downloadDeviceCookie  = "oie_download_device"
)

func WithDownloadDevice(id string) Option {
	return func(s *Server) {
		if validOperationKey(id) {
			s.fixedDownloadDevice = id
		}
	}
}

func (s *Server) downloadDevice(w http.ResponseWriter, r *http.Request) string {
	if id := s.currentDownloadDevice(r); id != "" {
		return id
	}
	id := newOperationKey()
	if id != "" {
		http.SetCookie(w, &http.Cookie{
			Name: downloadDeviceCookie, Value: id, Path: "/", HttpOnly: true,
			Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
			MaxAge: int((365 * 24 * time.Hour).Seconds()),
		})
	}
	return id
}

func (s *Server) currentDownloadDevice(r *http.Request) string {
	if s.fixedDownloadDevice != "" {
		return s.fixedDownloadDevice
	}
	if cookie, err := r.Cookie(downloadDeviceCookie); err == nil && validOperationKey(cookie.Value) {
		return cookie.Value
	}
	return ""
}

func (s *Server) downloadRetention(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		if !validCSRF(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if r.ParseForm() != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	engineID := r.Header.Get("X-Oie-Engine-ID")
	if !validOperationKey(engineID) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if engine.JSON200.Id != engineID {
		w.WriteHeader(http.StatusConflict)
		return
	}
	device := s.downloadDevice(w, r)
	if device == "" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var policy *engineapi.DownloadRetention
	if r.Method == http.MethodGet {
		response, err := s.client.GetDownloadRetentionWithResponse(ctx, device)
		if err == nil {
			policy = response.JSON200
		}
	} else {
		switch r.PostForm.Get("action") {
		case "save":
			days, err := strconv.Atoi(r.PostForm.Get("days"))
			if err != nil || days < 0 || days > 3650 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			response, err := s.client.SetDownloadRetentionWithResponse(ctx, device, engineapi.SetDownloadRetention{Days: days})
			if err == nil {
				policy = response.JSON200
			}
		case "clear":
			response, err := s.client.ClearDeviceDownloadsWithResponse(ctx, device)
			if err == nil {
				policy = response.JSON200
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	if policy == nil || policy.Days < 0 || policy.Days > 3650 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(policy)
}

func downloadDaysLabel(pattern, days string) string {
	return strings.ReplaceAll(pattern, "{days}", days)
}
