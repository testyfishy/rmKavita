package imgproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"testing"
	"time"
)

// TestLargePageTimings prints decode and fit timings for pages much larger
// than the screen. It only runs with RMK_BENCH=1:
//
//	RMK_BENCH=1 go test -run LargePageTimings -v ./internal/imgproc
func TestLargePageTimings(t *testing.T) {
	if os.Getenv("RMK_BENCH") == "" {
		t.Skip("set RMK_BENCH=1")
	}
	for _, sz := range [][2]int{{1200, 1800}, {2000, 3000}, {2500, 3800}} {
		w, h := sz[0], sz[1]
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := 128 + 100*math.Sin(float64(x)/9)*math.Cos(float64(y)/13)
				if (x/200+y/300)%2 == 0 {
					v = 255 - v/4
				}
				c := uint8(v)
				img.SetRGBA(x, y, color.RGBA{c, uint8(int(c) * 9 / 10), uint8(int(c) * 8 / 10), 255})
			}
		}
		var colour, gray bytes.Buffer
		jpeg.Encode(&colour, img, &jpeg.Options{Quality: 88})
		g := image.NewGray(img.Bounds())
		for i := 0; i < w*h; i++ {
			g.Pix[i] = img.Pix[4*i]
		}
		jpeg.Encode(&gray, g, &jpeg.Options{Quality: 88})
		for _, c := range []struct {
			name string
			data []byte
		}{{"gray JPEG", gray.Bytes()}, {"colour JPEG", colour.Bytes()}} {
			for _, col := range []bool{false, true} {
				t0 := time.Now()
				pl, err := DecodePlanes(c.data, col)
				if err != nil {
					t.Fatal(err)
				}
				td := time.Since(t0)
				dw, dh := FitDims(w, h, 1620, 2160, FitContain)
				t1 := time.Now()
				f := FitPlanes(pl, dw, dh, CatmullRom, 1)
				tf := time.Since(t1)
				t.Logf("%dx%d %-11s (%4d KB) colour=%-5v decode %4d ms  fit->%dx%d %4d ms  held %d KB",
					w, h, c.name, len(c.data)/1024, col, td.Milliseconds(), dw, dh, tf.Milliseconds(), f.Bytes()/1024)
			}
		}
	}
}
