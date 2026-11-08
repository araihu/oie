package mediafiles

import (
	"bytes"
	"errors"
	"image"
	"image/color"
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
	if err := png.Encode(&input, image.NewNRGBA(image.Rect(0, 0, 1600, 1200))); err != nil {
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
	if err != nil || config.Width != 1024 || config.Height != 768 {
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

func TestCardThumbnailFiltersFineDetailWithoutAliasing(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2048, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 2048; x++ {
			v := uint8(0)
			if x%2 == 1 {
				v = 255
			}
			source.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	store := New(t.TempDir())
	file, err := store.Thumbnail(t.Context(), input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Read(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	thumb, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := thumb.At(512, 0).RGBA()
	if r < 0x4000 || r > 0xc000 {
		t.Fatal("fine detail aliased instead of filtered", r)
	}
}
