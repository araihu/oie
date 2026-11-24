package omemo

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestDisablingRoomEncryptionReleasesItsPendingCiphertext(t *testing.T) {
	const room = "private@rooms.example.org"
	const otherRoom = "another@rooms.example.org"
	const peer = "alice@example.org"
	d, err := Open(filepath.Join(t.TempDir(), "device.json"))
	if err != nil {
		t.Fatal(err)
	}
	id, fingerprint, err := d.LocalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{room, otherRoom} {
		if err := d.SetRoomEncryption(address, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{room, otherRoom, ""} {
		message := Pending{Peer: peer, Room: address, Envelope: Envelope{Header: Header{From: 42, Keys: []WrappedKey{{To: id}}}}}
		if err := d.HoldPending(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetRoomEncryption(room, false); err != nil {
		t.Fatal(err)
	}
	late := Pending{Peer: peer, Room: room, Envelope: Envelope{Header: Header{From: 42, Keys: []WrappedKey{{To: id}}}}}
	if err := d.HoldPending(late); !errors.Is(err, ErrInvalid) {
		t.Fatalf("late receive repopulated the disabled room: %v", err)
	}
	if err := d.Verify(peer, 42, fingerprint); err != nil {
		t.Fatal(err)
	}
	ready, err := d.Ready()
	if err != nil {
		t.Fatal(err)
	}
	var rooms []string
	for _, message := range ready {
		rooms = append(rooms, message.Room)
	}
	if !slices.Equal(rooms, []string{otherRoom, ""}) {
		t.Fatalf("unexpected held rooms after disabling %s: %v", room, rooms)
	}
}

func TestRoomReceiveRejectsDisabledPolicyAndStalePendingEntry(t *testing.T) {
	const room = "private@rooms.example.org"
	const peer = "alice@example.org"
	const recipient = "bob@example.org"
	alice, err := Open(filepath.Join(t.TempDir(), "alice"))
	if err != nil {
		t.Fatal(err)
	}
	bob, err := Open(filepath.Join(t.TempDir(), "bob"))
	if err != nil {
		t.Fatal(err)
	}
	aid, afp, err := alice.LocalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	bid, bundle, err := bob.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	bfp, err := bundle.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.Verify(recipient, bid, bfp); err != nil {
		t.Fatal(err)
	}
	if err := bob.Verify(peer, aid, afp); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	envelope, err := alice.Encrypt(ctx, []Target{{JID: recipient, DeviceID: bid, Bundle: bundle}}, "private text")
	if err != nil {
		t.Fatal(err)
	}
	incoming := Pending{Peer: peer, Room: room, Envelope: envelope}
	setPolicy := func(enabled bool) {
		t.Helper()
		if err := bob.SetRoomEncryption(room, enabled); err != nil {
			t.Fatal(err)
		}
	}
	hold := func() Pending {
		t.Helper()
		if err := bob.HoldPending(incoming); err != nil {
			t.Fatal(err)
		}
		ready, err := bob.Ready()
		if err != nil || len(ready) != 1 {
			t.Fatalf("expected one held envelope, got %d: %v", len(ready), err)
		}
		return ready[0]
	}
	unexpected := func(string) error {
		t.Error("disabled or stale envelope reached the consumer")
		return nil
	}
	setPolicy(true)
	stale := hold()
	setPolicy(false)
	for _, p := range []Pending{incoming, stale} {
		if err := bob.Receive(ctx, p, unexpected); !errors.Is(err, ErrRoomEncryptionDisabled) {
			t.Fatalf("receive while disabled: %v", err)
		}
	}
	setPolicy(true)
	current := hold()
	if current.Digest != stale.Digest || current.ID == stale.ID {
		t.Fatal("the same envelope must have the same digest but a new queue identity")
	}
	if err := bob.Receive(ctx, stale, unexpected); !errors.Is(err, ErrInvalid) {
		t.Fatalf("receive of a replaced entry: %v", err)
	}
	if err := bob.Discard(stale); err != nil {
		t.Fatal(err)
	}
	if err := bob.Receive(ctx, current, func(text string) error {
		if text != "private text" {
			t.Fatalf("unexpected plaintext %q", text)
		}
		return nil
	}); err != nil {
		t.Fatalf("stale copy interfered with the current entry: %v", err)
	}
}
