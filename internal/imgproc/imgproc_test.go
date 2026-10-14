package imgproc

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func gradient(w, h int) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g.Pix[y*g.Stride+x] = uint8((x * 255) / (w - 1))
		}
	}
	return g
}

func TestDecodeGrayJPEG(t *testing.T) {
	src := gradient(200, 100)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	g, err := DecodeGray(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if g.Bounds() != image.Rect(0, 0, 200, 100) {
		t.Fatalf("bounds %v", g.Bounds())
	}
	if d := int(g.Pix[100]) - int(src.Pix[100]); d > 3 || d < -3 {
		t.Errorf("pixel drifted by %d", d)
	}
}

func TestDecodeGrayColourJPEGUsesLuma(t *testing.T) {
	rgba := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2], rgba.Pix[i+3] = 200, 200, 200, 255
	}
	var b bytes.Buffer
	jpeg.Encode(&b, rgba, &jpeg.Options{Quality: 95})
	g, err := DecodeGray(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if v := g.Pix[32*g.Stride+32]; v < 197 || v > 203 {
		t.Errorf("luma = %d, want ~200", v)
	}
}

func TestDecodePNGVariants(t *testing.T) {
	// NRGBA with alpha composites over white
	n := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(n.Pix); i += 4 {
		n.Pix[i], n.Pix[i+1], n.Pix[i+2], n.Pix[i+3] = 0, 0, 0, 128
	}
	var b bytes.Buffer
	png.Encode(&b, n)
	g, err := DecodeGray(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if v := g.Pix[0]; v < 124 || v > 131 {
		t.Errorf("half-transparent black over white = %d, want ~127", v)
	}
	// paletted
	p := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Gray{0}, color.Gray{255}, color.Gray{100}})
	p.SetColorIndex(1, 1, 2)
	b.Reset()
	png.Encode(&b, p)
	g, err = DecodeGray(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if g.Pix[1*g.Stride+1] < 98 || g.Pix[1*g.Stride+1] > 102 {
		t.Errorf("paletted = %d", g.Pix[1*g.Stride+1])
	}
	// 16-bit gray
	g16 := image.NewGray16(image.Rect(0, 0, 4, 4))
	g16.SetGray16(0, 0, color.Gray16{Y: 0x8000})
	b.Reset()
	png.Encode(&b, g16)
	g, err = DecodeGray(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if g.Pix[0] != 0x80 {
		t.Errorf("gray16 = %#x", g.Pix[0])
	}
}

func TestDecodeGIF(t *testing.T) {
	p := image.NewPaletted(image.Rect(0, 0, 10, 10), color.Palette{color.Gray{0}, color.Gray{255}})
	var b bytes.Buffer
	if err := gif.Encode(&b, p, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeGray(b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeWebP(t *testing.T) {
	for _, f := range []string{"blue-purple-pink.lossy.webp", "blue-purple-pink.lossless.webp", "gopher-doc.with-alpha.lossless.webp", "gopher-doc.8bpp.lossless.webp"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		g, err := DecodeGray(b)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if g.Bounds().Dx() == 0 || g.Bounds().Dy() == 0 {
			t.Errorf("%s: empty", f)
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	if _, err := DecodeGray([]byte("hello, this is not an image")); err == nil {
		t.Error("accepted text")
	}
	if _, err := DecodeGray(nil); err == nil {
		t.Error("accepted nil")
	}
	// truncated JPEG
	var b bytes.Buffer
	jpeg.Encode(&b, gradient(64, 64), nil)
	if _, err := DecodeGray(b.Bytes()[:b.Len()/2]); err == nil {
		t.Error("accepted truncated jpeg")
	}
	// absurd dimensions in a PNG header must be refused before allocation
	big := image.NewGray(image.Rect(0, 0, 1, 1))
	b.Reset()
	png.Encode(&b, big)
	raw := b.Bytes()
	copy(raw[16:20], []byte{0x00, 0x01, 0x00, 0x00}) // width 65536
	copy(raw[20:24], []byte{0x00, 0x01, 0x00, 0x00}) // height 65536
	if _, err := DecodeGray(raw); err == nil {
		t.Error("accepted a 4-gigapixel header")
	}
}

func TestFitSize(t *testing.T) {
	cases := []struct{ w, h, bw, bh, ew, eh int }{
		{1200, 1800, 1620, 2160, 1440, 2160},
		{1800, 1200, 1620, 2160, 1620, 1080},
		{3000, 4500, 1620, 2160, 1440, 2160},
		{100, 100, 1620, 2160, 300, 300}, // enlargement is capped at 3x
		{1620, 2160, 1620, 2160, 1620, 2160},
	}
	for _, c := range cases {
		w, h := FitSize(c.w, c.h, c.bw, c.bh, 3)
		if w != c.ew || h != c.eh {
			t.Errorf("FitSize(%d,%d) = %dx%d, want %dx%d", c.w, c.h, w, h, c.ew, c.eh)
		}
	}
}

func TestResizePreservesFlatAndEnergy(t *testing.T) {
	flat := image.NewGray(image.Rect(0, 0, 300, 200))
	for i := range flat.Pix {
		flat.Pix[i] = 137
	}
	for _, sz := range [][2]int{{100, 70}, {450, 301}, {300, 100}, {77, 200}} {
		out := Resize(flat, sz[0], sz[1])
		for i, v := range out.Pix {
			if v != 137 {
				t.Fatalf("%v: pixel %d = %d, want 137", sz, i, v)
			}
		}
	}
}

func TestResizeIdentityAndAveraging(t *testing.T) {
	g := gradient(64, 48)
	same := Resize(g, 64, 48)
	for i := range g.Pix {
		if same.Pix[i] != g.Pix[i] {
			t.Fatal("identity resize changed pixels")
		}
	}
	// 1-pixel checkerboard shrunk 2x must average to mid gray, not alias
	cb := image.NewGray(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			if (x+y)%2 == 0 {
				cb.Pix[y*cb.Stride+x] = 255
			}
		}
	}
	out := Resize(cb, 64, 64)
	var lo, hi uint8 = 255, 0
	for y := 8; y < 56; y++ {
		for x := 8; x < 56; x++ {
			v := out.Pix[y*out.Stride+x]
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
	}
	if lo < 110 || hi > 145 {
		t.Errorf("checkerboard averaged to %d..%d, want ~127", lo, hi)
	}
}

func TestResizeOnYPlaneView(t *testing.T) {
	// ToGray returns the JPEG's Y plane in place, whose Stride can exceed the width
	rgba := image.NewRGBA(image.Rect(0, 0, 33, 17))
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2], rgba.Pix[i+3] = 90, 90, 90, 255
	}
	var b bytes.Buffer
	jpeg.Encode(&b, rgba, nil)
	g, err := DecodeGray(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	out := Fit(g, 330, 170)
	if out.Bounds().Dx() != 99 || out.Bounds().Dy() != 51 { // 3x cap
		t.Errorf("size %v", out.Bounds())
	}
	if v := out.Pix[10*out.Stride+10]; v < 86 || v > 94 {
		t.Errorf("value %d", v)
	}
}

func TestAutoCropAndRotate(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 200, 300))
	for i := range g.Pix {
		g.Pix[i] = 255
	}
	for y := 50; y < 250; y++ {
		for x := 40; x < 160; x++ {
			g.Pix[y*g.Stride+x] = 0
		}
	}
	g.Pix[5*g.Stride+5] = 0 // a speck
	r := CropBox(g, DefaultCrop)
	if r != image.Rect(20, 30, 180, 270) { // content is 40..160 x 50..250; each edge moves at most 10 %
		t.Errorf("crop = %v", r)
	}
	rot := Rotate90(SubImage(g, image.Rect(0, 0, 4, 2)))
	if rot.Bounds().Dx() != 2 || rot.Bounds().Dy() != 4 {
		t.Errorf("rot bounds %v", rot.Bounds())
	}
	src := image.NewGray(image.Rect(0, 0, 3, 2))
	src.Pix = []byte{1, 2, 3, 4, 5, 6}
	got := Rotate90(src).Pix
	want := []byte{4, 1, 5, 2, 6, 3} // clockwise
	if !bytes.Equal(got, want) {
		t.Errorf("rotate = %v, want %v", got, want)
	}
}

func BenchmarkDecodeFit1200x1800(b *testing.B) {
	g := gradient(1200, 1800)
	var buf bytes.Buffer
	jpeg.Encode(&buf, g, &jpeg.Options{Quality: 85})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := DecodeGray(buf.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		_ = Fit(d, 1620, 2160)
	}
}

// A truncated or hostile file must come back as an error, never as a panic
// that would take the whole reader down.
func TestDecodeGrayNeverPanics(t *testing.T) {
	var gb bytes.Buffer
	jpeg.Encode(&gb, gradient(64, 64), nil)
	good := gb.Bytes()
	for _, n := range []int{12, 40, 200, len(good) / 2, len(good) - 1} {
		if n > len(good) {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("DecodeGray panicked on a %d byte truncation: %v", n, r)
				}
			}()
			_, _ = DecodeGray(good[:n])
		}()
	}
	junk := append([]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), make([]byte, 64)...)
	if _, err := DecodeGray(junk); err == nil {
		t.Error("junk WebP decoded")
	}
}
