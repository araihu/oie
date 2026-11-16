package hostauth

import (
	"errors"
	"testing"
	"time"
)

func TestPushFollowsTheBrowserSession(t *testing.T) {
	ctx := t.Context()
	store := NewStore(t.TempDir())
	owner := Owner{Issuer: "https://id.example", Subject: "owner"}
	if err := store.Initialize(ctx, owner); err != nil {
		t.Fatal(err)
	}
	current, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.createSession(ctx, current.Generation)
	if err != nil {
		t.Fatal(err)
	}
	auth := &Auth{store: store}
	id := verifier(session)
	publicKey, language, err := auth.BrowserPushStatus(ctx, id, "engine")
	if err != nil || publicKey == "" || language != "" {
		t.Fatalf("initial push status: %q, %v", language, err)
	}
	subscription := &PushSubscription{EngineID: "engine", Language: "en", Endpoint: "https://web.push.apple.com/subscription"}
	if err := auth.SaveBrowserPush(ctx, id, subscription, false); err != nil {
		t.Fatal(err)
	}
	_, recipients, err := auth.PushRecipients("engine")
	if err != nil || len(recipients) != 1 || recipients[0].SessionID != id {
		t.Fatalf("registered session unavailable: %v", err)
	}
	started := recipients[0].StartedAt
	updated := *subscription
	updated.Language = "pt-BR"
	if err := auth.SaveBrowserPush(ctx, id, &updated, true); err != nil {
		t.Fatal(err)
	}
	_, recipients, err = auth.PushRecipients("engine")
	if err != nil || len(recipients) != 1 || recipients[0].Language != "pt-BR" || !recipients[0].StartedAt.Equal(started) {
		t.Fatalf("preference update reset the subscription: %v", err)
	}
	_, recipients, err = auth.PushRecipients("different-engine")
	if err != nil || len(recipients) != 0 {
		t.Fatalf("wrong engine has push recipients: %v", err)
	}
	if err := auth.SaveBrowserPush(ctx, id, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveBrowserPush(ctx, id, &updated, true); !errors.Is(err, ErrPushChanged) {
		t.Fatalf("preference update restored removed subscription: %v", err)
	}
	if err := auth.SaveBrowserPush(ctx, id, subscription, false); err != nil {
		t.Fatal(err)
	}
	if err := store.revokeSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	_, recipients, err = auth.PushRecipients("engine")
	if err != nil || len(recipients) != 0 {
		t.Fatalf("signed-out session still subscribed: %v", err)
	}
	next, err := store.createSession(ctx, current.Generation)
	if err != nil {
		t.Fatal(err)
	}
	retainedKey, language, err := auth.BrowserPushStatus(ctx, verifier(next), "engine")
	if err != nil || retainedKey != publicKey || language != "" {
		t.Fatalf("next sign-in changed the server key or retained opt-in: %v", err)
	}
	if err := auth.SaveBrowserPush(ctx, verifier(next), subscription, false); err != nil {
		t.Fatal(err)
	}
	if err := store.update(ctx, false, func(current *state) error {
		current.Sessions[0].ExpiresAt = time.Now().Add(-time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, recipients, err = auth.PushRecipients("engine")
	if err != nil || len(recipients) != 0 {
		t.Fatalf("expired session still subscribed: %v", err)
	}
}
