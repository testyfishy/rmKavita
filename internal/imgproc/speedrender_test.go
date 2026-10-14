package imgproc

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"rmkavita/internal/gfx"
	"rmkavita/internal/synth"
)

// TestRenderSpeedLevels writes the readability comparison of the speed mode:
// 1:1 crops (640 x 420 of the fitted 1405 x 2130 page) of a text page, a
// screentone page and a typical page at 2, 4 and 16 levels with each dither.
// It only runs with RMK_RENDER_DIR set:
//
//	RMK_RENDER_DIR=out go test -run RenderSpeedLevels ./internal/imgproc
func TestRenderSpeedLevels(t *testing.T) {
	dir := os.Getenv("RMK_RENDER_DIR")
	if dir == "" {
		t.Skip("set RMK_RENDER_DIR")
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
	crops := map[string]image.Rectangle{
		"small-text":    image.Rect(30, 60, 670, 480),
		"screentone-40": image.Rect(300, 600, 940, 1020),
		"typical":       image.Rect(80, 40, 720, 460),
	}
	for name, rc := range crops {
		g, err := DecodeGray(by[name])
		if err != nil {
			t.Fatal(err)
		}
		dw, dh := FitDims(g.Bounds().Dx(), g.Bounds().Dy(), 1620, 2130, FitContain)
		fit := ResizeK(g, dw, dh, CatmullRom)
		save := func(tag string, pix []uint8) {
			out := image.NewGray(image.Rect(0, 0, rc.Dx(), rc.Dy()))
			for y := 0; y < rc.Dy(); y++ {
				copy(out.Pix[y*out.Stride:y*out.Stride+rc.Dx()], pix[(rc.Min.Y+y)*dw+rc.Min.X:(rc.Min.Y+y)*dw+rc.Max.X])
			}
			f, err := os.Create(filepath.Join(dir, "speed-"+name+"-"+tag+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, out)
			f.Close()
		}
		save("L256", fit.Pix)
		for _, n := range []int{2, 4, 16} {
			for _, d := range []Dither{DitherNone, DitherOrdered, DitherDiffuse} {
				buf := append([]uint8(nil), fit.Pix...)
				Quantise(buf, dw, dh, n, d)
				save(itoa(n)+"-"+d.String(), buf)
			}
		}
	}
}

func itoa(n int) string {
	switch n {
	case 2:
		return "L2"
	case 4:
		return "L4"
	}
	return "L16"
}

// TestRenderSpeedSheets arranges those crops into one sheet per page: columns
// none | ordered | diffuse, rows 256 levels (the same crop three times), 2, 4,
// 16 levels.
func TestRenderSpeedSheets(t *testing.T) {
	dir := os.Getenv("RMK_RENDER_DIR")
	if dir == "" {
		t.Skip("set RMK_RENDER_DIR")
	}
	for _, name := range []string{"small-text", "screentone-40", "typical"} {
		load := func(tag string) *image.Gray {
			f, err := os.Open(filepath.Join(dir, "speed-"+name+"-"+tag+".png"))
			if err != nil {
				t.Skip("run TestRenderSpeedLevels first")
			}
			defer f.Close()
			img, err := png.Decode(f)
			if err != nil {
				t.Fatal(err)
			}
			g, _ := img.(*image.Gray)
			return g
		}
		const cw, ch = 640, 420
		sheet := image.NewGray(image.Rect(0, 0, 3*cw+20, 4*ch+30))
		for i := range sheet.Pix {
			sheet.Pix[i] = 128
		}
		put := func(g *image.Gray, col, row int) {
			for y := 0; y < ch; y++ {
				copy(sheet.Pix[(row*(ch+10)+y)*sheet.Stride+col*(cw+10):], g.Pix[y*g.Stride:y*g.Stride+cw])
			}
		}
		orig := load("L256")
		for c := 0; c < 3; c++ {
			put(orig, c, 0)
		}
		for r, n := range []int{2, 4, 16} {
			for c, d := range []string{"none", "ordered", "diffuse"} {
				put(load(itoa(n)+"-"+d), c, r+1)
			}
		}
		f, err := os.Create(filepath.Join(dir, "sheet-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, sheet)
		f.Close()
	}
}
