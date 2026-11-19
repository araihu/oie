package web

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	selectfield "github.com/araihu/goshtoso/components/select"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

const readReceiptsPath = "/ui/settings/read-receipts"

func supportsReadReceipts(version engineapi.EngineApiVersion) bool {
	return supportsEngineMinor(version, 39)
}

func supportsEngineMinor(version engineapi.EngineApiVersion, required int) bool {
	majorText, rest, _ := strings.Cut(string(version), ".")
	minorText, patchText, _ := strings.Cut(rest, ".")
	major, majorErr := strconv.Atoi(majorText)
	minor, minorErr := strconv.Atoi(minorText)
	_, patchErr := strconv.Atoi(patchText)
	return majorErr == nil && minorErr == nil && patchErr == nil && (major > 0 || major == 0 && minor >= required)
}

type readReceiptsView struct {
	Revision, Notice string
	Override         *bool
	GlobalEnabled    bool
	Loading, Saved   bool
}

func (s *Server) readReceipts(w http.ResponseWriter, r *http.Request) {
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
	if id != "" && !validOperationKey(id) {
		http.NotFound(w, r)
		return
	}

	status := http.StatusOK
	notice := ""
	saved := false
	var policy readReceiptsView
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		status, notice = http.StatusServiceUnavailable, v.Text.ReadReceiptsUnavailable
	} else if !supportsReadReceipts(engine.JSON200.ApiVersion) {
		status, notice = http.StatusNotFound, v.Text.ReadReceiptsUnavailable
	} else {
		v.EngineID = engine.JSON200.Id
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
			switch {
			case r.ParseForm() != nil:
				status, notice = http.StatusUnprocessableEntity, v.Text.InvalidForm
			case !validCSRF(r):
				status, notice = http.StatusForbidden, v.Text.CSRFError
			case r.PostForm.Get("engineId") != v.EngineID:
				status, notice = http.StatusConflict, v.Text.ReadReceiptsConflict
			default:
				policy, status = s.saveReadReceipts(ctx, id, r.PostForm)
				switch status {
				case http.StatusOK:
					saved = true
				case http.StatusConflict:
					notice = v.Text.ReadReceiptsConflict
				case http.StatusUnprocessableEntity, http.StatusBadRequest:
					notice = v.Text.InvalidForm
				default:
					notice = v.Text.ReadReceiptsUnavailable
				}
			}
		}
		if r.Method == http.MethodGet || status == http.StatusConflict {
			var loadStatus int
			policy, loadStatus = s.loadReadReceipts(ctx, id)
			if loadStatus != http.StatusOK {
				status, notice = loadStatus, v.Text.ReadReceiptsUnavailable
			}
		}
	}
	policy.Notice, policy.Saved = notice, saved
	if saved && r.Header.Get("HX-Request") != "true" {
		q := url.Values{"lang": {v.Locale}, "theme": {v.Theme}}
		if id == "" {
			q.Set("open", "settings")
			q.Set("settingsTab", "engine")
		} else {
			q.Set("open", "details")
			q.Set("chat", id)
			q.Set("infoView", "read-receipts")
		}
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
		return
	}

	var body bytes.Buffer
	if err := readReceiptsRegion(v, id, policy).Render(presentationContext(ctx, v), &body); err != nil {
		http.Error(w, v.Text.ReadReceiptsUnavailable, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}

func (s *Server) loadReadReceipts(ctx context.Context, id string) (readReceiptsView, int) {
	if id == "" {
		out, err := s.client.GetGlobalReadReceiptsWithResponse(ctx)
		if err != nil {
			return readReceiptsView{}, http.StatusServiceUnavailable
		}
		if out.JSON200 == nil {
			return readReceiptsView{}, out.StatusCode()
		}
		return readReceiptsView{Revision: out.JSON200.Revision, GlobalEnabled: out.JSON200.Enabled}, http.StatusOK
	}
	out, err := s.client.GetConversationReadReceiptsWithResponse(ctx, id)
	if err != nil {
		return readReceiptsView{}, http.StatusServiceUnavailable
	}
	if out.JSON200 == nil {
		return readReceiptsView{}, out.StatusCode()
	}
	return readReceiptsView{Revision: out.JSON200.Revision, GlobalEnabled: out.JSON200.GlobalEnabled, Override: out.JSON200.Override}, http.StatusOK
}

func (s *Server) saveReadReceipts(ctx context.Context, id string, form url.Values) (readReceiptsView, int) {
	if len(form["mode"]) != 1 || len(form["revision"]) != 1 {
		return readReceiptsView{}, http.StatusUnprocessableEntity
	}
	var override *bool
	switch form.Get("mode") {
	case "on":
		override = new(true)
	case "off":
		override = new(false)
	case "inherit":
		if id == "" {
			return readReceiptsView{}, http.StatusUnprocessableEntity
		}
	default:
		return readReceiptsView{}, http.StatusUnprocessableEntity
	}
	revision := form.Get("revision")
	if id == "" {
		out, err := s.client.SetGlobalReadReceiptsWithResponse(ctx, engineapi.SetGlobalReadReceipts{ExpectedRevision: revision, Enabled: *override})
		if err != nil {
			return readReceiptsView{}, http.StatusServiceUnavailable
		}
		if out.JSON200 == nil {
			return readReceiptsView{}, out.StatusCode()
		}
		return readReceiptsView{Revision: out.JSON200.Revision, GlobalEnabled: out.JSON200.Enabled}, http.StatusOK
	}
	out, err := s.client.SetConversationReadReceiptsWithResponse(ctx, id, engineapi.SetConversationReadReceipts{ExpectedRevision: revision, Override: override})
	if err != nil {
		return readReceiptsView{}, http.StatusServiceUnavailable
	}
	if out.JSON200 == nil {
		return readReceiptsView{}, out.StatusCode()
	}
	return readReceiptsView{Revision: out.JSON200.Revision, GlobalEnabled: out.JSON200.GlobalEnabled, Override: out.JSON200.Override}, http.StatusOK
}

func (v view) readReceiptsHref(id string) string {
	q := url.Values{"lang": {v.Locale}}
	if id != "" {
		q.Set("chat", id)
	}
	return readReceiptsPath + "?" + q.Encode()
}

func readReceiptOptions(v view, id string, data readReceiptsView) []selectfield.Option {
	var options []selectfield.Option
	selected := data.GlobalEnabled
	if id != "" {
		label := v.Text.ReadReceiptsOff
		if data.GlobalEnabled {
			label = v.Text.ReadReceiptsOn
		}
		options = append(options, selectfield.Option{
			Value: "inherit", Label: strings.ReplaceAll(v.Text.ReadReceiptsInherit, "{setting}", label), Selected: data.Override == nil,
		})
		if data.Override != nil {
			selected = *data.Override
		}
	}
	explicit := id == "" || data.Override != nil
	return append(options,
		selectfield.Option{Value: "on", Label: v.Text.ReadReceiptsOn, Selected: explicit && selected},
		selectfield.Option{Value: "off", Label: v.Text.ReadReceiptsOff, Selected: explicit && !selected},
	)
}

func (v view) canSetReadReceipts(c *engineapi.Conversation) bool {
	return v.ReadReceiptsSupported && !v.Preview && c != nil && c.Request == nil && !removedConversation(c)
}
