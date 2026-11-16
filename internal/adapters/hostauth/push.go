package hostauth

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"
)

type PushSubscription struct {
	Endpoint, Auth, PublicKey, EngineID, Language string
	MutedConversations                            []string
	RoomSound                                     string
	StartedAt, BoundAt                            time.Time
	Revision                                      string
	Sound, DirectSound, ArchivedSound             bool
	Expired                                       bool `json:"expired,omitempty"`
}

var ErrPushChanged = errors.New("browser push subscription changed")

type PushKeys struct {
	Public, Private string
}

type PushRecipient struct {
	SessionID string
	PushSubscription
}

type PushStatus struct {
	PublicKey, Language, SubscriptionTag string
	Expired                              bool
}

func (p *PushSubscription) tag() string {
	hash := sha256.Sum256([]byte(p.Endpoint + "\n" + p.Auth + "\n" + p.PublicKey))
	revision := p.Revision
	if revision == "" {
		revision = strconv.FormatInt(p.StartedAt.UnixNano(), 10)
	}
	return hex.EncodeToString(hash[:]) + "." + revision
}

func (p *PushSubscription) boundAt() time.Time {
	if p.BoundAt.IsZero() {
		return p.StartedAt
	}
	return p.BoundAt
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

func (a *Auth) BrowserPushStatus(ctx context.Context, sessionID, engineID string) (PushStatus, error) {
	current, err := a.store.read()
	if err != nil {
		return PushStatus{}, err
	}
	index := current.activeSessionIndex(sessionID)
	if index < 0 {
		return PushStatus{}, errUnauthorized
	}
	keys := current.PushKeys
	push := current.Sessions[index].Push
	if keys == nil {
		err = a.store.update(ctx, false, func(current *state) error {
			index := current.activeSessionIndex(sessionID)
			if index < 0 {
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
			push = current.Sessions[index].Push
			return nil
		})
		if err != nil {
			return PushStatus{}, err
		}
	}
	status := PushStatus{PublicKey: keys.Public}
	if push != nil && push.EngineID == engineID {
		status.Language = push.Language
		status.SubscriptionTag = push.tag()
		status.Expired = push.Expired
	}
	return status, nil
}

func (a *Auth) SaveBrowserPush(ctx context.Context, sessionID string, push *PushSubscription, expected string) error {
	return a.store.update(ctx, false, func(current *state) error {
		index := current.activeSessionIndex(sessionID)
		if index < 0 {
			return errUnauthorized
		}
		previous := current.Sessions[index].Push
		if expected != "" && (previous == nil || previous.tag() != expected) {
			return ErrPushChanged
		}
		if push != nil {
			same := previous != nil && previous.EngineID == push.EngineID && previous.Endpoint == push.Endpoint &&
				previous.Auth == push.Auth && previous.PublicKey == push.PublicKey
			if same && previous.Expired {
				return ErrPushChanged
			}
			now := time.Now().UTC()
			push.StartedAt, push.BoundAt = now, now
			push.Revision = newCredential()
			if previous != nil && previous.EngineID == push.EngineID && (same || expected != "") {
				push.StartedAt = previous.StartedAt
			}
			if same {
				push.BoundAt = previous.boundAt()
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
		if session.Push != nil && !session.Push.Expired && session.Push.EngineID == engineID && now.Before(session.ExpiresAt) {
			recipients = append(recipients, PushRecipient{session.Verifier, *session.Push})
		}
	}
	return *current.PushKeys, recipients, nil
}

func (a *Auth) CurrentPushRecipient(engineID, sessionID string) (*PushRecipient, error) {
	current, err := a.store.read()
	if err != nil {
		return nil, err
	}
	index := current.activeSessionIndex(sessionID)
	if index < 0 {
		return nil, nil
	}
	push := current.Sessions[index].Push
	if push == nil || push.Expired || push.EngineID != engineID {
		return nil, nil
	}
	return &PushRecipient{sessionID, *push}, nil
}

// ExpirePush stops delivery to an expired endpoint while preserving consent
// for browser renewal. A stale delivery cannot disable a replacement endpoint.
func (a *Auth) ExpirePush(ctx context.Context, recipient PushRecipient) error {
	return a.store.update(ctx, false, func(current *state) error {
		index := current.activeSessionIndex(recipient.SessionID)
		if index >= 0 {
			push := current.Sessions[index].Push
			if push != nil && push.Endpoint == recipient.Endpoint && push.Auth == recipient.Auth &&
				push.PublicKey == recipient.PublicKey && push.EngineID == recipient.EngineID &&
				!push.Expired && push.boundAt().Equal(recipient.boundAt()) {
				push.Expired = true
				push.Revision = newCredential()
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
