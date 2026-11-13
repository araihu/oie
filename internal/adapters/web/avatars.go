package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

type avatarIdentity struct{ Account, Address string }

func validAvatarHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (v view) avatarSource(account, address string) string {
	return avatarImageSource(v.Avatars[avatarIdentity{account, address}])
}

func avatarImageSource(hash string) string {
	if !validAvatarHash(hash) {
		return ""
	}
	return "/ui/avatars/" + hash
}

// A room nickname is not a verified peer address. Only a direct conversation
// establishes the remote sender identity used for an incoming message photo.
func (v view) messageAvatarAddress(m messageView) string {
	if v.Selected == nil {
		return ""
	}
	if m.IsOwn {
		return v.account(m.AccountId).Address
	}
	if v.Selected.Kind == "direct" {
		return v.Selected.Address
	}
	return ""
}

func (v view) messageAvatarBorder(m messageView) string {
	var scope string
	if v.Selected != nil {
		scope = v.Selected.Address
	}
	return senderAvatarBorder(m.Message, v.messageAvatarAddress(m), scope)
}

func searchSenderAvatarBorder(hit engineapi.MessageSearchHit) string {
	var identity, scope string
	if hit.SenderAddress != nil {
		identity = *hit.SenderAddress
	}
	if hit.ConversationAddress != nil {
		scope = *hit.ConversationAddress
	}
	return senderAvatarBorder(hit.Message, identity, scope)
}

// Room nicknames are scoped to their room for color only, never photo lookup.
func senderAvatarBorder(m engineapi.Message, identity, scope string) string {
	if identity == "" {
		if scope == "" {
			scope = m.ConversationId
		}
		identity = scope + "/" + m.Sender.DisplayName
	}
	colors := [...]string{
		"oie-avatar-user-blue", "oie-avatar-user-purple", "oie-avatar-user-pink", "oie-avatar-user-orange",
		"oie-avatar-user-olive", "oie-avatar-user-green", "oie-avatar-user-teal", "oie-avatar-user-indigo",
	}
	hash := sha256.Sum256([]byte(identity))
	return colors[int(hash[0])%len(colors)]
}

func (s *Server) loadAvatars(ctx context.Context, v *view, account string) {
	limit := 100
	params := &engineapi.ListAccountAvatarsParams{Limit: &limit}
	found := map[avatarIdentity]string{}
	seen := map[string]bool{}
	for {
		page, err := s.client.ListAccountAvatarsWithResponse(ctx, account, params)
		if err != nil || page.JSON200 == nil {
			return
		}
		for _, a := range page.JSON200.Items {
			if a.AccountId == account && a.Address != "" && (a.ImageHash == "" || validAvatarHash(a.ImageHash)) {
				found[avatarIdentity{account, a.Address}] = a.ImageHash
			}
		}
		next := page.JSON200.NextCursor
		if next == nil {
			break
		}
		if *next == "" || seen[*next] {
			return
		}
		seen[*next] = true
		params.After = next
	}
	for identity, hash := range found {
		v.Avatars[identity] = hash
	}
	v.AvatarAccounts = append(v.AvatarAccounts, account)
}

// avatarImage serves only retained, hash-verified PNGs from the configured
// engine. Provider addresses and response headers never become browser URLs.
func (s *Server) avatarImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	hash := strings.TrimPrefix(r.URL.Path, "/ui/avatars/")
	if !validAvatarHash(hash) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	params := &engineapi.GetAvatarImageParams{}
	if value := r.Header.Get("If-None-Match"); value != "" {
		params.IfNoneMatch = &value
	}
	response, err := s.client.GetAvatarImage(r.Context(), hash, params)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = response.Body.Close() }()
	cache := func() {
		w.Header().Set("ETag", `"`+hash+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	if response.StatusCode == http.StatusNotModified && params.IfNoneMatch != nil {
		cache()
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusNotFound {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		return
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "image/png" {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	const maxImageBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil || len(body) > maxImageBytes {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	config, err := png.DecodeConfig(bytes.NewReader(body))
	digest := sha256.Sum256(body)
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 256 || config.Height > 256 || hex.EncodeToString(digest[:]) != hash {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	cache()
	w.Header().Set("Content-Type", "image/png")
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
