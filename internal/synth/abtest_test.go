package synth

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"

	"rmkavita/internal/gfx"
	"rmkavita/internal/imgproc"
)

func TestABChapterContent(t *testing.T) {
	fonts, err := gfx.NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	o := ABOptions{W: 1280, H: 1968, SpreadW: 1988, SpreadH: 1528, Quality: 85}
	pages := ABChapter(fonts, o)
	if len(pages) != 12 {
		t.Fatalf("%d pages, want 12", len(pages))
	}
	names := map[string]ABPage{}
	for i, p := range pages {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(p.JPEG))
		if err != nil {
			t.Fatalf("page %d (%s): %v", i+1, p.Name, err)
		}
		w, h := o.W, o.H
		if p.Name == "spread" {
			w, h = o.SpreadW, o.SpreadH
		}
		if cfg.Width != w || cfg.Height != h {
			t.Errorf("page %d (%s): %dx%d, want %dx%d", i+1, p.Name, cfg.Width, cfg.Height, w, h)
		}
		names[p.Name] = p
	}
	// colour bars are colour, every other page is gray
	for name, p := range names {
		pl, err := imgproc.DecodePlanes(p.JPEG, true)
		if err != nil {
			t.Fatal(err)
		}
		if (name == "colour-bars") == pl.IsGray() {
			t.Errorf("%s: gray=%v", name, pl.IsGray())
		}
	}
	// the ramp has 16 distinct plateaus in its first band
	g, err := imgproc.DecodeGray(names["grey-ramp"].JPEG)
	if err != nil {
		t.Fatal(err)
	}
	levels := map[uint8]bool{}
	y := o.H/6 + o.H/16
	for i := 0; i < 16; i++ {
		x := o.W/20 + i*(o.W-o.W/10)/16 + (o.W-o.W/10)/32
		v := g.Pix[y*g.Stride+x]
		want := uint8(i * 17)
		if d := int(v) - int(want); d < -6 || d > 6 {
			t.Errorf("ramp step %d = %d, want about %d", i, v, want)
		}
		levels[v/8] = true
	}
	if len(levels) < 14 {
		t.Errorf("only %d distinguishable ramp levels", len(levels))
	}
	// the screentones have the coverage they claim
	for _, tc := range []struct {
		name string
		cov  float64
	}{{"screentone-20", 0.20}, {"screentone-40", 0.40}, {"screentone-60", 0.60}} {
		g, err := imgproc.DecodeGray(names[tc.name].JPEG)
		if err != nil {
			t.Fatal(err)
		}
		r := image.Rect(o.W/4, o.H/4, o.W*3/4, o.H*3/4)
		var sum, n int
		for yy := r.Min.Y; yy < r.Max.Y; yy++ {
			for xx := r.Min.X; xx < r.Max.X; xx++ {
				sum += 255 - int(g.Pix[yy*g.Stride+xx])
				n++
			}
		}
		got := float64(sum) / float64(n) / 255
		if got < tc.cov-0.04 || got > tc.cov+0.04 {
			t.Errorf("%s coverage %.3f, want about %.2f", tc.name, got, tc.cov)
		}
	}
	// the black page is black at the edges and has white bubbles touching them
	g, _ = imgproc.DecodeGray(names["black-bubbles"].JPEG)
	if v := g.Pix[(o.H-10)*g.Stride+o.W/2]; v > 30 {
		t.Errorf("black page bottom = %d", v)
	}
	lvl, ok := imgproc.BorderLevel(g, imgproc.SideLeft|imgproc.SideRight, imgproc.DefaultBorder)
	if !ok || lvl != 0 {
		t.Errorf("border of the black page = %d ok=%v", lvl, ok)
	}
}

func TestABChapterRealSizeBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("real-size pages")
	}
	fonts, _ := gfx.NewFonts()
	// one real-size page: bytes within the range real comic pages have (1.5 - 3 MB)
	o := DefaultAB
	g := gfx.NewCanvas(o.W, o.H)
	halftone(g, image.Rect(0, 0, o.W, o.H), 0.4, 9)
	soften(g.Gray)
	label(g, fonts, o.W, "x")
	b := encodeGray(g.Gray, o.Quality)
	if len(b) < 300<<10 || len(b) > 6<<20 {
		t.Errorf("a screentone page is %d bytes", len(b))
	}
	t.Logf("real-size screentone page: %.2f MB", float64(len(b))/1e6)
}

func TestSpeedChapterPairHasTheSameSixPagesGreyAndColour(t *testing.T) {
	fonts, err := gfx.NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	o := ABOptions{W: 640, H: 984, Quality: 80}
	grey := SpeedChapter(fonts, o, false)
	col := SpeedChapter(fonts, o, true)
	if len(grey) != 6 || len(col) != 6 {
		t.Fatalf("%d and %d pages, want 6 and 6", len(grey), len(col))
	}
	for i := range grey {
		pg, err := imgproc.DecodePlanes(grey[i].JPEG, true)
		if err != nil {
			t.Fatal(err)
		}
		pc, err := imgproc.DecodePlanes(col[i].JPEG, true)
		if err != nil {
			t.Fatal(err)
		}
		if !pg.IsGray() {
			t.Errorf("grey page %d has colour", i+1)
		}
		if pc.IsGray() && i != 2 && i != 3 { // text and black-panel pages have little mid grey
			t.Errorf("colour page %d is grey", i+1)
		}
		if pg.Bounds() != pc.Bounds() {
			t.Errorf("page %d: sizes differ", i+1)
		}
	}
}
