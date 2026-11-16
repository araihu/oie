package browserpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/hostauth"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

const engineID = "019a5df0-0000-7000-8000-000000000001"

type testRegistry struct {
	keys       hostauth.PushKeys
	recipients []hostauth.PushRecipient
	expired    []string
}

func (r *testRegistry) PushRecipients(string) (hostauth.PushKeys, []hostauth.PushRecipient, error) {
	return r.keys, r.recipients, nil
}

func (r *testRegistry) ForgetPush(_ context.Context, recipient hostauth.PushRecipient) error {
	r.expired = append(r.expired, recipient.SessionID)
	return nil
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type testSource struct {
	eventSource
	message      domain.Message
	conversation domain.Conversation
	presence     string
	unread       int
}

func (s testSource) Message(context.Context, domain.ID) (domain.Message, error) {
	return s.message, nil
}
func (s testSource) Conversation(context.Context, domain.ID) (domain.Conversation, error) {
	return s.conversation, nil
}
func (s testSource) ConversationReadState(context.Context, domain.ID) (domain.ReadState, error) {
	return domain.ReadState{UnreadCount: s.unread}, nil
}
func (s testSource) AccountCommunicationPreferences(context.Context, domain.ID) (domain.AccountCommunicationPreferences, error) {
	return domain.AccountCommunicationPreferences{Presence: s.presence}, nil
}

func testKey(t *testing.T) hostauth.PushKeys {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoding := base64.RawURLEncoding
	return hostauth.PushKeys{Public: encoding.EncodeToString(key.PublicKey().Bytes()), Private: encoding.EncodeToString(key.Bytes())}
}

func TestDeliverySkipsIneligibleDevicesAndRemovesExpiredEndpoint(t *testing.T) {
	message := domain.Message{
		ID:             "019a5df0-0000-7000-8000-000000000002",
		AccountID:      "019a5df0-0000-7000-8000-000000000003",
		ConversationID: "019a5df0-0000-7000-8000-000000000004",
		OriginalText:   "private text", Sender: domain.Sender{DisplayName: "private sender"}, SendState: "received",
	}
	var calls []string
	registered := &testRegistry{keys: testKey(t)}
	for _, id := range []string{"ready", "gone", "focused", "muted", "new"} {
		entry := hostauth.PushRecipient{SessionID: id, PushSubscription: hostauth.PushSubscription{
			Endpoint: "https://web.push.apple.com/" + id, Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
			PublicKey: testKey(t).Public, EngineID: engineID, Language: "pt-BR", Sound: true, DirectSound: true,
		}}
		if id == "muted" {
			entry.MutedConversations = []string{string(message.ConversationID)}
		}
		if id == "new" {
			entry.StartedAt = time.Now()
		}
		registered.recipients = append(registered.recipients, entry)
	}
	service := &Service{auth: registered, claims: make(map[deliveryKey]deliveryClaim), sender: sender{
		origin: "https://oie.example",
		client: transportFunc(func(request *http.Request) (*http.Response, error) {
			calls = append(calls, request.URL.Path)
			if request.Method != http.MethodPost || request.Header.Get("Content-Encoding") != "aes128gcm" ||
				!strings.HasPrefix(request.Header.Get("Authorization"), "vapid t=") || request.Header.Get("TTL") != "300" {
				t.Fatalf("invalid Web Push request: %v %v", request.Method, request.Header)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil || len(body) == 0 || bytes.Contains(body, []byte("Nova mensagem")) {
				t.Fatalf("payload was not encrypted: %v", err)
			}
			status := http.StatusCreated
			if request.URL.Path == "/gone" {
				status = http.StatusGone
			}
			return &http.Response{StatusCode: status, Body: http.NoBody}, nil
		}),
	}}
	if !service.ClaimForeground("focused", engineID, string(message.ID)) {
		t.Fatal("foreground delivery unable to claim an unsent message")
	}
	source := testSource{message: message, conversation: domain.Conversation{
		ID: message.ConversationID, AccountID: message.AccountID, Kind: "direct",
	}, unread: 1}
	job := delivery{message.ID, time.Now().Add(-2 * time.Second)}
	service.deliver(t.Context(), source, engineID, job)
	if strings.Join(calls, ",") != "/ready,/gone" || strings.Join(registered.expired, ",") != "gone" {
		t.Fatalf("delivery attempts %v, expired endpoints %v", calls, registered.expired)
	}
	if service.ClaimForeground("ready", engineID, string(message.ID)) {
		t.Fatal("foreground delivery would duplicate an issued Web Push")
	}
	service.claims = make(map[deliveryKey]deliveryClaim)
	source.presence = "dnd"
	service.deliver(t.Context(), source, engineID, job)
	source.presence, source.unread = "", 0
	service.deliver(t.Context(), source, engineID, job)
	source.unread, source.message.Retraction = 1, &domain.MessageRetraction{}
	service.deliver(t.Context(), source, engineID, job)
	if len(calls) != 2 {
		t.Fatalf("sent an alert for a DND account, read conversation or retracted message: %v", calls)
	}

	data, err := payload("https://oie.example", engineID, message, registered.recipients[0], false)
	if err != nil || bytes.Contains(data, []byte("private")) {
		t.Fatalf("notification exposed message details: %s (%v)", data, err)
	}
	var decoded struct {
		WebPush      int          `json:"web_push"`
		Notification notification `json:"notification"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(decoded.Notification.Navigate)
	if err != nil || link.Host != "oie.example" || link.Query().Get("message") != string(message.ID) ||
		link.Query().Get("notificationEngine") != engineID || decoded.WebPush != 8030 {
		t.Fatalf("invalid declarative notification: %s (%v)", data, err)
	}
}

func TestLiveEventEligibility(t *testing.T) {
	event := application.Event{Type: "message.created", Message: &domain.Message{SendState: "received"}}
	if !isLiveMessage(event) {
		t.Fatal("new incoming message was ineligible")
	}
	event.Message.Historical = true
	if isLiveMessage(event) {
		t.Fatal("historical message was eligible")
	}
	event.Message.Historical, event.Message.IsOwn = false, true
	if isLiveMessage(event) {
		t.Fatal("own message was eligible")
	}
	event.Message.IsOwn, event.Type = false, "message.updated"
	if isLiveMessage(event) {
		t.Fatal("message edit was eligible")
	}
}

func TestPushEndpointAndEncryptionKeyValidation(t *testing.T) {
	keys := testKey(t)
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	if !ValidSubscription("https://web.push.apple.com/endpoint", auth, keys.Public) {
		t.Fatal("valid Apple subscription rejected")
	}
	for _, endpoint := range []string{
		"http://web.push.apple.com/endpoint",
		"https://web.push.apple.com:444/endpoint",
		"https://web.push.apple.com.attacker.example/endpoint",
		"https://web.push.apple.com@127.0.0.1/endpoint",
		"https://169.254.169.254/latest/meta-data/",
		"https://web.push.apple.com/endpoint#",
	} {
		if ValidSubscription(endpoint, auth, keys.Public) {
			t.Errorf("accepted invalid endpoint %q", endpoint)
		}
	}
	if ValidSubscription("https://web.push.apple.com/endpoint", auth, keys.Private) {
		t.Fatal("accepted an invalid ECDH point")
	}
}
