package web

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	qr "github.com/skip2/go-qrcode"
)

const legacyOMEMO = "eu.siacs.conversations.axolotl"

func (v view) omemoHref(account, chat string) string {
	q := url.Values{"account": {account}, "lang": {v.Locale}}
	if chat != "" {
		q.Set("chat", chat)
	}
	return "/ui/omemo?" + q.Encode()
}
func trustURI(address string, d engineapi.OmemoDevice, legacy bool) string {
	query := "trust-message;encryption=" + legacyOMEMO + ";trust=" + d.Fingerprint
	if legacy {
		query = "omemo-sid-" + strconv.Itoa(d.Id) + "=" + d.Fingerprint
	}
	return "xmpp:" + url.PathEscape(address) + "?" + query
}
func fingerprintQR(uri string) templ.SafeURL {
	data, err := qr.Encode(uri, qr.Medium, 220)
	if err != nil {
		return ""
	}
	return templ.SafeURL("data:image/png;base64," + base64.StdEncoding.EncodeToString(data))
}
func fingerprintGroups(fp string) string {
	var groups []string
	for len(fp) >= 8 {
		groups = append(groups, fp[:8])
		fp = fp[8:]
	}
	return strings.Join(groups, " ")
}
func candidateDevice(value string, state engineapi.OmemoDevices) (engineapi.OmemoDevice, bool) {
	value = strings.TrimSpace(value)
	var id int
	if strings.HasPrefix(value, "xmpp:") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "xmpp" || u.Host != "" || u.Fragment != "" {
			return engineapi.OmemoDevice{}, false
		}
		addr, err := url.PathUnescape(u.Opaque)
		if err != nil || addr != state.Address {
			return engineapi.OmemoDevice{}, false
		}
		q := strings.TrimPrefix(u.RawQuery, "message;")
		if strings.HasPrefix(q, "trust-message;encryption="+legacyOMEMO+";trust=") {
			value = strings.TrimPrefix(q, "trust-message;encryption="+legacyOMEMO+";trust=")
		} else {
			n, fp, ok := strings.Cut(strings.TrimPrefix(q, "omemo-sid-"), "=")
			if !strings.HasPrefix(q, "omemo-sid-") || !ok {
				return engineapi.OmemoDevice{}, false
			}
			id, err = strconv.Atoi(n)
			if err != nil || id <= 0 {
				return engineapi.OmemoDevice{}, false
			}
			value = fp
		}
	}
	fp := strings.ToLower(strings.Join(strings.Fields(value), ""))
	raw, err := hex.DecodeString(fp)
	if err != nil || len(raw) != 32 {
		return engineapi.OmemoDevice{}, false
	}
	var match engineapi.OmemoDevice
	for _, d := range state.Devices {
		if d.Active && !d.Own && d.Fingerprint == fp && (id == 0 || id == d.Id) {
			if match.Id != 0 {
				return match, false
			}
			match = d
		}
	}
	return match, match.Id != 0
}
func uploadedTrustURI(r *http.Request) (string, error) {
	f, _, err := r.FormFile("qr")
	if errors.Is(err, http.ErrMissingFile) {
		return r.PostForm.Get("candidate"), nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return "", errors.New("invalid QR image")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width > 2048 || config.Height > 2048 {
		return "", errors.New("invalid QR dimensions")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	result, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		return "", err
	}
	return result.GetText(), nil
}
func (s *Server) omemoPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	v := s.baseView(r)
	v.CSRF = csrfToken(w, r)
	ctx := r.Context()
	account := r.URL.Query().Get("account")
	chat := r.URL.Query().Get("chat")
	if !validOperationKey(account) || chat != "" && !validOperationKey(chat) {
		http.NotFound(w, r)
		return
	}
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil || engine.JSON200.OmemoEnabled == nil || !*engine.JSON200.OmemoEnabled {
		http.Error(w, v.Text.OmemoUnavailable, http.StatusServiceUnavailable)
		return
	}
	v.EngineID = engine.JSON200.Id
	v.Role = engine.JSON200.Role
	var peer *string
	if chat != "" {
		c, err := s.client.GetConversationWithResponse(ctx, chat)
		if err != nil || c.JSON200 == nil || c.JSON200.Kind != "direct" || c.JSON200.AccountId != account {
			http.NotFound(w, r)
			return
		}
		peer = &c.JSON200.Address
	}
	var preview *engineapi.OmemoDevice
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, (2<<20)+(64<<10))
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			err = r.ParseMultipartForm(3 << 20)
			if r.MultipartForm != nil {
				defer func() { _ = r.MultipartForm.RemoveAll() }()
			}
		} else {
			err = r.ParseForm()
		}
		if err != nil || !validCSRF(r) {
			http.Error(w, v.Text.CSRFError, http.StatusForbidden)
			return
		}
		if r.PostForm.Get("engineId") != v.EngineID {
			http.Error(w, v.Text.OperationConflict, http.StatusConflict)
			return
		}
	}
	response, err := s.client.GetOmemoDevicesWithResponse(ctx, account, &engineapi.GetOmemoDevicesParams{Peer: peer})
	if err != nil || response.JSON200 == nil {
		http.Error(w, v.Text.OmemoUnavailable, http.StatusServiceUnavailable)
		return
	}
	state := *response.JSON200
	if r.Method == "POST" {
		action := r.PostForm.Get("action")
		if action == "review" {
			value, err := uploadedTrustURI(r)
			d, ok := candidateDevice(value, state)
			if err != nil || !ok {
				http.Error(w, v.Text.OmemoMismatch, http.StatusBadRequest)
				return
			}
			preview = &d
		} else {
			body := engineapi.OmemoDecision{Action: engineapi.OmemoDecisionAction(action)}
			switch action {
			case "verify", "distrust":
				id, err := strconv.Atoi(r.PostForm.Get("device"))
				if err != nil || id < 1 {
					http.Error(w, v.Text.InvalidForm, http.StatusBadRequest)
					return
				}
				fp := r.PostForm.Get("fingerprint")
				body.Device = &id
				body.Fingerprint = &fp
			case "encrypt", "plaintext":
				if peer == nil {
					http.Error(w, v.Text.InvalidForm, http.StatusBadRequest)
					return
				}
			default:
				http.Error(w, v.Text.InvalidForm, http.StatusBadRequest)
				return
			}
			saved, err := s.client.SetOmemoDecisionWithResponse(ctx, account, &engineapi.SetOmemoDecisionParams{Peer: peer}, body)
			if err != nil {
				http.Error(w, v.Text.OmemoUnavailable, http.StatusServiceUnavailable)
				return
			}
			if saved.StatusCode() != 204 {
				http.Error(w, v.Text.OmemoMismatch, saved.StatusCode())
				return
			}
			http.Redirect(w, r, v.omemoHref(account, chat), http.StatusSeeOther)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := omemoSettingsPage(v, state, account, chat, preview).Render(ctx, w); err != nil {
		return
	}
}
func deviceNumber(id int) string { return fmt.Sprint(id) }
