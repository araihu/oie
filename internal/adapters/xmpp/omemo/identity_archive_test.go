package omemo

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityArchiveBindsAccountAndRetainsFreshSessions(t *testing.T) {
	alice, err := Open(filepath.Join(t.TempDir(), "alice"))
	if err != nil {
		t.Fatal(err)
	}
	var file bytes.Buffer
	const owner = "alice@example.test"
	const password = "fixture passphrase"
	if err := alice.ExportIdentity(&file, owner, password); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file.Bytes(), []byte(owner)) {
		t.Fatal("archive disclosed account")
	}
	info, err := InspectIdentity(bytes.NewReader(file.Bytes()), password)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "device")
	for _, restore := range []struct {
		data              []byte
		password, address string
	}{{file.Bytes(), "wrong", owner}, {file.Bytes(), password, "bob@example.test"}, {file.Bytes()[:file.Len()-1], password, owner}} {
		if err := RestoreIdentity(bytes.NewReader(restore.data), restore.password, target, restore.address, info.Fingerprint); err == nil {
			t.Fatal("invalid import succeeded")
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if err := RestoreIdentity(bytes.NewReader(file.Bytes()), password, target, owner, info.Fingerprint); err != nil {
		t.Fatal(err)
	}
	restored, err := Existing(target)
	if err != nil {
		t.Fatal(err)
	}
	id, fp, err := restored.LocalIdentity()
	if err != nil || id != info.DeviceID || fp != info.Fingerprint {
		t.Fatal("identity changed", err)
	}
	_, oldBundle, _ := alice.Bundle()
	_, freshBundle, _ := restored.Bundle()
	if oldBundle.Signed.Value == freshBundle.Signed.Value {
		t.Fatal("restored stale signed prekey")
	}
}
