package browserpush

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/araihu/oie/internal/adapters/hostauth"
	"github.com/araihu/oie/internal/domain"
)

func ValidSubscription(endpoint, auth, publicKey string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || u.Scheme != "https" || u.User != nil ||
		u.Fragment != "" || strings.Contains(endpoint, "#") || u.Path == "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "fcm.googleapis.com" && host != "updates.push.services.mozilla.com" &&
		!strings.HasSuffix(host, ".push.apple.com") && !strings.HasSuffix(host, ".notify.windows.com") {
		return false
	}
	encoding := base64.RawURLEncoding.Strict()
	secret, err := encoding.DecodeString(auth)
	if err != nil || len(secret) != 16 {
		return false
	}
	point, err := encoding.DecodeString(publicKey)
	if err != nil {
		return false
	}
	_, err = ecdh.P256().NewPublicKey(point)
	return err == nil
}

type notification struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Lang     string `json:"lang"`
	Navigate string `json:"navigate"`
	Tag      string `json:"tag"`
	Silent   bool   `json:"silent"`
}

func payload(origin, engineID string, message domain.Message, recipient hostauth.PushRecipient, silent bool) ([]byte, error) {
	destination := origin + "/?" + url.Values{
		"notificationEngine": {engineID},
		"account":            {string(message.AccountID)},
		"chat":               {string(message.ConversationID)},
		"message":            {string(message.ID)},
	}.Encode()
	body := "New message"
	if recipient.Language == "pt-BR" {
		body = "Nova mensagem"
	}
	return json.Marshal(struct {
		WebPush      int          `json:"web_push"`
		Notification notification `json:"notification"`
	}{
		WebPush: 8030,
		Notification: notification{
			Title: "Oiê", Body: body, Lang: recipient.Language, Navigate: destination,
			Tag: engineID + ":" + string(message.ID), Silent: silent,
		},
	})
}

type sender struct {
	origin string
	client webpush.HTTPClient
}

func (s sender) send(ctx context.Context, keys hostauth.PushKeys, recipient hostauth.PushRecipient, data []byte, messageID domain.ID) (bool, error) {
	if !ValidSubscription(recipient.Endpoint, recipient.Auth, recipient.PublicKey) {
		return true, nil
	}
	topic := sha256.Sum256([]byte(recipient.EngineID + ":" + string(messageID)))
	response, err := webpush.SendNotificationWithContext(ctx, data, &webpush.Subscription{
		Endpoint: recipient.Endpoint,
		Keys:     webpush.Keys{Auth: recipient.Auth, P256dh: recipient.PublicKey},
	}, &webpush.Options{
		HTTPClient: s.client, Subscriber: s.origin, VAPIDPublicKey: keys.Public, VAPIDPrivateKey: keys.Private,
		TTL: 300, Topic: base64.RawURLEncoding.EncodeToString(topic[:24]), Urgency: webpush.UrgencyHigh,
	})
	if err != nil {
		return false, err
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusGone || response.StatusCode == http.StatusNotFound, nil
}
