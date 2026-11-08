package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"math/rand"
	"testing"
)

func noisePNG(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // deterministic test noise
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Intn(256))
		if i%4 == 3 && !alpha {
			img.Pix[i] = 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFit(t *testing.T) {
	src := noisePNG(t, 1200, 800, false)

	f, err := Fit(src, "a.png", 0, FetchOptions{})
	if err != nil || !bytes.Equal(f.Bytes, src) {
		t.Fatalf("no budget, no options: changed (%v)", err)
	}
	f, err = Fit(src, "a.png", int64(len(src)), FetchOptions{})
	if err != nil || !bytes.Equal(f.Bytes, src) {
		t.Fatalf("fits already: changed (%v)", err)
	}

	budget := int64(len(src) / 8)
	f, err = Fit(src, "a.png", budget, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.ContentType != TypeJPEG || f.Filename != "a.jpg" || int64(len(f.Bytes)) > budget {
		t.Fatalf("got %s %s %d bytes (budget %d)", f.ContentType, f.Filename, len(f.Bytes), budget)
	}

	f, err = Fit(src, "a.png", 0, FetchOptions{MaxDimension: 300})
	if err != nil || f.Width != 300 || f.Height != 200 || f.ContentType != TypePNG {
		t.Fatalf("max dimension: %+v, %v", f, err)
	}

	alpha := noisePNG(t, 1500, 1000, true)
	f, err = Fit(alpha, "t.png", int64(len(alpha)/3), FetchOptions{})
	if err != nil || f.ContentType != TypePNG || f.Width >= 1500 {
		t.Fatalf("transparent image should shrink as PNG: %+v, %v", f, err)
	}

	if _, err := Fit(src, "a.png", 100, FetchOptions{}); !errors.Is(err, ErrCannotFit) {
		t.Fatalf("impossible budget: %v", err)
	}
}

func TestFitAnimatedGIF(t *testing.T) {
	var buf bytes.Buffer
	frame := func() *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 8, 8), palette.Plan9)
		p.Set(1, 1, color.White)
		return p
	}
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{frame(), frame()}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if f, err := Fit(b, "a.gif", int64(len(b)), FetchOptions{}); err != nil || !bytes.Equal(f.Bytes, b) {
		t.Fatalf("animated GIF that fits must pass through: %v", err)
	}
	if _, err := Fit(b, "a.gif", 10, FetchOptions{}); !errors.Is(err, ErrCannotFit) {
		t.Fatalf("oversized animated GIF: %v", err)
	}
}
