package imgproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestFitDims(t *testing.T) {
	const W, H = 1620, 2160
	cases := []struct {
		name         string
		sw, sh       int
		fit          FitMode
		wantW, wantH int
	}{
		{"contain portrait 1020x1320", 1020, 1320, FitContain, 1620, 2096},
		{"contain large scan", 3000, 4500, FitContain, 1440, 2160},
		{"width, tall strip", 800, 4000, FitWidth, 1620, 8100},
		{"width, near fit becomes whole page", 1000, 1350, FitWidth, 1600, 2160}, // 1620/1000 -> 2187 tall: within 3 percent of 2160
		{"width, overflowing page", 1000, 1500, FitWidth, 1620, 2430},
		{"height, wide spread", 3200, 2400, FitHeight, 2880, 2160},
		{"height, narrow page fits", 800, 1300, FitHeight, 1329, 2160},
		{"upscale is limited to 3x", 100, 150, FitContain, 300, 450},
		{"degenerate", 0, 0, FitContain, 1, 1},
	}
	for _, c := range cases {
		w, h := FitDims(c.sw, c.sh, W, H, c.fit)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
	// a webtoon strip never exceeds the pixel cap
	w, h := FitDims(800, 40000, W, H, FitWidth)
	if w*h > MaxFitPixels || w < 100 {
		t.Errorf("strip fitted to %dx%d = %d px", w, h, w*h)
	}
}

func jpegColourOf(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 95})
	return b.Bytes()
}

func TestDecodePlanesGrayAndColour(t *testing.T) {
	grey := jpegColourOf(64, 48, color.RGBA{120, 120, 120, 255})
	red := jpegColourOf(64, 48, color.RGBA{220, 40, 30, 255})

	// black and white mode never keeps chroma
	pl, err := DecodePlanes(red, false)
	if err != nil || !pl.IsGray() {
		t.Fatalf("b/w decode of a colour page: %v gray=%v", err, pl != nil && pl.IsGray())
	}
	// colour mode: a neutral "colour" JPEG is still a gray page (and costs gray memory)
	pl, err = DecodePlanes(grey, true)
	if err != nil || !pl.IsGray() {
		t.Fatalf("neutral page kept chroma: %v", err)
	}
	pl, err = DecodePlanes(red, true)
	if err != nil || pl.IsGray() {
		t.Fatalf("colour page lost its chroma: %v", err)
	}
	f := FitPlanes(pl, 32, 24, CatmullRom, 1)
	if f.IsGray() || len(f.Col) != 32*24 || f.Bytes() != 2*32*24 {
		t.Fatalf("fitted colour page: gray=%v bytes=%d", f.IsGray(), f.Bytes())
	}
	v := f.Col[12*32+16]
	r5, g6, b5 := int(v>>11)&31, int(v>>5)&63, int(v)&31
	if r5 < 24 || g6 > 20 || b5 > 8 {
		t.Errorf("a red page came out r=%d g=%d b=%d", r5, g6, b5)
	}
	// a gray page fitted in colour mode stays 8-bit gray, exactly one copy
	pg, _ := DecodePlanes(grey, true)
	fg := FitPlanes(pg, 32, 24, Lanczos3, 1)
	if !fg.IsGray() || len(fg.Gray) != 32*24 || fg.Col != nil {
		t.Errorf("gray page: gray=%v", fg.IsGray())
	}
	if d := int(fg.Gray[100]) - 120; d < -3 || d > 3 {
		t.Errorf("gray level %d, want ~120", fg.Gray[100])
	}
}

func TestDecodePlanesPNGWithAlphaAndPalette(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	img.SetNRGBA(3, 3, color.NRGBA{0, 0, 255, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	pl, err := DecodePlanes(b.Bytes(), true)
	if err != nil {
		t.Fatal(err)
	}
	if pl.IsGray() {
		t.Error("a blue pixel is colour")
	}
	if pl.Bounds().Dx() != 16 {
		t.Errorf("bounds %v", pl.Bounds())
	}
	// the same file in b/w mode is gray
	pl, _ = DecodePlanes(b.Bytes(), false)
	if !pl.IsGray() {
		t.Error("b/w decode kept chroma")
	}
}

func TestPlanesCropAndRotateKeepChromaAligned(t *testing.T) {
	red := jpegColourOf(64, 48, color.RGBA{220, 40, 30, 255})
	pl, _ := DecodePlanes(red, true)
	c := pl.Crop(image.Rect(8, 8, 40, 32))
	if c.Bounds().Dx() != 32 || c.Cb == nil || c.Cb.Bounds().Dx() != 16 {
		t.Errorf("crop: y=%v cb=%v", c.Bounds(), c.Cb.Bounds())
	}
	r := pl.Rotate90()
	if r.Bounds().Dx() != 48 || r.Bounds().Dy() != 64 || r.Cb.Bounds().Dx() != 24 {
		t.Errorf("rotate: %v %v", r.Bounds(), r.Cb.Bounds())
	}
}

// Both kernels keep a flat field flat (weights sum to one) and Lanczos is a
// little sharper on an edge than Catmull-Rom.
func TestKernelsNormalisedAndLanczosSharper(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if x >= 50 {
				src.Pix[y*src.Stride+x] = 255
			}
		}
	}
	for _, k := range []Kernel{CatmullRom, Lanczos3} {
		flat := image.NewGray(image.Rect(0, 0, 40, 40))
		for i := range flat.Pix {
			flat.Pix[i] = 77
		}
		o := ResizeK(flat, 65, 65, k)
		for _, v := range o.Pix {
			if v != 77 {
				t.Fatalf("kernel %d changed a flat field to %d", k, v)
			}
		}
	}
	steepness := func(k Kernel) int {
		o := ResizeK(src, 162, 162, k)
		row := o.Pix[80*o.Stride : 80*o.Stride+162]
		// width of the transition: pixels strictly between 20 and 235
		n := 0
		for _, v := range row {
			if v > 20 && v < 235 {
				n++
			}
		}
		return n
	}
	if cr, lz := steepness(CatmullRom), steepness(Lanczos3); lz > cr {
		t.Errorf("Lanczos edge (%d px) is softer than Catmull-Rom (%d px)", lz, cr)
	}
}

func TestFromGrayNoCopyWhenOwned(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 10, 8))
	f := FromGray(g)
	if f.W != 10 || f.H != 8 || len(f.Gray) != 80 || &f.Gray[0] != &g.Pix[0] {
		t.Error("FromGray copied an image that owns its pixels")
	}
}

func TestGray565NeutralAndMonotonic(t *testing.T) {
	prev := -1
	for g := 0; g < 256; g++ {
		v := Gray565[g]
		r5, g6, b5 := int(v>>11)&31, int(v>>5)&63, int(v)&31
		if r5 != b5 || g6 != r5<<1|r5>>4 {
			t.Fatalf("g=%d not neutral: %d %d %d", g, r5, g6, b5)
		}
		if int(v) < prev {
			t.Fatalf("not monotonic at %d", g)
		}
		prev = int(v)
	}
	if Gray565[0] != 0 || Gray565[255] != 0xFFFF {
		t.Errorf("ends %#x %#x", Gray565[0], Gray565[255])
	}
}
