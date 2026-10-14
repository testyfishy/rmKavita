package imgproc

import (
	"image"
	"math"
)

// ---- auto-crop ------------------------------------------------------------------

// CropParams tune CropBox.
type CropParams struct {
	Tol      int     // a pixel is content when it differs from the background by more than this
	RowFrac  float64 // a row or column is content when more than this fraction of it is
	ClampPct int     // never remove more than this percent of a dimension per edge
	Step     int     // sample every Step-th pixel in both directions
	MinGain  float64 // ignore a crop that removes less than this fraction of the area
}

// DefaultCrop is the shipped setting (background-aware, 1 % rows, 10 % clamp).
var DefaultCrop = CropParams{Tol: 16, RowFrac: 0.01, ClampPct: 10, Step: 2, MinGain: 0.03}

// CropBox returns the part of src worth showing: the page without its blank
// margins, or src's bounds when nothing worthwhile can be removed. The
// background is the most common luminance of a thin frame around the page
// (measured, not assumed white: a washed-out scan's white is ~215, a black
// page is black), content is whatever differs from it, and no edge moves by
// more than ClampPct of the dimension.
func CropBox(src *image.Gray, p CropParams) image.Rectangle {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 64 || h < 64 {
		return b
	}
	step := max(p.Step, 1)
	var hist [256]int
	t := max(2, min(w, h)/50)
	for y := 0; y < h; y += step {
		row := src.Pix[y*src.Stride : y*src.Stride+w]
		if y < t || y >= h-t {
			for x := 0; x < w; x += step {
				hist[row[x]]++
			}
		} else {
			for x := 0; x < t; x += step {
				hist[row[x]]++
				hist[row[w-1-x]]++
			}
		}
	}
	bg := 0
	for v := 1; v < 256; v++ {
		if hist[v] > hist[bg] {
			bg = v
		}
	}
	rows := make([]int, h)
	cols := make([]int, w)
	for y := 0; y < h; y += step {
		row := src.Pix[y*src.Stride : y*src.Stride+w]
		for x := 0; x < w; x += step {
			d := int(row[x]) - bg
			if d < 0 {
				d = -d
			}
			if d > p.Tol {
				rows[y]++
				cols[x]++
			}
		}
	}
	rl := int(p.RowFrac*float64(w/step)) + 1
	cl := int(p.RowFrac*float64(h/step)) + 1
	top, bot, left, right := 0, h, 0, w
	found := false
	for y := 0; y < h; y += step {
		if rows[y] > rl {
			top, found = y, true
			break
		}
	}
	if !found {
		return b // a blank page: leave it alone
	}
	for y := h - 1; y >= 0; y-- {
		if y%step == 0 && rows[y] > rl {
			bot = y + 1
			break
		}
	}
	for x := 0; x < w; x += step {
		if cols[x] > cl {
			left = x
			break
		}
	}
	for x := w - 1; x >= 0; x-- {
		if x%step == 0 && cols[x] > cl {
			right = x + 1
			break
		}
	}
	mx, my := w*p.ClampPct/100, h*p.ClampPct/100
	top, bot = min(top, my), max(bot, h-my)
	left, right = min(left, mx), max(right, w-mx)
	if right-left < w/4 || bot-top < h/4 {
		return b
	}
	removed := 1 - float64((right-left)*(bot-top))/float64(w*h)
	if removed < p.MinGain {
		return b
	}
	return image.Rect(b.Min.X+left, b.Min.Y+top, b.Min.X+right, b.Min.Y+bot)
}

// ---- border colour --------------------------------------------------------------

// Sides is the set of page edges that abut a visible border.
type Sides uint8

// Page edges.
const (
	SideLeft Sides = 1 << iota
	SideRight
	SideTop
	SideBottom
	SideAll = SideLeft | SideRight | SideTop | SideBottom
)

// BorderParams tune BorderLevel.
type BorderParams struct {
	BandPct   float64 // band depth as percent of min(W, H)
	BandMin   int
	BandMax   int
	Tol       int     // luminance tolerance for "flat"
	FlatFrac  float64 // fraction of a band within Tol of its median
	Disagree  int     // flat edges further apart than this: no single colour
	SnapLo    int     // <= SnapLo -> 0
	SnapHi    int     // >= SnapHi -> 255
	StepAlong int     // subsample along the edge
}

// DefaultBorder is the shipped setting.
var DefaultBorder = BorderParams{BandPct: 1.2, BandMin: 3, BandMax: 12, Tol: 24, FlatFrac: 0.80, Disagree: 96, SnapLo: 12, SnapHi: 245, StepAlong: 2}

type bandEdge struct {
	med  int
	flat bool
	n    int
}

func bandStat(src *image.Gray, r image.Rectangle, alongX bool, p BorderParams) bandEdge {
	var hist [256]int
	n := 0
	b := src.Bounds()
	r = r.Intersect(b)
	if r.Empty() {
		return bandEdge{}
	}
	st := max(p.StepAlong, 1)
	if alongX {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			row := src.Pix[(y-b.Min.Y)*src.Stride+(r.Min.X-b.Min.X):]
			for x := 0; x < r.Dx(); x += st {
				hist[row[x]]++
				n++
			}
		}
	} else {
		for y := r.Min.Y; y < r.Max.Y; y += st {
			row := src.Pix[(y-b.Min.Y)*src.Stride+(r.Min.X-b.Min.X):]
			for x := 0; x < r.Dx(); x++ {
				hist[row[x]]++
				n++
			}
		}
	}
	if n == 0 {
		return bandEdge{}
	}
	acc, med := 0, 0
	for v := 0; v < 256; v++ {
		acc += hist[v]
		if acc*2 >= n {
			med = v
			break
		}
	}
	in := 0
	for v := max(0, med-p.Tol); v <= min(255, med+p.Tol); v++ {
		in += hist[v]
	}
	return bandEdge{med: med, flat: float64(in) >= p.FlatFrac*float64(n), n: n}
}

// BorderLevel returns the luminance the border next to a page should take
// ("band median, flat fill, keep-previous-when-unsure"): the median of a thin
// band along each page edge in sides, kept only where the band is flat, the
// flat edges agree, snapped to pure black/white near the ends (the only
// dither-free fills on the panel). ok is false when the evidence is ambiguous;
// the caller then keeps the previous colour. src is the uncropped page.
func BorderLevel(src *image.Gray, sides Sides, p BorderParams) (level uint8, ok bool) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 16 || h < 16 {
		return 0, false
	}
	t := int(math.Round(p.BandPct / 100 * float64(min(w, h))))
	t = min(max(t, p.BandMin), p.BandMax)
	var es []bandEdge
	if sides&SideTop != 0 {
		es = append(es, bandStat(src, image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+t), true, p))
	}
	if sides&SideBottom != 0 {
		es = append(es, bandStat(src, image.Rect(b.Min.X, b.Max.Y-t, b.Max.X, b.Max.Y), true, p))
	}
	if sides&SideLeft != 0 {
		es = append(es, bandStat(src, image.Rect(b.Min.X, b.Min.Y, b.Min.X+t, b.Max.Y), false, p))
	}
	if sides&SideRight != 0 {
		es = append(es, bandStat(src, image.Rect(b.Max.X-t, b.Min.Y, b.Max.X, b.Max.Y), false, p))
	}
	type wv struct{ v, w int }
	var fl []wv
	tot := 0
	for _, e := range es {
		if e.flat {
			fl = append(fl, wv{e.med, e.n})
			tot += e.n
		}
	}
	if len(fl) == 0 {
		return 0, false
	}
	lo, hi := 255, 0
	for _, f := range fl {
		lo, hi = min(lo, f.v), max(hi, f.v)
	}
	if hi-lo > p.Disagree {
		return 0, false
	}
	for i := 0; i < len(fl); i++ {
		for j := i + 1; j < len(fl); j++ {
			if fl[j].v < fl[i].v {
				fl[i], fl[j] = fl[j], fl[i]
			}
		}
	}
	acc := 0
	lvl := fl[len(fl)-1].v
	for _, f := range fl {
		acc += f.w
		if acc*2 >= tot {
			lvl = f.v
			break
		}
	}
	switch {
	case lvl >= p.SnapHi:
		lvl = 255
	case lvl <= p.SnapLo:
		lvl = 0
	}
	return uint8(lvl), true
}

// BorderSides says which page edges abut a visible border when a page of
// dw x dh is shown on a boxW x boxH screen, or 0 when the border is too thin to
// matter (under 1.5 % of the screen). Edges that touch the screen edge are not
// listed.
func BorderSides(dw, dh, boxW, boxH int) Sides {
	var s Sides
	if dw < boxW {
		s |= SideLeft | SideRight
	}
	if dh < boxH {
		s |= SideTop | SideBottom
	}
	visible := boxW*boxH - dw*dh
	if s == 0 || visible*1000 < boxW*boxH*15 {
		return 0
	}
	return s
}
