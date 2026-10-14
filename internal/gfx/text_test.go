package gfx

import (
	"image"
	"os"
	"testing"
	"time"
)

func inkPixels(c *Canvas, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if c.GrayAt(x, y).Y < 128 {
				n++
			}
		}
	}
	return n
}

func TestEllipsizeAndWrapFit(t *testing.T) {
	f, err := NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	st := Style{Size: 44}
	long := "A Very Long Series Title That Must Not Overflow The Row Of The List View At All"
	if got := f.Ellipsize(long, 600, st); f.Width(got, st) > 600 || got == long {
		t.Errorf("ellipsize: %q (%d px)", got, f.Width(got, st))
	}
	for _, l := range f.Wrap(long, 600, st, 2) {
		if f.Width(l, st) > 600 {
			t.Errorf("wrapped line too wide: %q", l)
		}
	}
	if got := f.Wrap(long, 600, st, 2); len(got) != 2 {
		t.Errorf("wrap lines = %d", len(got))
	}
}

// The CJK fallback reads a variable Noto font through ReaderAt, lazily, the
// way it will on the tablet. Windows ships the same family, so the path is
// exercised here with that file.
func TestCJKFallbackFromVariableFont(t *testing.T) {
	const p = `C:\Windows\Fonts\NotoSansJP-VF.ttf`
	if _, err := os.Stat(p); err != nil {
		t.Skip("no Noto Sans JP variable font on this machine")
	}
	f, err := NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	st := Style{Size: 60}
	text := "日本語のタイトル"
	// without a fallback every glyph is a "?"
	f.SetFallbackPaths(nil)
	plain := NewCanvas(900, 120)
	wNone := f.Draw(plain, 10, 80, text, st)
	// with the fallback the glyphs are real and wider than question marks
	f.SetFallbackPaths([]string{p})
	t0 := time.Now()
	fb := NewCanvas(900, 120)
	wFb := f.Draw(fb, 10, 80, text, st)
	first := time.Since(t0)
	if wFb <= 0 || inkPixels(fb, fb.Bounds()) == 0 {
		t.Fatalf("fallback drew nothing (width %d)", wFb)
	}
	same := true
	for i := range fb.Pix {
		if fb.Pix[i] != plain.Pix[i] {
			same = false
			break
		}
	}
	if same {
		t.Errorf("fallback output equals the question-mark rendering (widths %d vs %d)", wFb, wNone)
	}
	// a second draw must be cheap: the font is parsed once
	t1 := time.Now()
	f.Draw(NewCanvas(900, 120), 10, 80, text, st)
	t.Logf("CJK fallback: first draw %v, second %v, width %d px", first, time.Since(t1), wFb)
}
