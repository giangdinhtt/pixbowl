package imageproc

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := Start(RuntimeConfig{Concurrency: 1, Quality: 80}); err != nil {
		panic(err)
	}
	code := m.Run()
	Stop()
	os.Exit(code)
}

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), uint8((x + y) % 256), 255})
		}
	}
	return img
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, gradient(w, h)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, gradient(w, h), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg.Width, cfg.Height
}

func TestConvertResizeAndKeepFormat(t *testing.T) {
	out, err := Process(encodePNG(t, 800, 600), Options{Format: "webp", MaxDimension: 400, Quality: 80})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if out.Format != "webp" {
		t.Fatalf("format = %q, want webp", out.Format)
	}
	if out.Width != 400 || out.Height != 300 {
		t.Fatalf("size = %dx%d, want 400x300", out.Width, out.Height)
	}
	if w, h := decodeSize(t, out.Data); w != 400 || h != 300 {
		t.Fatalf("decoded size = %dx%d", w, h)
	}

	out, err = Process(encodeJPEG(t, 300, 200), Options{Format: "preserve"})
	if err != nil {
		t.Fatalf("preserve: %v", err)
	}
	if out.Format != "jpeg" {
		t.Fatalf("preserve format = %q", out.Format)
	}
}

func TestNeverUpscales(t *testing.T) {
	out, err := Process(encodePNG(t, 800, 600), Options{Format: "png", MaxDimension: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 800 || out.Height != 600 {
		t.Fatalf("size = %dx%d, want 800x600", out.Width, out.Height)
	}

	out, err = Process(encodePNG(t, 800, 600), Options{Format: "png", Width: 2000, Height: 2000, Fit: "inside"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 800 || out.Height != 600 {
		t.Fatalf("inside variant upscaled to %dx%d", out.Width, out.Height)
	}
}

func TestVariantFits(t *testing.T) {
	src := encodePNG(t, 800, 600)

	inside, err := Process(src, Options{Format: "webp", Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	if inside.Width != 200 || inside.Height != 150 {
		t.Fatalf("inside = %dx%d, want 200x150", inside.Width, inside.Height)
	}

	cover, err := Process(src, Options{Format: "webp", Width: 200, Height: 200, Fit: "cover"})
	if err != nil {
		t.Fatal(err)
	}
	if cover.Width != 200 || cover.Height != 200 {
		t.Fatalf("cover = %dx%d, want 200x200", cover.Width, cover.Height)
	}

	contain, err := Process(src, Options{Format: "webp", Width: 200, Height: 200, Fit: "contain"})
	if err != nil {
		t.Fatal(err)
	}
	if contain.Width != 200 || contain.Height != 200 {
		t.Fatalf("contain = %dx%d, want 200x200", contain.Width, contain.Height)
	}
}

func TestRejectsOversizedAndUnsupported(t *testing.T) {
	_, err := Process(encodePNG(t, 800, 600), Options{Format: "webp", MaxPixels: 100_000})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`)
	_, err = Process(svg, Options{Format: "webp"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestAnimatedGIFPassesThrough(t *testing.T) {
	palette := []color.Color{color.Black, color.White}
	anim := &gif.GIF{}
	for i := 0; i < 3; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 32, 24), palette)
		frame.SetColorIndex(i%32, 0, 1)
		anim.Image = append(anim.Image, frame)
		anim.Delay = append(anim.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, anim); err != nil {
		t.Fatal(err)
	}

	out, err := Process(buf.Bytes(), Options{Format: "webp"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Animated {
		t.Fatal("expected animated result")
	}
	if out.Format != "gif" {
		t.Fatalf("format = %q, want gif passthrough", out.Format)
	}
	if !bytes.Equal(out.Data, buf.Bytes()) {
		t.Fatal("animated image was re-encoded")
	}
}

func TestSupportedFormatsIncludesBasics(t *testing.T) {
	for _, format := range []string{"jpeg", "png", "webp"} {
		if !SupportsFormat(format) {
			t.Fatalf("%s should always be supported", format)
		}
	}
}
