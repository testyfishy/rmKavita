package imgproc

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestQuantLUTIsMonotonicAndHitsExactlyTheLevels(t *testing.T) {
	for _, n := range []int{2, 4, 16} {
		l := QuantLUT(n)
		set := map[uint8]bool{}
		for i := 0; i < n; i++ {
			set[LevelValue(i, n)] = true
		}
		if len(set) != n {
			t.Fatalf("%d levels: %d distinct values", n, len(set))
		}
		seen := map[uint8]bool{}
		for v := 0; v < 256; v++ {
			if v > 0 && l[v] < l[v-1] {
				t.Errorf("n=%d: not monotonic at %d (%d < %d)", n, v, l[v], l[v-1])
			}
			if !set[l[v]] {
				t.Errorf("n=%d: %d maps to %d, not a level", n, v, l[v])
			}
			seen[l[v]] = true
		}
		if len(seen) != n {
			t.Errorf("n=%d: the output uses %d of the levels", n, len(seen))
		}
		if l[0] != 0 || l[255] != 255 {
			t.Errorf("n=%d: ends map to %d and %d", n, l[0], l[255])
		}
		// nearest: no input is more than half a step from its level
		step := 255 / (n - 1)
		for v := 0; v < 256; v++ {
			d := int(l[v]) - v
			if d < 0 {
				d = -d
			}
			if d > step/2+1 {
				t.Errorf("n=%d: %d -> %d is %d away (half step %d)", n, v, l[v], d, step/2)
			}
		}
	}
	if got := QuantLUT(4); got[0] != 0 || got[100] != 85 || got[200] != 170 || got[250] != 255 {
		t.Errorf("4 levels: %d %d %d %d", got[0], got[100], got[200], got[250])
	}
}

func noisePage(w, h int, seed int64) []uint8 {
	r := rand.New(rand.NewSource(seed))
	g := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g[y*w+x] = uint8((x*255/w + y*255/h) / 2) // a smooth diagonal ramp ...
			if r.Intn(7) == 0 {
				g[y*w+x] = uint8(r.Intn(256)) // ... with salt
			}
		}
	}
	return g
}

func TestQuantiseOutputSetDeterminismAndIdempotence(t *testing.T) {
	const w, h = 97, 61
	for _, n := range []int{2, 4, 16} {
		set := map[uint8]bool{}
		for i := 0; i < n; i++ {
			set[LevelValue(i, n)] = true
		}
		for _, d := range []Dither{DitherNone, DitherOrdered, DitherDiffuse} {
			a := noisePage(w, h, 1)
			b := append([]uint8(nil), a...)
			Quantise(a, w, h, n, d)
			Quantise(b, w, h, n, d)
			if !bytes.Equal(a, b) {
				t.Errorf("n=%d %s: two runs differ", n, d)
			}
			for i, v := range a {
				if !set[v] {
					t.Fatalf("n=%d %s: pixel %d = %d is not a level", n, d, i, v)
				}
			}
			// the look-up itself is idempotent (the white and black points are
			// stretched on the way in, so a whole second pass is not)
			lut := QuantLUT(n)
			for i, v := range a {
				if lut[v] != v {
					t.Fatalf("n=%d %s: look-up of level %d at %d is %d", n, d, v, i, lut[v])
				}
			}
			// the mean is kept (within a level step) by the dithers
			var sa, so int
			orig := noisePage(w, h, 1)
			for i := range a {
				sa += int(a[i])
				so += int(orig[i])
			}
			if d != DitherNone {
				if diff := (sa - so) / (w * h); diff < -6 || diff > 6 {
					t.Errorf("n=%d %s: the mean moved by %d", n, d, diff)
				}
			}
		}
	}
}

func TestQuantisePreservesFlatBlackWhiteAndLineArt(t *testing.T) {
	const w, h = 64, 64
	for _, d := range []Dither{DitherNone, DitherOrdered, DitherDiffuse} {
		for _, n := range []int{2, 4, 16} {
			g := make([]uint8, w*h)
			for i := range g {
				g[i] = 255
			}
			for x := 0; x < w; x++ { // a 3 px black line and a black block
				for y := 20; y < 23; y++ {
					g[y*w+x] = 0
				}
			}
			for y := 40; y < 60; y++ {
				for x := 10; x < 30; x++ {
					g[y*w+x] = 0
				}
			}
			ref := append([]uint8(nil), g...)
			Quantise(g, w, h, n, d)
			if !bytes.Equal(g, ref) {
				t.Errorf("n=%d %s: pure black and white changed", n, d)
			}
		}
	}
}

func TestDitherParsing(t *testing.T) {
	for in, want := range map[string]Dither{"none": DitherNone, "ordered": DitherOrdered, "diffuse": DitherDiffuse, "": DitherOrdered, "x": DitherOrdered} {
		if got := ParseDither(in); got != want {
			t.Errorf("%q -> %v", in, got)
		}
	}
	if !ValidLevels(2) || !ValidLevels(4) || !ValidLevels(16) || ValidLevels(3) || ValidLevels(256) {
		t.Error("ValidLevels")
	}
}

func BenchmarkQuantise4OrderedFullPage(b *testing.B) {
	g := noisePage(1405, 2130, 2)
	b.SetBytes(int64(len(g)))
	for i := 0; i < b.N; i++ {
		Quantise(g, 1405, 2130, 4, DitherOrdered)
	}
}

func BenchmarkQuantise4DiffuseFullPage(b *testing.B) {
	g := noisePage(1405, 2130, 2)
	b.SetBytes(int64(len(g)))
	for i := 0; i < b.N; i++ {
		Quantise(g, 1405, 2130, 4, DitherDiffuse)
	}
}

func TestClipTableAndTheWhiteAndBlackPoints(t *testing.T) {
	c := ClipTable()
	if c[0] != 0 || c[ClipLow] != 0 || c[255] != 255 || c[ClipHigh] != 255 {
		t.Fatalf("end points: %d %d %d %d", c[0], c[ClipLow], c[ClipHigh], c[255])
	}
	for v := 1; v < 256; v++ {
		if c[v] < c[v-1] {
			t.Fatalf("not monotonic at %d", v)
		}
	}
	// paper grain and near-black ink go to the end levels with every reduction
	for _, n := range []int{2, 4, 16} {
		for _, d := range []Dither{DitherNone, DitherOrdered, DitherDiffuse} {
			g := make([]uint8, 64*64)
			for i := range g {
				if i%2 == 0 {
					g[i] = 247 // grain on white paper
				} else {
					g[i] = 252
				}
			}
			Quantise(g, 64, 64, n, d)
			for i, v := range g {
				if v != 255 {
					t.Fatalf("n=%d %s: paper %d at %d", n, d, v, i)
				}
			}
			for i := range g {
				g[i] = uint8(i % 7) // 0..6: ink
			}
			Quantise(g, 64, 64, n, d)
			for i, v := range g {
				if v != 0 {
					t.Fatalf("n=%d %s: ink %d at %d", n, d, v, i)
				}
			}
		}
	}
}
