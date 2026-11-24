package omemo

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"math/big"
	"slices"
	"unicode/utf8"

	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/libsignal/keys/identity"
	"go.mau.fi/libsignal/keys/prekey"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/libsignal/util/optional"
)

const Namespace = "eu.siacs.conversations.axolotl"

type PublicPreKey struct {
	ID    uint32 `xml:"preKeyId,attr"`
	Value string `xml:",chardata"`
}
type SignedPublicKey struct {
	ID    uint32 `xml:"signedPreKeyId,attr"`
	Value string `xml:",chardata"`
}
type Bundle struct {
	XMLName   xml.Name        `xml:"eu.siacs.conversations.axolotl bundle"`
	Identity  string          `xml:"eu.siacs.conversations.axolotl identityKey"`
	Signed    SignedPublicKey `xml:"eu.siacs.conversations.axolotl signedPreKeyPublic"`
	Signature string          `xml:"eu.siacs.conversations.axolotl signedPreKeySignature"`
	PreKeys   []PublicPreKey  `xml:"prekeys>preKeyPublic"`
}
type Target struct {
	JID      string
	DeviceID uint32
	Bundle   Bundle
}
type WrappedKey struct {
	To     uint32 `xml:"rid,attr"`
	PreKey bool   `xml:"prekey,attr,omitempty"`
	Value  string `xml:",chardata"`
}
type Header struct {
	From uint32       `xml:"sid,attr"`
	Keys []WrappedKey `xml:"eu.siacs.conversations.axolotl key"`
	IV   string       `xml:"eu.siacs.conversations.axolotl iv"`
}
type Envelope struct {
	XMLName xml.Name `xml:"eu.siacs.conversations.axolotl encrypted"`
	Header  Header   `xml:"eu.siacs.conversations.axolotl header"`
	Payload string   `xml:"eu.siacs.conversations.axolotl payload"`
}

// ProviderID identifies the same authenticated envelope in live delivery and MAM.
func (e Envelope) ProviderID() string {
	e.XMLName = xml.Name{}
	raw, _ := json.Marshal(e)
	digest := sha256.Sum256(raw)
	return "omemo:" + hex.EncodeToString(digest[:])
}

func (e Envelope) Size() int {
	size := len(e.Payload) + len(e.Header.IV)
	for _, key := range e.Header.Keys {
		size += len(key.Value) + 16
	}
	return size
}

var b64 = base64.StdEncoding

func wirePoint(value string) (ecc.ECPublicKeyable, error) {
	raw, err := b64.DecodeString(value)
	if err != nil || len(raw) != 33 || raw[0] != ecc.DjbType {
		return nil, ErrInvalid
	}
	return ecc.DecodePoint(raw, 0)
}
func (b Bundle) Fingerprint() (string, error) {
	pub, err := wirePoint(b.Identity)
	if err != nil {
		return "", err
	}
	raw := pub.PublicKey()
	return hex.EncodeToString(raw[:]), nil
}
func (d *Device) Bundle() (uint32, Bundle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.read()
	if err != nil {
		return 0, Bundle{}, err
	}
	keys, err := s.LoadSignedPreKeys(context.Background())
	if err != nil {
		return 0, Bundle{}, err
	}
	spk := keys[0]
	sig := spk.Signature()
	b := Bundle{Identity: b64.EncodeToString(s.GetIdentityKeyPair().PublicKey().Serialize()), Signed: SignedPublicKey{spk.ID(), b64.EncodeToString(spk.KeyPair().PublicKey().Serialize())}, Signature: b64.EncodeToString(sig[:])}
	for id := range s.PreKeys {
		key, err := s.LoadPreKey(context.Background(), id)
		if err != nil {
			return 0, Bundle{}, err
		}
		b.PreKeys = append(b.PreKeys, PublicPreKey{id, b64.EncodeToString(key.KeyPair().PublicKey().Serialize())})
	}
	slices.SortFunc(b.PreKeys, func(a, b PublicPreKey) int { return int(a.ID) - int(b.ID) })
	return s.DeviceID, b, nil
}
func (t Target) preKeyBundle() (*prekey.Bundle, error) {
	pub, err := wirePoint(t.Bundle.Identity)
	if err != nil {
		return nil, err
	}
	spk, err := wirePoint(t.Bundle.Signed.Value)
	if err != nil {
		return nil, err
	}
	sig, err := b64.DecodeString(t.Bundle.Signature)
	if err != nil || len(sig) != 64 || len(t.Bundle.PreKeys) == 0 {
		return nil, ErrInvalid
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(t.Bundle.PreKeys))))
	if err != nil {
		return nil, err
	}
	pk := t.Bundle.PreKeys[index.Int64()]
	point, err := wirePoint(pk.Value)
	if err != nil {
		return nil, err
	}
	return prekey.NewBundle(0, t.DeviceID, optional.NewOptionalUint32(pk.ID), t.Bundle.Signed.ID, point, spk, [64]byte(sig), identity.NewKey(pub)), nil
}
func (d *Device) Encrypt(ctx context.Context, targets []Target, text string) (Envelope, error) {
	if len(targets) == 0 || len(targets) > 256 || text == "" || !utf8.ValidString(text) {
		return Envelope{}, ErrInvalid
	}
	key := make([]byte, 16)
	rand.Read(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return Envelope{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, err
	}
	iv := make([]byte, aead.NonceSize())
	rand.Read(iv)
	ciphertext := aead.Seal(nil, iv, []byte(text), nil)
	split := len(ciphertext) - aead.Overhead()
	material := append(key, ciphertext[split:]...)
	env := Envelope{Header: Header{IV: b64.EncodeToString(iv)}, Payload: b64.EncodeToString(ciphertext[:split])}
	err = d.update(func(s *state) error {
		env.Header.From = s.DeviceID
		seen := map[uint32]bool{}
		for _, t := range targets {
			if t.JID == "" || t.DeviceID == 0 || seen[t.DeviceID] {
				return ErrInvalid
			}
			seen[t.DeviceID] = true
			addr := protocol.NewSignalAddress(t.JID, t.DeviceID)
			fp, err := t.Bundle.Fingerprint()
			if err != nil {
				return err
			}
			if s.Verified[addr.String()] != fp {
				return ErrUnverified
			}
			builder := session.NewBuilder(s, s, s, s, addr, serializer)
			if _, ok := s.Sessions[addr.String()]; !ok {
				bundle, err := t.preKeyBundle()
				if err != nil {
					return err
				}
				if err = builder.ProcessBundle(ctx, bundle); err != nil {
					return err
				}
			}
			wrapped, err := session.NewCipher(builder, addr).Encrypt(ctx, material)
			if err != nil {
				return err
			}
			env.Header.Keys = append(env.Header.Keys, WrappedKey{t.DeviceID, wrapped.Type() == protocol.PREKEY_TYPE, b64.EncodeToString(wrapped.Serialize())})
		}
		return nil
	})
	if err != nil {
		return Envelope{}, err
	}
	return env, nil
}
func (d *Device) Decrypt(ctx context.Context, peer string, env Envelope) (string, error) {
	var text string
	err := d.DecryptTo(ctx, peer, env, func(value string) error { text = value; return nil })
	return text, err
}

// DecryptTo commits the ratchet only after the consumer accepts the message.
func (d *Device) DecryptTo(ctx context.Context, peer string, env Envelope, accept func(string) error) error {
	return d.decryptTo(ctx, peer, env, "", accept)
}
func (d *Device) decryptTo(ctx context.Context, peer string, env Envelope, digest string, accept func(string) error) error {
	iv, err := b64.DecodeString(env.Header.IV)
	if err != nil || (len(iv) != 12 && len(iv) != 16) || peer == "" || env.Header.From == 0 || len(env.Header.Keys) > 256 {
		return ErrInvalid
	}
	payload, err := b64.DecodeString(env.Payload)
	if err != nil {
		return ErrInvalid
	}
	var text string
	err = d.update(func(s *state) error {
		addr := protocol.NewSignalAddress(peer, env.Header.From)
		if s.Verified[addr.String()] == "" {
			return ErrUnverified
		}
		var chosen *WrappedKey
		for i := range env.Header.Keys {
			if env.Header.Keys[i].To == s.DeviceID {
				if chosen != nil {
					return ErrInvalid
				}
				chosen = &env.Header.Keys[i]
			}
		}
		if chosen == nil {
			return ErrInvalid
		}
		raw, err := b64.DecodeString(chosen.Value)
		if err != nil || len(raw) < 10 || len(raw) > 65536 {
			return ErrInvalid
		}
		builder := session.NewBuilder(s, s, s, s, addr, serializer)
		material, err := unwrap(ctx, session.NewCipher(builder, addr), raw, chosen.PreKey)
		if err != nil {
			return err
		}
		if len(payload) > 0 {
			if len(material) != 32 && len(material) != 16 {
				return ErrInvalid
			}
			block, err := aes.NewCipher(material[:16])
			if err != nil {
				return err
			}
			aead, err := cipher.NewGCMWithNonceSize(block, len(iv))
			if err != nil {
				return err
			}
			clear, err := aead.Open(nil, iv, append(payload, material[16:]...), nil)
			if err != nil || !utf8.Valid(clear) {
				return ErrInvalid
			}
			text = string(clear)
		}
		if chosen.PreKey {
			if err := s.replenish(); err != nil {
				return err
			}
		}
		if err := accept(text); err != nil {
			return err
		}
		discardPending(s, peer, digest)
		return nil
	})
	return err
}

// Parse the remote message before passing its fields to the Signal library.
func unwrap(ctx context.Context, c *session.Cipher, raw []byte, pk bool) ([]byte, error) {
	signal := raw
	if pk {
		header, err := serializer.PreKeySignalMessage.Deserialize(raw)
		if err != nil || !validPoint(header.IdentityKey) || !validPoint(header.BaseKey) {
			return nil, ErrInvalid
		}
		signal = header.Message
	}
	if len(signal) <= 1+protocol.MacLength {
		return nil, ErrInvalid
	}
	message, err := serializer.SignalMessage.Deserialize(signal)
	if err != nil || !validPoint(message.RatchetKey) || len(message.CipherText) == 0 || len(message.CipherText)%aes.BlockSize != 0 {
		return nil, ErrInvalid
	}
	if pk {
		p, err := protocol.NewPreKeySignalMessageFromBytes(raw, serializer.PreKeySignalMessage, serializer.SignalMessage)
		if err != nil {
			return nil, err
		}
		return c.DecryptMessage(ctx, p)
	}
	m, err := protocol.NewSignalMessageFromStruct(message, serializer.SignalMessage)
	if err != nil {
		return nil, err
	}
	return c.Decrypt(ctx, m)
}
func validPoint(raw []byte) bool { return len(raw) == 33 && raw[0] == ecc.DjbType }
