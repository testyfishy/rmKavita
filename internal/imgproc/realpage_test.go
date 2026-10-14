package imgproc

import (
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"rmkavita/internal/gfx"
	"rmkavita/internal/synth"
)

func pct(v []float64, p float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

// TestRealSizePages times decode and fit of pages at the real size of the
// owner's comics (2560 x 3936 and the 3975 x 3057 spread), black and white and
// colour, over several runs, and compares the three kernels on a screentone
// page. It only runs with RMK_BENCH=1; with RMK_RENDER_DIR it also writes a crop
// of each kernel's output:
//
//	RMK_BENCH=1 RMK_RENDER_DIR=out go test -run RealSizePages -v ./internal/imgproc
func TestRealSizePages(t *testing.T) {
	if os.Getenv("RMK_BENCH") == "" {
		t.Skip("set RMK_BENCH=1")
	}
	fonts, err := gfx.NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	pages := synth.ABChapter(fonts, synth.DefaultAB)
	by := map[string][]byte{}
	for _, p := range pages {
		by[p.Name] = p.JPEG
	}
	const runs = 7
	for _, c := range []struct {
		label, name string
	}{{"2560x3936 screentone 40 %", "screentone-40"}, {"2560x3936 typical page", "typical"}, {"3975x3057 spread", "spread"}, {"2560x3936 colour bars", "colour-bars"}} {
		data := by[c.name]
		for _, col := range []bool{false, true} {
			var dec, fit []float64
			var dw, dh int
			for i := 0; i < runs; i++ {
				t0 := time.Now()
				pl, err := DecodePlanes(data, col)
				if err != nil {
					t.Fatal(err)
				}
				dec = append(dec, float64(time.Since(t0).Microseconds())/1000)
				b := pl.Bounds()
				dw, dh = FitDims(b.Dx(), b.Dy(), 1620, 2160, FitContain)
				t1 := time.Now()
				FitPlanes(pl, dw, dh, CatmullRom, 1)
				fit = append(fit, float64(time.Since(t1).Microseconds())/1000)
			}
			t.Logf("%-26s %5d KB colour=%-5v -> %4dx%d  decode p50 %4.0f p95 %4.0f | fit p50 %4.0f p95 %4.0f ms | sum p95 %4.0f ms (x5 = %4.0f)",
				c.label, len(data)/1024, col, dw, dh, pct(dec, 50), pct(dec, 95), pct(fit, 50), pct(fit, 95), pct(dec, 95)+pct(fit, 95), 5*(pct(dec, 95)+pct(fit, 95)))
		}
	}

	// kernels on the three screentone densities
	type kcase struct {
		name string
		k    Kernel
	}
	kernels := []kcase{{"catmull-rom", CatmullRom}, {"lanczos3", Lanczos3}, {"smooth", Smooth}}
	for _, name := range []string{"screentone-20", "screentone-40", "screentone-60"} {
		g, err := DecodeGray(by[name])
		if err != nil {
			t.Fatal(err)
		}
		dw, dh := FitDims(g.Bounds().Dx(), g.Bounds().Dy(), 1620, 2160, FitContain)
		for _, kc := range kernels {
			t0 := time.Now()
			out := ResizeK(g, dw, dh, kc.k)
			el := time.Since(t0)
			// moire: spread of 16 x 16 block means inside the tone (a flat screen
			// should be flat once resampled; beats between the dot pitch and the
			// pixel grid show up as low-frequency variation)
			r := image.Rect(dw/8, dh/8, dw-dw/8, dh-dh/8)
			var means []float64
			for by0 := r.Min.Y; by0+16 <= r.Max.Y; by0 += 16 {
				for bx := r.Min.X; bx+16 <= r.Max.X; bx += 16 {
					s := 0
					for y := 0; y < 16; y++ {
						row := out.Pix[(by0+y)*out.Stride+bx:]
						for x := 0; x < 16; x++ {
							s += int(row[x])
						}
					}
					means = append(means, float64(s)/256)
				}
			}
			var mu, sd float64
			for _, m := range means {
				mu += m
			}
			mu /= float64(len(means))
			for _, m := range means {
				sd += (m - mu) * (m - mu)
			}
			sd = math.Sqrt(sd / float64(len(means)))
			// dot contrast: how much of the screentone's texture survives
			var tmin, tmax float64 = 255, 0
			var sum2 float64
			n := 0
			for y := r.Min.Y; y < r.Min.Y+200; y++ {
				for x := r.Min.X; x < r.Min.X+200; x++ {
					v := float64(out.Pix[y*out.Stride+x])
					tmin, tmax = math.Min(tmin, v), math.Max(tmax, v)
					sum2 += (v - mu) * (v - mu)
					n++
				}
			}
			t.Logf("%-14s %-12s %4.0f ms  mean %5.1f  block-mean sd %5.2f (moire)  texture rms %5.1f  min/max %3.0f/%3.0f",
				name, kc.name, float64(el.Microseconds())/1000, mu, sd, math.Sqrt(sum2/float64(n)), tmin, tmax)
			if dir := os.Getenv("RMK_RENDER_DIR"); dir != "" && name == "screentone-40" {
				crop := image.NewGray(image.Rect(0, 0, 400, 400))
				for y := 0; y < 400; y++ {
					copy(crop.Pix[y*crop.Stride:y*crop.Stride+400], out.Pix[(r.Min.Y+y)*out.Stride+r.Min.X:])
				}
				f, err := os.Create(filepath.Join(dir, "kernel-"+kc.name+".png"))
				if err == nil {
					png.Encode(f, crop)
					f.Close()
				}
			}
		}
	}

	// ringing on a hard edge between two greys (64 | 192), downscaled by 2560/1405
	edge := image.NewGray(image.Rect(0, 0, 2560, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 2560; x++ {
			v := uint8(64)
			if x >= 1280 {
				v = 192
			}
			edge.Pix[y*edge.Stride+x] = v
		}
	}
	for _, kc := range kernels {
		out := ResizeK(edge, 1405, 110, kc.k)
		lo, hi := 255, 0
		for x := 0; x < 1405; x++ {
			v := int(out.Pix[40*out.Stride+x])
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		// width of the transition (10 % .. 90 % of the step)
		w := 0
		for x := 0; x < 1405; x++ {
			v := int(out.Pix[40*out.Stride+x])
			if v > 64+13 && v < 192-13 {
				w++
			}
		}
		t.Logf("edge 64|192 %-12s undershoot %2d  overshoot %2d  transition %d px", kc.name, 64-lo, hi-192, w)
	}
}
