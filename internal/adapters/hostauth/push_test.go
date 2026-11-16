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
	initial, err := auth.BrowserPushStatus(ctx, id, "engine")
	if err != nil || initial.PublicKey == "" || initial.Language != "" || initial.SubscriptionTag != "" {
		t.Fatalf("initial push status: %+v, %v", initial, err)
	}
	subscription := &PushSubscription{EngineID: "engine", Language: "en", Endpoint: "https://web.push.apple.com/subscription"}
	if err := auth.SaveBrowserPush(ctx, id, subscription, ""); err != nil {
		t.Fatal(err)
	}
	_, recipients, err := auth.PushRecipients("engine")
	if err != nil || len(recipients) != 1 || recipients[0].SessionID != id {
		t.Fatalf("registered session unavailable: %v", err)
	}
	original := recipients[0]
	started := original.StartedAt
	tag := original.tag()
	updated := *subscription
	updated.Language = "pt-BR"
	if err := auth.SaveBrowserPush(ctx, id, &updated, tag); err != nil {
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
	if err := auth.SaveBrowserPush(ctx, id, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveBrowserPush(ctx, id, &updated, tag); !errors.Is(err, ErrPushChanged) {
		t.Fatalf("preference update restored removed subscription: %v", err)
	}
	if err := auth.SaveBrowserPush(ctx, id, subscription, ""); err != nil {
		t.Fatal(err)
	}
	if err := auth.ExpirePush(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveBrowserPush(ctx, id, nil, tag); !errors.Is(err, ErrPushChanged) {
		t.Fatalf("stale removal removed a replacement subscription: %v", err)
	}
	replacement, err := auth.CurrentPushRecipient("engine", id)
	if err != nil || replacement == nil || replacement.StartedAt.Equal(started) {
		t.Fatalf("stale provider expiration cleared a replacement subscription: %+v (%v)", replacement, err)
	}
	if err := auth.ExpirePush(ctx, *replacement); err != nil {
		t.Fatal(err)
	}
	expired, err := auth.BrowserPushStatus(ctx, id, "engine")
	if err != nil || !expired.Expired || expired.SubscriptionTag == "" {
		t.Fatalf("expiration discarded consent for renewal: %+v (%v)", expired, err)
	}
	if recipient, err := auth.CurrentPushRecipient("engine", id); err != nil || recipient != nil {
		t.Fatalf("expired endpoint was still eligible: %+v (%v)", recipient, err)
	}
	renewed := *subscription
	renewed.Endpoint += "-renewed"
	if err := auth.SaveBrowserPush(ctx, id, &renewed, expired.SubscriptionTag); err != nil {
		t.Fatal(err)
	}
	if recipient, err := auth.CurrentPushRecipient("engine", id); err != nil || recipient == nil || recipient.Endpoint != renewed.Endpoint {
		t.Fatalf("renewal did not restore delivery: %+v (%v)", recipient, err)
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
	retained, err := auth.BrowserPushStatus(ctx, verifier(next), "engine")
	if err != nil || retained.PublicKey != initial.PublicKey || retained.SubscriptionTag != "" {
		t.Fatalf("next sign-in changed the server key or retained opt-in: %v", err)
	}
	if err := auth.SaveBrowserPush(ctx, verifier(next), subscription, ""); err != nil {
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
