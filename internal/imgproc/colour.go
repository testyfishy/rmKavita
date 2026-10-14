package imgproc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/webp"
)

// FitMode says which edge of the screen a page is scaled to.
type FitMode int

const (
	FitContain FitMode = iota // whole page visible (width and height)
	FitWidth                  // page width = screen width (a tall page scrolls)
	FitHeight                 // page height = screen height (a wide page scrolls)
)

// MaxFitPixels bounds one fitted page: a webtoon strip fitted to the screen
// width would otherwise be tens of megabytes. Larger results are scaled down.
const MaxFitPixels = 16 << 20

// FillPermille is the aspect tolerance (thousandths) within which a page is
// stretched to fill the screen instead of leaving a thin border: 15 = 1.5 %.
const FillPermille = 15

// nearFitPct: a page that overflows the screen by no more than this percent in
// the scrolling direction is shown whole instead (one tap, not two for 40 px).
const nearFitPct = 3

// FitDims is the size a sw x sh page takes on a boxW x boxH screen. Upscaling
// is limited to 3x; the result never exceeds MaxFitPixels. Every size is whole
// pixels, so a page always lands on exact pixel boundaries.
func FitDims(sw, sh, boxW, boxH int, fit FitMode) (int, int) {
	if sw <= 0 || sh <= 0 {
		return 1, 1
	}
	const maxUp = 3.0
	scaleContain := math.Min(float64(boxW)/float64(sw), float64(boxH)/float64(sh))
	var s float64
	switch fit {
	case FitWidth:
		s = float64(boxW) / float64(sw)
		if float64(sh)*s <= float64(boxH)*(100+nearFitPct)/100 {
			s = scaleContain
		}
	case FitHeight:
		s = float64(boxH) / float64(sh)
		if float64(sw)*s <= float64(boxW)*(100+nearFitPct)/100 {
			s = scaleContain
		}
	default:
		s = scaleContain
	}
	if s > maxUp {
		s = maxUp
	}
	dw := int(math.Round(float64(sw) * s))
	dh := int(math.Round(float64(sh) * s))
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	// A page whose aspect is within FillPct of the screen's fills the screen: the
	// sliver of border the contain-fit would leave is worse than a <= 1.5 % stretch.
	if fit == FitContain && s == scaleContain && dw <= boxW && dh <= boxH {
		if (boxW-dw)*1000 <= boxW*FillPermille && (boxH-dh)*1000 <= boxH*FillPermille {
			dw, dh = boxW, boxH
		}
	}
	if dw*dh > MaxFitPixels {
		f := math.Sqrt(float64(MaxFitPixels) / (float64(dw) * float64(dh)))
		dw = max(1, int(float64(dw)*f))
		dh = max(1, int(float64(dh)*f))
	}
	return dw, dh
}

// Planes are the Y, Cb and Cr planes of a decoded page. Cb and Cr may be
// smaller than Y (chroma subsampling) and are nil for a gray page.
type Planes struct {
	Y, Cb, Cr *image.Gray
}

// Bounds is the page size.
func (p *Planes) Bounds() image.Rectangle { return p.Y.Bounds() }

// IsGray reports a page without chroma.
func (p *Planes) IsGray() bool { return p.Cb == nil }

// Rotate90 returns the planes turned a quarter clockwise.
func (p *Planes) Rotate90() *Planes {
	o := &Planes{Y: Rotate90(p.Y)}
	if p.Cb != nil {
		o.Cb, o.Cr = Rotate90(p.Cb), Rotate90(p.Cr)
	}
	return o
}

// Crop returns the planes restricted to r (page coordinates).
func (p *Planes) Crop(r image.Rectangle) *Planes {
	o := &Planes{Y: SubImage(p.Y, r)}
	if p.Cb != nil {
		sx := p.Y.Bounds().Dx()
		cx := p.Cb.Bounds().Dx()
		sy := p.Y.Bounds().Dy()
		cy := p.Cb.Bounds().Dy()
		cr := image.Rect(r.Min.X*cx/sx, r.Min.Y*cy/sy, (r.Max.X*cx+sx-1)/sx, (r.Max.Y*cy+sy-1)/sy).Intersect(p.Cb.Bounds())
		if cr.Empty() {
			return o
		}
		o.Cb, o.Cr = SubImage(p.Cb, cr), SubImage(p.Cr, cr)
	}
	return o
}

// neutralChroma is how far a chroma sample may stray from 128 for a pixel to
// count as gray (scanner noise in a "colour" JPEG of a black-and-white page).
const neutralChroma = 3

// A page is colour only when more than colourFracPermille thousandths of its
// chroma samples stray from neutral by more than colourDev: one JPEG ringing
// outlier must not turn a black-and-white page into a colour page (KCC uses the
// same trimmed-histogram idea).
const (
	colourDev          = 10
	colourFracPermille = 2 // 0.2 %
)

// DecodePlanes decodes JPEG, PNG, WebP or GIF. With colour false only the
// luminance is kept (a gray page costs a third of an RGB one); with colour true
// pages whose chroma is all but neutral also come back gray.
func DecodePlanes(data []byte, colour bool) (pl *Planes, err error) {
	defer func() {
		if r := recover(); r != nil {
			pl, err = nil, fmt.Errorf("image decoder panic: %v", r)
		}
	}()
	if len(data) < 12 {
		return nil, errors.New("image too short")
	}
	format := sniff(data)
	if format == "" {
		return nil, fmt.Errorf("unsupported image format (first bytes % x)", data[:8])
	}
	if err := checkSize(data); err != nil {
		return nil, err
	}
	var img image.Image
	switch format {
	case "jpeg":
		img, err = jpeg.Decode(bytes.NewReader(data))
	case "png":
		img, err = png.Decode(bytes.NewReader(data))
	case "webp":
		img, err = webp.Decode(bytes.NewReader(data))
	case "gif":
		img, err = gif.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, fmt.Errorf("%s decode: %w", format, err)
	}
	if !colour {
		return &Planes{Y: ToGray(img)}, nil
	}
	return planesOf(img), nil
}

func chromaDims(w, h int, r image.YCbCrSubsampleRatio) (int, int) {
	switch r {
	case image.YCbCrSubsampleRatio420:
		return (w + 1) / 2, (h + 1) / 2
	case image.YCbCrSubsampleRatio422:
		return (w + 1) / 2, h
	case image.YCbCrSubsampleRatio440:
		return w, (h + 1) / 2
	case image.YCbCrSubsampleRatio411:
		return (w + 3) / 4, h
	case image.YCbCrSubsampleRatio410:
		return (w + 3) / 4, (h + 1) / 2
	}
	return w, h
}

func planesOf(img image.Image) *Planes {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	switch m := img.(type) {
	case *image.Gray:
		return &Planes{Y: ToGray(m)}
	case *image.YCbCr:
		if b.Min == (image.Point{}) {
			cw, ch := chromaDims(w, h, m.SubsampleRatio)
			p := &Planes{
				Y:  &image.Gray{Pix: m.Y, Stride: m.YStride, Rect: image.Rect(0, 0, w, h)},
				Cb: &image.Gray{Pix: m.Cb, Stride: m.CStride, Rect: image.Rect(0, 0, cw, ch)},
				Cr: &image.Gray{Pix: m.Cr, Stride: m.CStride, Rect: image.Rect(0, 0, cw, ch)},
			}
			if !hasColour(p.Cb, p.Cr) {
				p.Cb, p.Cr = nil, nil
			}
			return p
		}
	}
	// Anything else (PNG, lossless WebP, GIF, an offset YCbCr): convert once to
	// three full-size planes, or to one when the page turns out to be gray.
	y := image.NewGray(image.Rect(0, 0, w, h))
	cb := image.NewGray(image.Rect(0, 0, w, h))
	cr := image.NewGray(image.Rect(0, 0, w, h))
	deviant := 0
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < w; xx++ {
			r16, g16, b16, a16 := img.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
			// composite over white
			r8 := int(r16>>8) + (255 - int(a16>>8))
			g8 := int(g16>>8) + (255 - int(a16>>8))
			b8 := int(b16>>8) + (255 - int(a16>>8))
			if r8 > 255 {
				r8 = 255
			}
			if g8 > 255 {
				g8 = 255
			}
			if b8 > 255 {
				b8 = 255
			}
			yv, cbv, crv := color.RGBToYCbCr(uint8(r8), uint8(g8), uint8(b8))
			o := yy*y.Stride + xx
			y.Pix[o], cb.Pix[o], cr.Pix[o] = yv, cbv, crv
			if absInt(int(cbv)-128) > colourDev || absInt(int(crv)-128) > colourDev {
				deviant++
			}
		}
	}
	if deviant*1000 <= w*h*colourFracPermille {
		return &Planes{Y: y}
	}
	return &Planes{Y: y, Cb: cb, Cr: cr}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// hasColour applies the tolerant test to the chroma planes of a page.
func hasColour(cb, cr *image.Gray) bool {
	dev, total := 0, 0
	for _, p := range []*image.Gray{cb, cr} {
		b := p.Bounds()
		for y := 0; y < b.Dy(); y++ {
			row := p.Pix[y*p.Stride : y*p.Stride+b.Dx()]
			for _, v := range row {
				if int(v) < 128-colourDev || int(v) > 128+colourDev {
					dev++
				}
			}
			total += b.Dx()
		}
	}
	return dev*1000 > total*colourFracPermille
}

// Fitted is a page scaled to its place on the screen, held in the cheapest
// form that keeps what will be shown: 8-bit gray, or RGB565 (the panel's own
// format) for a page with colour. Exactly one of Gray and Col is set.
type Fitted struct {
	W, H int
	Gray []uint8
	Col  []uint16

	// Border is the luminance the margin next to this page should take (valid
	// when BorderOK): measured on the uncropped source, before fitting.
	Border   uint8
	BorderOK bool
}

// Bytes is the memory the page holds.
func (f *Fitted) Bytes() int { return len(f.Gray) + 2*len(f.Col) }

// IsGray reports a page without colour.
func (f *Fitted) IsGray() bool { return f.Col == nil }

// FromGray wraps an 8-bit gray image (no copy when it owns its pixels).
func FromGray(g *image.Gray) *Fitted {
	b := g.Bounds()
	f := &Fitted{W: b.Dx(), H: b.Dy()}
	if g.Stride == f.W && b.Min == (image.Point{}) {
		f.Gray = g.Pix[:f.W*f.H]
		return f
	}
	f.Gray = make([]uint8, f.W*f.H)
	for y := 0; y < f.H; y++ {
		copy(f.Gray[y*f.W:(y+1)*f.W], g.Pix[g.PixOffset(b.Min.X, b.Min.Y+y):])
	}
	return f
}

var (
	lut5 [256]uint16 // 8-bit -> 5-bit, rounded
	lut6 [256]uint16
)

func init() {
	for i := range lut5 {
		lut5[i] = uint16((i*31 + 127) / 255)
		lut6[i] = uint16((i*63 + 127) / 255)
	}
}

// FitPlanes scales planes to dw x dh and returns the fitted page. gamma (1 = off) is
// applied to the luminance. Gray pages stay 8-bit gray. Chroma is resized to half
// the resolution in each direction and replicated on packing: it is subsampled in
// the source anyway, it halves the work and the temporary memory of a colour page,
// and the colour layer of the panel is coarser than its luminance.
func FitPlanes(pl *Planes, dw, dh int, k Kernel, gamma float64) *Fitted {
	y := ResizeK(pl.Y, dw, dh, k)
	Gamma(y, gamma)
	if pl.Cb == nil {
		return FromGray(y)
	}
	cw, ch := (dw+1)/2, (dh+1)/2
	cb := ResizeK(pl.Cb, cw, ch, CatmullRom)
	cr := ResizeK(pl.Cr, cw, ch, CatmullRom)
	f := &Fitted{W: dw, H: dh, Col: make([]uint16, dw*dh)}
	for py := 0; py < dh; py++ {
		yrow := y.Pix[py*y.Stride : py*y.Stride+dw]
		cbrow := cb.Pix[(py/2)*cb.Stride:]
		crrow := cr.Pix[(py/2)*cr.Stride:]
		out := f.Col[py*dw : (py+1)*dw]
		for px, yv := range yrow {
			cbv, crv := int(cbrow[px/2])-128, int(crrow[px/2])-128
			if cbv >= -neutralChroma && cbv <= neutralChroma && crv >= -neutralChroma && crv <= neutralChroma {
				v5 := lut5[yv]
				out[px] = v5<<11 | (v5<<1|v5>>4)<<5 | v5 // neutral gray: no tint
				continue
			}
			yy := int(yv) << 16
			r := (yy + 91881*crv + 1<<15) >> 16
			g := (yy - 22554*cbv - 46802*crv + 1<<15) >> 16
			bl := (yy + 116130*cbv + 1<<15) >> 16
			out[px] = lut5[clampU8(r)]<<11 | lut6[clampU8(g)]<<5 | lut5[clampU8(bl)]
		}
	}
	return f
}

func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Gray565 maps an 8-bit gray to the RGB565 value of a neutral gray (the same
// table the panel backend uses for the UI canvas).
var Gray565 [256]uint16

func init() {
	for g := range Gray565 {
		v5 := lut5[g]
		Gray565[g] = v5<<11 | (v5<<1|v5>>4)<<5 | v5
	}
}
