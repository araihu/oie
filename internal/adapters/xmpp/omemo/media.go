package omemo

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/araihu/oie/internal/domain"
)

const MediaTagSize = 16

// SealMedia uses the XEP-0454 format: ciphertext followed by the GCM tag.
func SealMedia(plain []byte) ([]byte, domain.EncryptedAttachment, error) {
	file := domain.EncryptedAttachment{Key: make([]byte, 32), Nonce: make([]byte, 12)}
	rand.Read(file.Key)
	rand.Read(file.Nonce)
	aead, err := mediaCipher(file)
	if err != nil {
		return nil, domain.EncryptedAttachment{}, err
	}
	return aead.Seal(nil, file.Nonce, plain, nil), file, nil
}

func OpenMedia(file domain.EncryptedAttachment, ciphertext []byte) ([]byte, error) {
	aead, err := mediaCipher(file)
	if err != nil {
		return nil, err
	}
	return aead.Open(ciphertext[:0], file.Nonce, ciphertext, nil)
}

func mediaCipher(file domain.EncryptedAttachment) (cipher.AEAD, error) {
	if len(file.Key) != 32 || len(file.Nonce) != 12 && len(file.Nonce) != 16 {
		return nil, ErrInvalid
	}
	block, err := aes.NewCipher(file.Key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithNonceSize(block, len(file.Nonce))
}

func MediaURI(https string, file domain.EncryptedAttachment) string {
	return "aesgcm:" + strings.TrimPrefix(https, "https:") + "#" + hex.EncodeToString(file.Nonce) + hex.EncodeToString(file.Key)
}

func ParseMediaURI(raw string) (domain.EncryptedAttachment, bool) {
	var file domain.EncryptedAttachment
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4224 || u.Scheme != "aesgcm" || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || strings.ContainsAny(raw, " \r\n\t<>\\\"") || u.RawFragment != "" {
		return file, false
	}
	key, err := hex.DecodeString(u.Fragment)
	if err != nil || len(key) != 44 && len(key) != 48 {
		return file, false
	}
	u.Scheme, u.Fragment = "https", ""
	file.URL, file.Nonce, file.Key = u.String(), key[:len(key)-32], key[len(key)-32:]
	name := path.Base(strings.ReplaceAll(u.Path, "\\", "/"))
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return '_'
		}
		return c
	}, name)
	if name == "." || name == ".." || name == "/" || len(name) > 255 {
		name = "attachment"
	}
	file.FileName = name
	return file, true
}
