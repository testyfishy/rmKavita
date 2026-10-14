// Package imgproc turns downloaded manga pages into 8-bit gray bitmaps sized
// for the panel. It only keeps luminance for black-and-white reading (a gray
// page is a third of the memory of an RGB one); colour is decoded on request.
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
	"runtime"
	"sync"

	"golang.org/x/image/webp"
)

// MaxPixels bounds the decoded size we are willing to hold (a 36 MP JPEG is
// ~54 MB of planes; beyond that the tablet would be pushed into OOM).
const MaxPixels = 36 << 20

// DecodeGray decodes JPEG, PNG, WebP or GIF into an *image.Gray whose Rect
// starts at (0,0). The returned image owns its pixels.
func DecodeGray(data []byte) (g *image.Gray, err error) {
	// A decoder that panics on a hostile or truncated file must cost one page,
	// not the whole process (it runs in a background goroutine).
	defer func() {
		if r := recover(); r != nil {
			g, err = nil, fmt.Errorf("image decoder panic: %v", r)
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
	return ToGray(img), nil
}

func checkSize(data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return errors.New("empty image")
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return fmt.Errorf("image too large (%dx%d)", cfg.Width, cfg.Height)
	}
	return nil
}

func sniff(b []byte) string {
	switch {
	case b[0] == 0xFF && b[1] == 0xD8:
		return "jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "webp"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "gif"
	}
	return ""
}

// ToGray converts any image to 8-bit gray with fast paths for the types the
// decoders actually return. Transparent pixels composite over white.
func ToGray(img image.Image) *image.Gray {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	switch m := img.(type) {
	case *image.Gray:
		out := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			so := m.PixOffset(b.Min.X, b.Min.Y+y)
			copy(out.Pix[y*out.Stride:y*out.Stride+w], m.Pix[so:so+w])
		}
		return out
	case *image.YCbCr:
		// The Y plane is the gray image: no colour maths at all. When the
		// image starts at the origin the plane is used in place (a view that
		// keeps the chroma planes alive only until the caller drops it).
		if b.Min == (image.Point{}) {
			return &image.Gray{Pix: m.Y, Stride: m.YStride, Rect: m.Rect}
		}
		out := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			so := m.YOffset(b.Min.X, b.Min.Y+y)
			copy(out.Pix[y*out.Stride:y*out.Stride+w], m.Y[so:so+w])
		}
		return out
	case *image.NRGBA:
		out := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			d := out.Pix[y*out.Stride : y*out.Stride+w]
			for x := range d {
				r, g, bl, a := int(p[4*x]), int(p[4*x+1]), int(p[4*x+2]), int(p[4*x+3])
				l := (299*r + 587*g + 114*bl + 500) / 1000
				if a != 255 {
					l = (l*a + 255*(255-a) + 127) / 255
				}
				d[x] = uint8(l)
			}
		}
		return out
	case *image.RGBA:
		out := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			d := out.Pix[y*out.Stride : y*out.Stride+w]
			for x := range d {
				// premultiplied: compositing over white adds (255-a)
				r, g, bl, a := int(p[4*x]), int(p[4*x+1]), int(p[4*x+2]), int(p[4*x+3])
				l := (299*r+587*g+114*bl+500)/1000 + (255 - a)
				if l > 255 {
					l = 255
				}
				d[x] = uint8(l)
			}
		}
		return out
	case *image.Paletted:
		var lut [256]uint8
		for i, c := range m.Palette {
			if i < 256 {
				lut[i] = grayOver(c)
			}
		}
		out := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			d := out.Pix[y*out.Stride : y*out.Stride+w]
			for x := range d {
				d[x] = lut[p[x]]
			}
		}
		return out
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Pix[y*out.Stride+x] = grayOver(img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

func grayOver(c color.Color) uint8 {
	r, g, b, a := c.RGBA() // premultiplied 16-bit
	l := (299*int(r) + 587*int(g) + 114*int(b)) / 1000
	l += 0xFFFF - int(a)
	if l > 0xFFFF {
		l = 0xFFFF
	}
	return uint8(l >> 8)
}

// FitSize returns the size a w x h page takes when scaled to fit inside
// boxW x boxH (aspect preserved, never enlarged beyond maxUp times).
func FitSize(w, h, boxW, boxH int, maxUp float64) (int, int) {
	if w <= 0 || h <= 0 {
		return 1, 1
	}
	s := math.Min(float64(boxW)/float64(w), float64(boxH)/float64(h))
	if s > maxUp {
		s = maxUp
	}
	dw := int(math.Round(float64(w) * s))
	dh := int(math.Round(float64(h) * s))
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	return dw, dh
}

// Fit scales src to fit inside boxW x boxH.
func Fit(src *image.Gray, boxW, boxH int) *image.Gray {
	dw, dh := FitSize(src.Bounds().Dx(), src.Bounds().Dy(), boxW, boxH, 3)
	return Resize(src, dw, dh)
}

// Rotate90 returns src rotated a quarter turn clockwise.
func Rotate90(src *image.Gray) *image.Gray {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewGray(image.Rect(0, 0, sh, sw))
	for y := 0; y < sh; y++ {
		so := y * src.Stride
		for x := 0; x < sw; x++ {
			out.Pix[x*out.Stride+(sh-1-y)] = src.Pix[so+x]
		}
	}
	return out
}

// SubImage returns an independent, (0,0)-based copy of r from src.
func SubImage(src *image.Gray, r image.Rectangle) *image.Gray {
	out := image.NewGray(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		so := src.PixOffset(r.Min.X, r.Min.Y+y)
		copy(out.Pix[y*out.Stride:y*out.Stride+r.Dx()], src.Pix[so:so+r.Dx()])
	}
	return out
}

// ---- resampling -----------------------------------------------------------

type contrib struct {
	start int
	w     []int32 // 14-bit fixed point, sums to 1<<14
}

func catmullRom(x float64) float64 {
	x = math.Abs(x)
	switch {
	case x < 1:
		return 1.5*x*x*x - 2.5*x*x + 1
	case x < 2:
		return -0.5*x*x*x + 2.5*x*x - 4*x + 2
	}
	return 0
}

func makeContribs(srcN, dstN int, k Kernel) []contrib {
	scale := float64(srcN) / float64(dstN)
	fscale := math.Max(scale, 1)
	support := k.radius() * fscale
	out := make([]contrib, dstN)
	for i := range out {
		center := (float64(i) + 0.5) * scale
		left := int(math.Floor(center - support))
		right := int(math.Ceil(center + support))
		if left < 0 {
			left = 0
		}
		if right > srcN {
			right = srcN
		}
		if right <= left { // degenerate: nearest
			left = min(max(int(center), 0), srcN-1)
			right = left + 1
		}
		ws := make([]float64, right-left)
		var sum float64
		for j := left; j < right; j++ {
			ws[j-left] = k.weight((float64(j) + 0.5 - center) / fscale)
			sum += ws[j-left]
		}
		if sum == 0 {
			ws[(right-left)/2] = 1
			sum = 1
		}
		iw := make([]int32, len(ws))
		var isum int32
		big := 0
		for k := range ws {
			iw[k] = int32(math.Round(ws[k] / sum * (1 << 14)))
			isum += iw[k]
			if math.Abs(ws[k]) > math.Abs(ws[big]) {
				big = k
			}
		}
		iw[big] += (1 << 14) - isum // exact normalisation
		out[i] = contrib{start: left, w: iw}
	}
	return out
}

func clamp8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func parallelRows(n int, fn func(y0, y1 int)) {
	workers := runtime.NumCPU()
	if workers > 3 {
		workers = 3
	}
	if workers < 1 || n < 64 {
		workers = 1
	}
	if workers == 1 {
		fn(0, n)
		return
	}
	var wg sync.WaitGroup
	band := (n + workers - 1) / workers
	for y0 := 0; y0 < n; y0 += band {
		y1 := min(y0+band, n)
		wg.Add(1)
		go func(a, b int) {
			defer wg.Done()
			fn(a, b)
		}(y0, y1)
	}
	wg.Wait()
}

// scratch holds the intermediate image of a two-pass resize (up to 16 MB for a
// large page): taken from a small pool instead of being allocated and dropped
// for every page, which keeps the Go heap from growing by a page-sized garbage
// object per decode. The pool is emptied by every garbage collection.
var scratch sync.Pool

func getScratch(w, h int) *image.Gray {
	n := w * h
	if v := scratch.Get(); v != nil {
		if b := v.(*[]uint8); cap(*b) >= n {
			// every pixel is overwritten by the pass that fills it
			return &image.Gray{Pix: (*b)[:n], Stride: w, Rect: image.Rect(0, 0, w, h)}
		}
	}
	return image.NewGray(image.Rect(0, 0, w, h))
}

func putScratch(g *image.Gray) {
	b := g.Pix[:cap(g.Pix)]
	scratch.Put(&b)
}

func resizeH(src *image.Gray, dw int, cs []contrib) *image.Gray {
	return resizeHTo(image.NewGray(image.Rect(0, 0, dw, src.Bounds().Dy())), src, dw, cs)
}

func resizeHTo(out, src *image.Gray, dw int, cs []contrib) *image.Gray {
	sh := src.Bounds().Dy()
	parallelRows(sh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			srow := src.Pix[y*src.Stride:]
			drow := out.Pix[y*out.Stride : y*out.Stride+dw]
			for x := 0; x < dw; x++ {
				c := cs[x]
				s := srow[c.start : c.start+len(c.w)]
				var acc int32 = 1 << 13
				for k, wv := range c.w {
					acc += wv * int32(s[k])
				}
				drow[x] = clamp8(acc >> 14)
			}
		}
	})
	return out
}

func resizeV(src *image.Gray, dh int, cs []contrib) *image.Gray {
	return resizeVTo(image.NewGray(image.Rect(0, 0, src.Bounds().Dx(), dh)), src, dh, cs)
}

func resizeVTo(out, src *image.Gray, dh int, cs []contrib) *image.Gray {
	sw := src.Bounds().Dx()
	parallelRows(dh, func(y0, y1 int) {
		acc := make([]int32, sw)
		for y := y0; y < y1; y++ {
			c := cs[y]
			for i := range acc {
				acc[i] = 1 << 13
			}
			for k, wv := range c.w {
				srow := src.Pix[(c.start+k)*src.Stride : (c.start+k)*src.Stride+sw]
				for x, v := range srow {
					acc[x] += wv * int32(v)
				}
			}
			drow := out.Pix[y*out.Stride : y*out.Stride+sw]
			for x, a := range acc {
				drow[x] = clamp8(a >> 14)
			}
		}
	})
	return out
}

// Resize scales src to dw x dh with a Catmull-Rom filter (support widened when
// shrinking, so screentones are averaged instead of aliased into moire).
func Resize(src *image.Gray, dw, dh int) *image.Gray { return ResizeK(src, dw, dh, CatmullRom) }

// ResizeK is Resize with an explicit kernel.
func ResizeK(src *image.Gray, dw, dh int, k Kernel) *image.Gray {
	if src.Bounds().Min != (image.Point{}) {
		src = SubImage(src, src.Bounds())
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw == dw && sh == dh {
		out := image.NewGray(image.Rect(0, 0, dw, dh))
		for y := 0; y < dh; y++ {
			copy(out.Pix[y*out.Stride:y*out.Stride+dw], src.Pix[y*src.Stride:y*src.Stride+dw])
		}
		return out
	}
	if sw == dw {
		return resizeV(src, dh, makeContribs(sh, dh, k))
	}
	if sh == dh {
		return resizeH(src, dw, makeContribs(sw, dw, k))
	}
	hc := makeContribs(sw, dw, k)
	vc := makeContribs(sh, dh, k)
	taps := func(cs []contrib) float64 { return float64(len(cs[len(cs)/2].w)) }
	hFirst := float64(sh)*float64(dw)*taps(hc) <= float64(dh)*float64(sw)*taps(vc)
	if hFirst {
		tmp := resizeHTo(getScratch(dw, sh), src, dw, hc)
		out := resizeV(tmp, dh, vc)
		putScratch(tmp)
		return out
	}
	tmp := resizeVTo(getScratch(sw, dh), src, dh, vc)
	out := resizeH(tmp, dw, hc)
	putScratch(tmp)
	return out
}

// Gamma applies an output curve to a gray image in place (v' = 255*(v/255)^g).
func Gamma(img *image.Gray, g float64) {
	if g == 1 || g <= 0 {
		return
	}
	var lut [256]uint8
	for i := range lut {
		lut[i] = uint8(math.Round(255 * math.Pow(float64(i)/255, g)))
	}
	for i, v := range img.Pix {
		img.Pix[i] = lut[v]
	}
}

// Kernel selects the resampling filter.
type Kernel int

const (
	// CatmullRom: no ringing worth mentioning, good for screentone (the support
	// is widened when shrinking, so halftones are averaged instead of aliased).
	CatmullRom Kernel = iota
	// Lanczos3: a little sharper, for text pages (PDFs).
	Lanczos3
)

func (k Kernel) radius() float64 {
	switch k {
	case Lanczos3:
		return 3
	case Smooth:
		return 1
	}
	return 2
}

func (k Kernel) weight(x float64) float64 {
	if k == Smooth {
		x = math.Abs(x)
		if x >= 1 {
			return 0
		}
		return 1 - x*x*(3-2*x)
	}
	if k == Lanczos3 {
		x = math.Abs(x)
		if x >= 3 {
			return 0
		}
		if x < 1e-9 {
			return 1
		}
		px := math.Pi * x
		return 3 * math.Sin(px) * math.Sin(px/3) / (px * px)
	}
	return catmullRom(x)
}

// Smooth is a non-negative kernel (smoothstep falloff over one pixel, widened
// when shrinking): no ringing and no negative lobes, a little softer than
// Catmull-Rom; it is what MuPDF-style scalers fall back to on screentone.
const Smooth Kernel = 2
