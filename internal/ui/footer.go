package ui

import (
	"fmt"
	"image"
	"math"

	"rmkavita/internal/imgproc"
)

// The footer is a thin, always-visible status strip under the page, like
// KOReader's: page n/N, percent, pages left, and a progress line. It is outside
// the page image (pages are fitted into the area above it), opaque, and part of
// the same screen update as the page, so it costs no extra update or flash.

// statusH is the strip's height: about 0.13 inch at the device density (30 px at
// 229 dpi), or 0 when the footer is switched off.
func (a *App) statusH() int {
	if !a.Set.Footer {
		return 0
	}
	return max(24, a.u(30))
}

// vh is the height of the area a page is fitted into and scrolls in.
func (a *App) vh() int { return a.H - a.statusH() }

// readProgress is how far through the chapter the reader is, 0..1, and the
// page counts the footer prints.
func (a *App) readProgress(r *reader) (frac float64, cur, total int) {
	total = r.count
	cur = r.page
	if r.unknown || total <= 0 {
		return 0, cur, 0
	}
	f := float64(cur+1) / float64(total)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f, cur, total
}

// statusRect is the footer strip on the screen.
func (a *App) statusRect() image.Rectangle {
	return image.Rect(0, a.vh(), a.W, a.H)
}

// luminance of a grey level in the WCAG sense (sRGB, 0 black .. 1 white).
func relLuminance(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// contrastRatio is the WCAG contrast ratio of two grey levels (1 .. 21).
func contrastRatio(a, b uint8) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// inkOn is the text colour for a background: black or white, whichever has the
// higher contrast. The worse of the two backgrounds (a grey of luminance 0.18)
// still gets 4.58:1, above the 4.5:1 that is asked for.
func inkOn(bg uint8) uint8 {
	if contrastRatio(0, bg) >= contrastRatio(255, bg) {
		return 0
	}
	return 255
}

// drawStatus paints the footer into the canvas: the strip takes the colour of
// the page border it sits on (so a black border gives a black strip) and its
// text, rule and progress line the colour of highest contrast with it. It is
// registered as opaque by composeReader: on a colour page the colour layer is
// authoritative everywhere else, and the strip would come out as the empty
// border colour of that layer.
func (a *App) drawStatus(r *reader, bg uint8) {
	h := a.statusH()
	if h == 0 {
		return
	}
	c := a.cv
	strip := a.statusRect()
	y0 := strip.Min.Y
	fg := inkOn(bg)
	c.Fill(strip, bg)
	c.Fill(image.Rect(0, y0, a.W, y0+max(2, a.u(2))), fg)
	f, cur, total := a.readProgress(r)
	// the progress line: the ink for what is behind, a softer line for what is left
	by := y0 + max(3, a.u(4))
	bh := max(3, a.u(4))
	track := uint8((int(bg) + int(fg)) / 2)
	if a.speedMode() && a.Cfg.SpeedLevels == 2 {
		track = bg // two levels have no grey for the remaining part of the line
	}
	c.Fill(image.Rect(0, by, a.W, by+bh), track)
	c.Fill(image.Rect(0, by, int(f*float64(a.W)), by+bh), fg)
	s := a.st(21, true, fg)
	var label string
	if total > 0 {
		left := total - (cur + 1)
		if left < 0 {
			left = 0
		}
		label = fmt.Sprintf("page %d/%d    %d%%    %d left", cur+1, total, int(f*100+0.5), left)
	} else {
		label = fmt.Sprintf("page %d", cur+1)
	}
	m := a.margin()
	a.Fonts.Draw(c, m, a.H-max(4, a.u(5)), a.Fonts.Ellipsize(label, a.W-2*m, s), s)
	if a.speedMode() {
		// the strip is made of the page's levels only: no anti-aliased greys that
		// a binary waveform would draw as noise
		lut := imgproc.QuantLUT(a.Cfg.SpeedLevels)
		g := c.Gray
		for y := strip.Min.Y; y < strip.Max.Y && y < a.H; y++ {
			row := g.Pix[y*g.Stride : y*g.Stride+a.W]
			for x, v := range row {
				row[x] = lut[v]
			}
		}
	}
}
