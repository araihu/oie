package omemo

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"
)

func TestWireCommitRequiresCurrentVerifiedRecipientSnapshot(t *testing.T) {
	const own = "alice@example.org"
	const peer = "bob@example.org"
	path := filepath.Join(t.TempDir(), "alice")
	device, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := Open(filepath.Join(t.TempDir(), "bob"))
	if err != nil {
		t.Fatal(err)
	}
	id, bundle, err := bob.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	fp, err := bundle.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	observe := func(address string, entries map[uint32]string) {
		t.Helper()
		if err := device.Observe(address, entries); err != nil {
			t.Fatal(err)
		}
	}
	observe(peer, map[uint32]string{id: fp})
	if err := device.Verify(peer, id, fp); err != nil {
		t.Fatal(err)
	}
	targets := []Target{{JID: peer, DeviceID: id, Bundle: bundle}}
	peers := []string{own, peer}
	if _, err := device.Encrypt(t.Context(), targets, "already encrypted"); err != nil {
		t.Fatal(err)
	}
	written := false
	if err := device.WithVerifiedTargets(t.Context(), own, peers, targets, func() {
		written = true
		operatorLock := flock.New(path + ".lock")
		defer func() { _ = operatorLock.Close() }()
		if locked, err := operatorLock.TryLock(); err != nil || locked {
			t.Errorf("operator changed the trust store during publication: locked=%t err=%v", locked, err)
		}
	}); err != nil || !written {
		t.Fatalf("valid recipient snapshot rejected: written=%t err=%v", written, err)
	}
	refuseStaleTargets := func() {
		t.Helper()
		if err := device.WithVerifiedTargets(t.Context(), own, peers, targets, func() {
			t.Error("stale recipient snapshot reached the wire")
		}); !errors.Is(err, ErrUnverified) {
			t.Fatal(err)
		}
	}
	observe(own, map[uint32]string{1: fp, 2: fp})
	refuseStaleTargets()
	observe(own, nil)
	observe(peer, nil)
	refuseStaleTargets()
	observe(peer, map[uint32]string{id: fp})
	operator, err := Existing(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.Distrust(peer, id); err != nil {
		t.Fatal(err)
	}
	refuseStaleTargets()
}
