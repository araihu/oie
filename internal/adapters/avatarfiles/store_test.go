package avatarfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/application"
)

func TestStaticThumbnailIntegrityAndPrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "avatars")
	store := New(dir)
	original := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	original.Set(10, 10, color.NRGBA{R: 255, A: 255})
	var source bytes.Buffer
	if err := png.Encode(&source, original); err != nil {
		t.Fatal(err)
	}
	hash, err := store.Put(t.Context(), source.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Read(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatal("content address mismatch")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 256 || config.Height != 128 {
		t.Fatalf("thumbnail: %+v %v", config, err)
	}
	for _, path := range []string{dir, filepath.Join(dir, hash+".png")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("cache permissions: %v %v", info, err)
		}
	}
	again, err := store.Put(t.Context(), source.Bytes())
	if err != nil || again != hash {
		t.Fatal("non-deterministic thumbnail")
	}
	path := filepath.Join(dir, hash+".png")
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(t.Context(), hash); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("corrupt cache served", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(dir, "other"), path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(t.Context(), hash); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("symlink accepted", err)
	}
}

func TestRejectUnsafeOrUnboundedAvatarInput(t *testing.T) {
	store := New(t.TempDir())
	for _, data := range [][]byte{nil, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), []byte("not an image"), bytes.Repeat([]byte{0}, (1<<20)+1)} {
		if _, err := store.Put(t.Context(), data); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("invalid image accepted", err)
		}
	}
	var huge bytes.Buffer
	if err := png.Encode(&huge, image.NewGray(image.Rect(0, 0, 4097, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), huge.Bytes()); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("oversize dimensions accepted", err)
	}
	for _, hash := range []string{"../../file", strings.Repeat("A", 64), strings.Repeat("f", 63)} {
		if _, err := store.Read(t.Context(), hash); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("invalid path accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Put(ctx, huge.Bytes()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
