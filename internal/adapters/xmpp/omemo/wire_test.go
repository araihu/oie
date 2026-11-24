package omemo

import (
	"encoding/xml"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifiedDirectRoundTrip(t *testing.T) {
	ctx := t.Context()
	alicePath := filepath.Join(t.TempDir(), "alice")
	alice, err := Open(alicePath)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := Open(filepath.Join(t.TempDir(), "bob"))
	if err != nil {
		t.Fatal(err)
	}
	aid, ab, err := alice.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	bid, bb, err := bob.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	afp, _ := ab.Fingerprint()
	bfp, _ := bb.Fingerprint()
	target := []Target{{"bob@example.org", bid, bb}}
	if _, err := alice.Encrypt(ctx, target, "first"); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
	if err := alice.Verify("bob@example.org", bid, bfp); err != nil {
		t.Fatal(err)
	}
	env, err := alice.Encrypt(ctx, target, "first")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := xml.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var received Envelope
	if err := xml.Unmarshal(raw, &received); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Decrypt(ctx, "alice@example.org", received); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
	if err := bob.Verify("alice@example.org", aid, afp); err != nil {
		t.Fatal(err)
	}
	damaged := received
	damaged.Payload = b64.EncodeToString([]byte("other"))
	if _, err := bob.Decrypt(ctx, "alice@example.org", damaged); err == nil {
		t.Fatal("accepted changed payload")
	}
	if got, err := bob.Decrypt(ctx, "alice@example.org", received); err != nil || got != "first" {
		t.Fatal(got, err)
	}
	reply, err := bob.Encrypt(ctx, []Target{{"alice@example.org", aid, ab}}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	alice, err = Open(alicePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := alice.Decrypt(ctx, "bob@example.org", reply); err != nil || got != "reply" {
		t.Fatal(got, err)
	}
	if err := alice.Distrust("bob@example.org", bid); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Encrypt(ctx, target, "next"); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
}

func TestPendingVerificationRollbackAndKeyChange(t *testing.T) {
	ctx := t.Context()
	alice, err := Open(filepath.Join(t.TempDir(), "alice"))
	if err != nil {
		t.Fatal(err)
	}
	bob, err := Open(filepath.Join(t.TempDir(), "bob"))
	if err != nil {
		t.Fatal(err)
	}
	aid, ab, err := alice.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	bid, bb, err := bob.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	afp, _ := ab.Fingerprint()
	bfp, _ := bb.Fingerprint()
	if err = alice.Verify("bob", bid, bfp); err != nil {
		t.Fatal(err)
	}
	msg, err := alice.Encrypt(ctx, []Target{{"bob", bid, bb}}, "before verification")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = bob.Hold("alice", "m1", msg, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := bob.Ready(); err != nil || len(ready) != 0 {
		t.Fatal("unexpected ready message", err)
	}
	observed := map[uint32]string{aid: afp}
	if err = bob.Observe("alice", observed); err != nil {
		t.Fatal(err)
	}
	if err = bob.Verify("alice", aid, afp); err != nil {
		t.Fatal(err)
	}
	ready, err := bob.Ready()
	if err != nil || len(ready) != 1 {
		t.Fatal("missing held message", err)
	}
	fail := errors.New("temporary application failure")
	if err = bob.Receive(ctx, ready[0], func(string) error { return fail }); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if err = bob.Receive(ctx, ready[0], func(text string) error {
		if text != "before verification" {
			t.Fatal(text)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ready, err = bob.Ready(); err != nil || len(ready) != 0 {
		t.Fatal("committed message still held", err)
	}
	if err = bob.Observe("alice", map[uint32]string{aid: bfp}); err != nil {
		t.Fatal(err)
	}
	if err = bob.Verify("alice", aid, afp); !errors.Is(err, ErrUnverified) {
		t.Fatal("accepted stale fingerprint", err)
	}
	_, pins, err := bob.DeviceList()
	if err != nil || len(pins) != 0 {
		t.Fatal("changed identity retained trust", err)
	}
}
