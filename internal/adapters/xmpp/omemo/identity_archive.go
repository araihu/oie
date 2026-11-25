package omemo

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/adapters/securearchive"
	"github.com/gofrs/flock"
	"go.mau.fi/libsignal/util/keyhelper"
	"mellium.im/xmpp/jid"
)

type IdentityInfo struct {
	Address     string `json:"address"`
	DeviceID    uint32 `json:"deviceId"`
	Fingerprint string `json:"fingerprint"`
}
type identityArchive struct {
	IdentityInfo
	Secret []byte `json:"secret"`
}

func (d *Device) ExportIdentity(dst io.Writer, address, password string) error {
	s, err := d.read()
	if err != nil {
		return err
	}
	archive := identityArchive{IdentityInfo{address, s.DeviceID, fingerprint(s.GetIdentityKeyPair().PublicKey())}, s.Secret}
	return securearchive.Seal(dst, securearchive.Identity, password, func(w io.Writer) error { return json.NewEncoder(w).Encode(archive) })
}
func unlockIdentity(src io.Reader, password string) (identityArchive, error) {
	var a identityArchive
	r, err := securearchive.Open(src, securearchive.Identity, password)
	if err != nil {
		return a, err
	}
	const max = 4096
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return a, err
	}
	if len(data) > max || json.Unmarshal(data, &a) != nil || len(a.Secret) != 32 || a.DeviceID == 0 || a.DeviceID > 0x7fffffff {
		return a, ErrInvalid
	}
	j, err := jid.Parse(a.Address)
	s := state{Secret: a.Secret}
	if err != nil || j.Localpart() == "" || j.Bare().String() != a.Address || fingerprint(s.GetIdentityKeyPair().PublicKey()) != a.Fingerprint {
		return a, ErrInvalid
	}
	return a, nil
}
func InspectIdentity(src io.Reader, password string) (IdentityInfo, error) {
	a, err := unlockIdentity(src, password)
	return a.IdentityInfo, err
}

// RestoreIdentity retains only the long-term identity. Regenerating prekeys
// and negotiating new sessions avoids rewinding a live or archived ratchet.
func RestoreIdentity(src io.Reader, password, path, address, confirmed string) error {
	a, err := unlockIdentity(src, password)
	if err != nil {
		return err
	}
	if a.Address != address || a.Fingerprint != confirmed {
		return errors.New("account or confirmed fingerprint does not match the archive")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock := flock.New(path + ".lock")
	if err = lock.Lock(); err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	d := &Device{path: path}
	if existing, err := d.read(); err == nil {
		if existing.DeviceID == a.DeviceID && fingerprint(existing.GetIdentityKeyPair().PublicKey()) == a.Fingerprint {
			return nil
		}
		return errors.New("the account already has a different local identity")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s := &state{Version: 1, DeviceID: a.DeviceID, Secret: a.Secret, NextPreKey: 1, PreKeys: map[uint32][]byte{}, Sessions: map[string][]byte{}, Verified: map[string]string{}}
	signed, err := keyhelper.GenerateSignedPreKey(s.GetIdentityKeyPair(), 1, serializer.SignedPreKeyRecord)
	if err != nil {
		return err
	}
	s.Signed = signed.Serialize()
	if err = s.replenish(); err != nil {
		return err
	}
	return d.write(s)
}
