package imgproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"testing"
	"time"
)

func fillRect(g *image.Gray, r image.Rectangle, v uint8) {
	r = r.Intersect(g.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			g.Pix[y*g.Stride+x] = v
		}
	}
}

func addNoise(g *image.Gray, amp int, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	for i, v := range g.Pix {
		nv := int(v) + rng.Intn(2*amp+1) - amp
		if nv < 0 {
			nv = 0
		}
		if nv > 255 {
			nv = 255
		}
		g.Pix[i] = uint8(nv)
	}
}

// art draws mid-tone "content" blobs inside the given margin fraction.
func art(g *image.Gray, margin float64) {
	b := g.Bounds()
	mx, my := int(float64(b.Dx())*margin), int(float64(b.Dy())*margin)
	for k := 0; k < 12; k++ {
		x0 := mx + (k*37)%(b.Dx()-2*mx-60)
		y0 := my + (k*91)%(b.Dy()-2*my-60)
		fillRect(g, image.Rect(x0, y0, x0+50+k*3, y0+40+k*5), uint8(20+k*15))
	}
}

func flatPage(w, h int, bg uint8) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	fillRect(g, g.Bounds(), bg)
	return g
}

// ---- crop ---------------------------------------------------------------------------

func TestCropBoxFixtures(t *testing.T) {
	const W, H = 1200, 1800
	// 1. a white page with 12 % margins: every edge is clamped to 10 %
	g := flatPage(W, H, 255)
	fillRect(g, image.Rect(W*12/100, H*12/100, W*88/100, H*88/100), 30)
	r := CropBox(g, DefaultCrop)
	if r != image.Rect(W*10/100, H*10/100, W-W*10/100, H-H*10/100) {
		t.Errorf("clamped crop = %v", r)
	}
	// 2. margins of 6 %: cropped exactly to the content
	g = flatPage(W, H, 255)
	c := image.Rect(W*6/100, H*6/100, W*94/100, H*94/100)
	fillRect(g, c, 40)
	r = CropBox(g, DefaultCrop)
	if d := absInt(r.Min.X-c.Min.X) + absInt(r.Min.Y-c.Min.Y) + absInt(r.Max.X-c.Max.X) + absInt(r.Max.Y-c.Max.Y); d > 8 {
		t.Errorf("crop %v, want about %v", r, c)
	}
	// 3. a full-bleed page: untouched
	g = flatPage(W, H, 255)
	art(g, 0.02)
	for _, e := range []image.Rectangle{image.Rect(0, 0, W, 3), image.Rect(0, H-3, W, H), image.Rect(0, 0, 3, H), image.Rect(W-3, 0, W, H)} {
		fillRect(g, e, 0) // a frame touching every edge
	}
	if r := CropBox(g, DefaultCrop); r != g.Bounds() {
		t.Errorf("full-bleed page cropped to %v", r)
	}
	// 4. a blank page: untouched
	g = flatPage(W, H, 250)
	addNoise(g, 3, 1)
	if r := CropBox(g, DefaultCrop); r != g.Bounds() {
		t.Errorf("blank page cropped to %v", r)
	}
	// 5. a black page with a dark-grey border measured as the background
	g = flatPage(W, H, 0)
	fillRect(g, image.Rect(W*8/100, H*8/100, W*92/100, H*92/100), 90)
	r = CropBox(g, DefaultCrop)
	if r.Dx() >= W-20 || r.Min.X < 70 {
		t.Errorf("black-background page not cropped to its content: %v", r)
	}
	// 6. a scan speck in the margin does not defeat the crop (a row needs > 1 % content)
	g = flatPage(W, H, 255)
	fillRect(g, image.Rect(W*8/100, H*8/100, W*92/100, H*92/100), 40)
	g.Pix[5*g.Stride+5] = 0
	r = CropBox(g, DefaultCrop)
	if r.Min.X < 80 || r.Min.Y < 120 {
		t.Errorf("speck defeated the crop: %v", r)
	}
	// 7. washed-out paper (216) with noise: the background is measured, not assumed 255
	g = flatPage(W, H, 216)
	addNoise(g, 5, 2)
	fillRect(g, image.Rect(W*9/100, H*9/100, W*91/100, H*91/100), 60)
	r = CropBox(g, DefaultCrop)
	if r.Min.X < 80 || r.Max.X > W-80 {
		t.Errorf("toned paper: %v", r)
	}
	// never more than the clamp per edge, and tiny gains are ignored
	g = flatPage(W, H, 255)
	fillRect(g, image.Rect(W*5/1000, H*5/1000, W-W*5/1000, H-H*5/1000), 40)
	if r := CropBox(g, DefaultCrop); r != g.Bounds() {
		t.Errorf("a 0.5 %% margin was cropped: %v", r)
	}
}

func TestCropBoxCost(t *testing.T) {
	g := flatPage(2560, 3936, 255)
	art(g, 0.06)
	t0 := time.Now()
	for i := 0; i < 5; i++ {
		CropBox(g, DefaultCrop)
	}
	per := time.Since(t0) / 5
	t.Logf("CropBox 2560x3936: %v per page", per)
	if per > 40*time.Millisecond {
		t.Errorf("crop too slow: %v", per)
	}
}

// ---- border -------------------------------------------------------------------------

func TestBorderLevelFixtures(t *testing.T) {
	const W, H = 1200, 1800
	type tc struct {
		name  string
		g     *image.Gray
		sides Sides
		want  int // -1 = ambiguous
	}
	c1 := flatPage(W, H, 255)
	art(c1, 0.06)
	c2 := flatPage(W, H, 8)
	art(c2, 0.04)
	for i := 0; i < 5; i++ { // white bubbles touching both edges
		fillRect(c2, image.Rect(2, 200+i*300, 60, 260+i*300), 250)
		fillRect(c2, image.Rect(W-70, 300+i*280, W-3, 360+i*280), 250)
	}
	c3 := flatPage(W, H, 255)
	art(c3, 0.06)
	fillRect(c3, image.Rect(0, 100, 40, 100+H*40/100), 0) // black panel bleeding off the left edge
	c4 := flatPage(W, H, 255)
	fillRect(c4, image.Rect(0, 0, W, H/2), 0) // top half black, bottom half white
	c5 := flatPage(W, H, 216)
	art(c5, 0.06)
	addNoise(c5, 5, 1)
	c6 := flatPage(W, H, 255)
	art(c6, 0.06)
	addNoise(c6, 6, 2)
	c7 := flatPage(W, H, 60)
	art(c7, 0.06)
	c8 := flatPage(W, H, 6)
	art(c8, 0.06)
	addNoise(c8, 5, 3)
	c9 := flatPage(W, H, 255)
	art(c9, 0.06)
	fillRect(c9, image.Rect(W-30, 0, W, H), 40) // scanner shadow on the right only
	c10 := flatPage(500, 700, 0)
	art(c10, 0.1)
	cases := []tc{
		{"white page, art inside margins", c1, SideLeft | SideRight, 255},
		{"black page, white bubbles at the edges", c2, SideLeft | SideRight, 0},
		{"white page, black panel bleeding off the left", c3, SideLeft | SideRight, 255},
		{"top half black / bottom half white, fit-height", c4, SideLeft | SideRight, -1},
		{"same page, fit-width", c4, SideTop | SideBottom, -1},
		{"yellowed paper 216 +-5", c5, SideLeft | SideRight, 216},
		{"white scan +-6", c6, SideLeft | SideRight, 255},
		{"toned dark page 60", c7, SideLeft | SideRight, 60},
		{"near-black 6 +-5", c8, SideLeft | SideRight, 0},
		{"scanner shadow on one side only", c9, SideLeft | SideRight, -1},
		{"small black page, all four sides", c10, SideAll, 0},
	}
	for _, c := range cases {
		lvl, ok := BorderLevel(c.g, c.sides, DefaultBorder)
		switch {
		case c.want < 0 && ok:
			t.Errorf("%s: level %d, want ambiguous", c.name, lvl)
		case c.want >= 0 && !ok:
			t.Errorf("%s: ambiguous, want %d", c.name, c.want)
		case c.want >= 0 && absInt(int(lvl)-c.want) > 1:
			t.Errorf("%s: level %d, want %d", c.name, lvl, c.want)
		}
	}
}

func TestBorderLevelCostAndSides(t *testing.T) {
	g := flatPage(2560, 3936, 255)
	art(g, 0.06)
	t0 := time.Now()
	for i := 0; i < 200; i++ {
		BorderLevel(g, SideAll, DefaultBorder)
	}
	per := time.Since(t0) / 200
	if per > time.Millisecond {
		t.Errorf("sampler %v per page, want < 1 ms", per)
	}
	// which edges abut a visible border
	if s := BorderSides(1440, 2160, 1620, 2160); s != SideLeft|SideRight {
		t.Errorf("fit-height page: %v", s)
	}
	if s := BorderSides(1620, 1700, 1620, 2160); s != SideTop|SideBottom {
		t.Errorf("fit-width page: %v", s)
	}
	if s := BorderSides(1620, 2160, 1620, 2160); s != 0 {
		t.Errorf("full page: %v", s)
	}
	if s := BorderSides(1610, 2160, 1620, 2160); s != 0 {
		t.Errorf("a 5 px sliver counts as a border: %v", s)
	}
}

// ---- fit / fill ---------------------------------------------------------------------

func TestFillWhenTheAspectIsWithinOnePointFivePercent(t *testing.T) {
	const W, H = 1620, 2160
	// 1.2 % off the screen's aspect: stretched to the whole screen
	if w, h := FitDims(1000, 1316, W, H, FitContain); w != W || h != H {
		t.Errorf("1000x1316 -> %dx%d, want the full screen", w, h)
	}
	// 3 % off (1020x1320 = 0.773): contain-fit with a band, as before
	if w, h := FitDims(1020, 1320, W, H, FitContain); w != 1620 || h != 2096 {
		t.Errorf("1020x1320 -> %dx%d", w, h)
	}
	// a landscape spread is fitted whole, never filled
	if w, h := FitDims(3975, 3057, W, H, FitContain); w != 1620 || h > 1300 {
		t.Errorf("spread -> %dx%d", w, h)
	}
	// width/height fits are not touched by the fill rule
	if w, h := FitDims(1000, 1316, W, H, FitWidth); w != W || h != 2132 {
		t.Errorf("fit width -> %dx%d", w, h)
	}
}

// ---- tolerant colour decision -------------------------------------------------------

func TestTolerantColourDecision(t *testing.T) {
	grey := jpegColourOf(200, 150, greyRGBA(120))
	pl, err := DecodePlanes(grey, true)
	if err != nil || !pl.IsGray() {
		t.Fatalf("neutral page: %v gray=%v", err, pl != nil && pl.IsGray())
	}
	// a handful of coloured pixels (JPEG ringing, a tiny logo) on a B/W page
	img := noisyBW(200, 150, 12)
	pl, err = DecodePlanes(encodeJPEG(img), true)
	if err != nil {
		t.Fatal(err)
	}
	if !pl.IsGray() {
		t.Error("a B/W page with a few outliers was treated as colour")
	}
	// a sepia page: colour
	pl, err = DecodePlanes(jpegColourOf(200, 150, sepia()), true)
	if err != nil || pl.IsGray() {
		t.Errorf("sepia page is gray: %v", err)
	}
	// a colour comic page (large coloured area): colour
	pl, err = DecodePlanes(encodeJPEG(colourBars(200, 150)), true)
	if err != nil || pl.IsGray() {
		t.Errorf("colour bars are gray: %v", err)
	}
}

// ---- helpers for the colour tests ------------------------------------------------

func greyRGBA(v uint8) color.RGBA { return color.RGBA{v, v, v, 255} }

func sepia() color.RGBA { return color.RGBA{214, 186, 140, 255} }

func encodeJPEG(img image.Image) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 92})
	return b.Bytes()
}

// noisyBW is a gray page with n saturated pixels sprinkled over it.
func noisyBW(w, h, n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(40 + (x*3+y*5)%180)
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	for i := 0; i < n; i++ {
		img.SetRGBA(10+i*13%w, 7+i*29%h, color.RGBA{255, 0, 0, 255})
	}
	return img
}

func colourBars(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cols := []color.RGBA{{255, 0, 0, 255}, {0, 200, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}, {200, 0, 200, 255}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, cols[x*len(cols)/w])
		}
	}
	return img
}
