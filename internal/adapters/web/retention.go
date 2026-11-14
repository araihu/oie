package web

import (
	"bytes"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	selectfield "github.com/araihu/goshtoso/components/select"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

const (
	retentionPath    = "/ui/settings/retention"
	maxRetentionDays = 365000 // Engine API RetentionDays maximum.
)

type retentionView struct {
	Conversation *engineapi.Conversation
	Global       *engineapi.GlobalRetention
	Local        *engineapi.ConversationRetention
	Notice       string
	Saved        bool
	Loading      bool
}

func (s *Server) retention(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	v := s.baseView(r)
	v.CSRF = csrfToken(w, r)
	ctx := r.Context()
	id := r.URL.Query().Get("chat")
	data := retentionView{}
	status := http.StatusOK
	defer func() {
		var body bytes.Buffer
		if err := retentionRegion(v, id, data).Render(presentationContext(ctx, v), &body); err != nil {
			http.Error(w, v.Text.Unavailable, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = body.WriteTo(w)
	}()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		status, data.Notice = http.StatusServiceUnavailable, v.Text.RetentionUnavailable
		return
	}
	v.EngineID = engine.JSON200.Id
	if id != "" {
		if !validOperationKey(id) {
			status, data.Notice = http.StatusNotFound, v.Text.RetentionUnavailable
			return
		}
		owner, err := s.client.GetConversationWithResponse(ctx, id)
		if err != nil || owner.JSON200 == nil || owner.JSON200.Id != id {
			status, data.Notice = http.StatusServiceUnavailable, v.Text.RetentionUnavailable
			if err == nil && owner.StatusCode() == http.StatusNotFound {
				status = http.StatusNotFound
			}
			return
		}
		v.Selected = owner.JSON200
		if v.requestKind() != "" {
			status, data.Notice = http.StatusNotFound, v.Text.RetentionUnavailable
			return
		}
		data.Conversation = owner.JSON200
		response, err := s.client.GetConversationRetentionWithResponse(ctx, id)
		if err == nil {
			data.Local = response.JSON200
		}
	} else {
		response, err := s.client.GetGlobalRetentionWithResponse(ctx)
		if err == nil {
			data.Global = response.JSON200
		}
	}
	if data.Global == nil && data.Local == nil {
		status, data.Notice = http.StatusServiceUnavailable, v.Text.RetentionUnavailable
		return
	}
	if r.Method == http.MethodGet {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if r.ParseForm() != nil {
		status, data.Notice = http.StatusUnprocessableEntity, v.Text.InvalidForm
		return
	}
	if !validCSRF(r) {
		status, data.Notice = http.StatusForbidden, v.Text.CSRFError
		return
	}
	if r.PostForm.Get("engineId") != v.EngineID {
		status, data.Notice = http.StatusConflict, v.Text.RetentionConflict
		return
	}
	revision := r.PostForm.Get("revision")
	if data.Local != nil {
		days, ok := retentionFormDays(r.PostForm, "override", true)
		if !ok {
			status, data.Notice = http.StatusUnprocessableEntity, v.Text.InvalidForm
			return
		}
		response, err := s.client.SetConversationRetentionWithResponse(ctx, id, engineapi.SetConversationRetention{ExpectedRevision: revision, Days: days})
		if err != nil {
			status, data.Notice = http.StatusServiceUnavailable, v.Text.RetentionUnavailable
			return
		}
		status = response.StatusCode()
		if response.JSON200 != nil {
			data.Local = response.JSON200
		}
	} else {
		rooms, roomOK := retentionFormDays(r.PostForm, "room", false)
		direct, directOK := retentionFormDays(r.PostForm, "direct", false)
		if !roomOK || !directOK {
			status, data.Notice = http.StatusUnprocessableEntity, v.Text.InvalidForm
			return
		}
		response, err := s.client.SetGlobalRetentionWithResponse(ctx, engineapi.SetGlobalRetention{ExpectedRevision: revision, RoomDays: *rooms, DirectDays: *direct})
		if err != nil {
			status, data.Notice = http.StatusServiceUnavailable, v.Text.RetentionUnavailable
			return
		}
		status = response.StatusCode()
		if response.JSON200 != nil {
			data.Global = response.JSON200
		}
	}
	switch status {
	case http.StatusOK:
		data.Saved = true
	case http.StatusConflict:
		data.Notice = v.Text.RetentionConflict
		if data.Local != nil {
			if latest, err := s.client.GetConversationRetentionWithResponse(ctx, id); err == nil && latest.JSON200 != nil {
				data.Local = latest.JSON200
			}
		} else if latest, err := s.client.GetGlobalRetentionWithResponse(ctx); err == nil && latest.JSON200 != nil {
			data.Global = latest.JSON200
		}
	default:
		data.Notice = v.Text.RetentionUnavailable
	}
}

func retentionFormDays(values url.Values, prefix string, allowInherit bool) (*int, bool) {
	if len(values[prefix+"-mode"]) != 1 {
		return nil, false
	}
	switch values.Get(prefix + "-mode") {
	case "inherit":
		return nil, allowInherit
	case "unlimited":
		zero := 0
		return &zero, true
	case "finite":
		day, err := strconv.Atoi(values.Get(prefix + "-days"))
		return &day, err == nil && day > 0 && day <= maxRetentionDays && len(values[prefix+"-days"]) == 1
	}
	return nil, false
}

func (v view) retentionHref(id string) string {
	q := url.Values{"lang": {v.Locale}}
	if id != "" {
		q.Set("chat", id)
	}
	return retentionPath + "?" + q.Encode()
}

func (v view) retentionDrawerHref() string {
	q := url.Values{"lang": {v.Locale}, "theme": {v.Theme}}
	if validOperationKey(v.ChatID) {
		q.Set("chat", v.ChatID)
		q.Set("open", "details")
		q.Set("infoView", "retention")
	} else {
		q.Set("open", "settings")
		q.Set("settingsTab", "device")
	}
	return "/?" + q.Encode()
}

func retentionRegionID(id string) string {
	if id == "" {
		return "device-retention"
	}
	return "conversation-retention"
}

func retentionMode(days *int) string {
	if days == nil {
		return "inherit"
	}
	if *days == 0 {
		return "unlimited"
	}
	return "finite"
}

func retentionAlpine(days *int) string {
	return "{mode:" + strconv.Quote(retentionMode(days)) + "}"
}

func retentionDayValue(days *int, inherited int) string {
	if days != nil && *days > 0 {
		return strconv.Itoa(*days)
	}
	if inherited > 0 {
		return strconv.Itoa(inherited)
	}
	return ""
}

func retentionOptions(v view, days *int, inherited int, canInherit bool) []selectfield.Option {
	mode := retentionMode(days)
	options := make([]selectfield.Option, 0, 3)
	if canInherit {
		period := v.Text.RetentionUnlimited
		if inherited > 0 {
			period = strings.ReplaceAll(v.Text.RetentionInheritDays, "{days}", strconv.Itoa(inherited))
		}
		label := strings.ReplaceAll(v.Text.RetentionInherit, "{period}", period)
		options = append(options, selectfield.Option{Value: "inherit", Label: label, Selected: mode == "inherit"})
	}
	return append(options,
		selectfield.Option{Value: "finite", Label: v.Text.RetentionFinite, Selected: mode == "finite"},
		selectfield.Option{Value: "unlimited", Label: v.Text.RetentionUnlimited, Selected: mode == "unlimited"},
	)
}
