package hostauth

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"time"
)

type PushSubscription struct {
	Endpoint, Auth, PublicKey, EngineID, Language string
	MutedConversations                            []string
	RoomSound                                     string
	StartedAt                                     time.Time
	Sound, DirectSound, ArchivedSound             bool
}

var ErrPushChanged = errors.New("browser push subscription changed")

type PushKeys struct {
	Public, Private string
}

type PushRecipient struct {
	SessionID string
	PushSubscription
}

// BrowserSessionID returns the verifier, never the bearer value in the cookie.
// Call it only inside Browser's authenticated handler.
func BrowserSessionID(r *http.Request) string {
	if !BrowserRequest(r.Context()) {
		return ""
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return verifier(cookie.Value)
}

func (a *Auth) BrowserPushStatus(ctx context.Context, sessionID, engineID string) (string, string, error) {
	current, err := a.store.read()
	if err != nil {
		return "", "", err
	}
	index := current.activeSessionIndex(sessionID)
	if index < 0 {
		return "", "", errUnauthorized
	}
	keys := current.PushKeys
	if keys == nil {
		err = a.store.update(ctx, false, func(current *state) error {
			if current.activeSessionIndex(sessionID) < 0 {
				return errUnauthorized
			}
			if current.PushKeys == nil {
				key, err := ecdh.P256().GenerateKey(rand.Reader)
				if err != nil {
					return err
				}
				encoding := base64.RawURLEncoding
				current.PushKeys = &PushKeys{encoding.EncodeToString(key.PublicKey().Bytes()), encoding.EncodeToString(key.Bytes())}
			}
			keys = current.PushKeys
			return nil
		})
		if err != nil {
			return "", "", err
		}
	}
	push := current.Sessions[index].Push
	if push != nil && push.EngineID == engineID {
		return keys.Public, push.Language, nil
	}
	return keys.Public, "", nil
}

func (a *Auth) SaveBrowserPush(ctx context.Context, sessionID string, push *PushSubscription, existing bool) error {
	return a.store.update(ctx, false, func(current *state) error {
		index := current.activeSessionIndex(sessionID)
		if index < 0 {
			return errUnauthorized
		}
		if push != nil {
			previous := current.Sessions[index].Push
			same := previous != nil && previous.EngineID == push.EngineID && previous.Endpoint == push.Endpoint
			if existing && !same {
				return ErrPushChanged
			}
			if same {
				push.StartedAt = previous.StartedAt
			} else {
				push.StartedAt = time.Now().UTC()
			}
			for i := range current.Sessions {
				other := current.Sessions[i].Push
				if i != index && other != nil && other.Endpoint == push.Endpoint {
					current.Sessions[i].Push = nil
				}
			}
		}
		current.Sessions[index].Push = push
		return nil
	})
}

func (a *Auth) PushRecipients(engineID string) (PushKeys, []PushRecipient, error) {
	current, err := a.store.read()
	if err != nil || current.PushKeys == nil {
		return PushKeys{}, nil, err
	}
	var recipients []PushRecipient
	now := time.Now()
	for _, session := range current.Sessions {
		if session.Push != nil && session.Push.EngineID == engineID && now.Before(session.ExpiresAt) {
			recipients = append(recipients, PushRecipient{session.Verifier, *session.Push})
		}
	}
	return *current.PushKeys, recipients, nil
}

// ForgetPush removes an expired endpoint only if it is still registered for
// that session. A stale delivery cannot clear a replacement subscription.
func (a *Auth) ForgetPush(ctx context.Context, recipient PushRecipient) error {
	return a.store.update(ctx, false, func(current *state) error {
		index := current.activeSessionIndex(recipient.SessionID)
		if index >= 0 {
			push := current.Sessions[index].Push
			if push != nil && push.Endpoint == recipient.Endpoint && push.Auth == recipient.Auth &&
				push.PublicKey == recipient.PublicKey && push.EngineID == recipient.EngineID {
				current.Sessions[index].Push = nil
			}
		}
		return nil
	})
}

func (s state) activeSessionIndex(sessionID string) int {
	now := time.Now()
	return slices.IndexFunc(s.Sessions, func(item session) bool {
		return item.Verifier == sessionID && now.Before(item.ExpiresAt)
	})
}
