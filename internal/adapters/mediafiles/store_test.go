package mediafiles

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/araihu/oie/internal/application"
)

func TestRetainedOriginalAndNormalizedThumbnail(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	var input bytes.Buffer
	if err := png.Encode(&input, image.NewNRGBA(image.Rect(0, 0, 800, 600))); err != nil {
		t.Fatal(err)
	}
	file, err := s.Put(t.Context(), input.Bytes(), true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.Read(t.Context(), file)
	if err != nil || !bytes.Equal(data, input.Bytes()) {
		t.Fatal("original changed", err)
	}
	thumbnail, err := s.Thumbnail(t.Context(), input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	data, err = s.Read(t.Context(), thumbnail)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 256 || config.Height != 192 {
		t.Fatal("thumbnail dimensions", config, err)
	}
	if err = os.WriteFile(filepath.Join(dir, file.Hash), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(t.Context(), file); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("modified file served", err)
	}
	for _, data := range [][]byte{[]byte(`<svg onload="bad()"/>`), nil, bytes.Repeat([]byte{0}, MaxOriginal+1)} {
		if _, err = s.Put(t.Context(), data, true); err == nil {
			t.Fatal("unsafe image accepted")
		}
	}
}
