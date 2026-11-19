// Package kobo is the display.Display of a Kobo e-reader with an i.MX EPDC
// panel (Libra 2, Clara HD/2E, Forma, Libra H2O ...): the Linux framebuffer,
// refreshed with the MXCFB_SEND_UPDATE ioctl, and the evdev touch screen and
// page-turn keys for input. Nickel (Kobo's own reader) is stopped by the
// launcher while the app runs, so the app owns the screen and the input.
//
// This file is the portable part (pixel conversion, the update request, touch
// parsing and calibration arithmetic), so it is tested on any host; the device
// access is in fb_linux.go.
package kobo

import (
	"encoding/binary"
	"image"
	"math"
	"strings"

	"rmkavita/internal/display"
)

// FBInfo is what the app needs of fb_var_screeninfo and fb_fix_screeninfo.
type FBInfo struct {
	XRes, YRes int // the visible size, in the rotation in effect
	XOff, YOff int // the visible area's offset inside the virtual buffer
	BPP        int // bits per pixel: 8 (grey), 16 (RGB565) or 32
	Stride     int // bytes per line
	Rotate     int // FB_ROTATE_* in effect
	Grayscale  bool
	// byte positions of the channels of a 32-bit pixel (from the bitfields)
	ROff, GOff, BOff, AOff int
	ALen                   int
}

// PixelFormat names the layout for the log.
func (fi FBInfo) PixelFormat() string {
	switch fi.BPP {
	case 8:
		return "kobo-gray8"
	case 16:
		return "kobo-rgb565"
	}
	return "kobo-32bpp"
}

// base is the byte offset of the visible area's first pixel.
func (fi FBInfo) base() int { return fi.YOff*fi.Stride + fi.XOff*fi.BPP/8 }

// gray565 is the luminance of an RGB565 value (the panels are black and
// white: a colour layer is shown in grey).
func gray565(v uint16) uint8 {
	r := int(v>>11) & 31
	g := int(v>>5) & 63
	b := int(v) & 31
	r8, g8, b8 := r<<3|r>>2, g<<2|g>>4, b<<3|b>>2
	return uint8((r8*299 + g8*587 + b8*114 + 500) / 1000)
}

// Convert writes region r of the frame into the framebuffer memory fb laid out
// as fi. A colour layer is converted to grey, except inside the opaque
// rectangles, where the grey canvas (the UI drawn over the page) wins.
func Convert(fb []byte, fi FBInfo, f display.Frame, r image.Rectangle) {
	layer := f.Color != nil && f.Color.W == fi.XRes && len(f.Color.Pix) >= fi.XRes*fi.YRes
	put := func(y int, row []uint8, x0 int) {
		putRow(fb, fi, y, x0, row)
	}
	buf := make([]uint8, r.Dx())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		if layer {
			src := f.Color.Pix[y*f.Color.W+r.Min.X : y*f.Color.W+r.Max.X]
			for i, v := range src {
				buf[i] = gray565(v)
			}
		} else {
			o := f.Gray.PixOffset(r.Min.X, y)
			copy(buf, f.Gray.Pix[o:o+r.Dx()])
		}
		put(y, buf, r.Min.X)
	}
	if layer {
		for _, op := range f.Opaque {
			op = op.Intersect(r)
			for y := op.Min.Y; y < op.Max.Y; y++ {
				o := f.Gray.PixOffset(op.Min.X, y)
				putRow(fb, fi, y, op.Min.X, f.Gray.Pix[o:o+op.Dx()])
			}
		}
	}
}

// putRow writes grey levels at (x0, y) in the framebuffer's own format.
func putRow(fb []byte, fi FBInfo, y, x0 int, row []uint8) {
	off := fi.base() + y*fi.Stride + x0*fi.BPP/8
	switch fi.BPP {
	case 8:
		copy(fb[off:off+len(row)], row)
	case 16:
		for i, g := range row {
			v5 := uint16((int(g)*31 + 127) / 255)
			v6 := v5<<1 | v5>>4
			binary.LittleEndian.PutUint16(fb[off+2*i:], v5<<11|v6<<5|v5)
		}
	default:
		for i, g := range row {
			p := fb[off+4*i : off+4*i+4]
			p[0], p[1], p[2], p[3] = g, g, g, g
			if fi.ALen > 0 && fi.AOff >= 0 && fi.AOff < 4 {
				p[fi.AOff] = 0xFF
			}
		}
	}
}

// ---- the panel update request ------------------------------------------------------

// Waveform modes and update flags of the i.MX EPDC driver. Only the values
// shared by every Kobo generation are used (DU, GC16 and AUTO keep their
// numbers on the Mark 7 driver, where A2 and GL16 moved).
const (
	wfDU         = 1
	wfGC16       = 2
	wfAuto       = 257
	updPartial   = 0
	updFull      = 1
	tempAmbient  = 0x1000
	ioSendV2     = 0x4048462E // MXCFB_SEND_UPDATE with mxcfb_update_data_v2 (Mark 6 / Mark 7)
	ioSendV1NTX  = 0x4044462E // MXCFB_SEND_UPDATE with mxcfb_update_data_v1_ntx (older Kobos)
	ioWaitV3     = 0xC008462F // MXCFB_WAIT_FOR_UPDATE_COMPLETE (mxcfb_update_marker_data)
	sizeUpdateV2 = 72
	sizeUpdateV1 = 68
)

// waveFor maps the app's refresh request to a waveform and an update mode: a
// full refresh is GC16 with the flash; CONTENT is GC16 without it; FAST and
// ANIMATE are DU (black and white, the quickest); everything else lets the
// driver pick (AUTO: DU for pure black and white areas, GC16 otherwise).
func waveFor(m display.Mode, full bool) (wf, mode uint32) {
	if full {
		return wfGC16, updFull
	}
	switch m {
	case display.ModeContent:
		return wfGC16, updPartial
	case display.ModeFast, display.ModeAnimate:
		return wfDU, updPartial
	}
	return wfAuto, updPartial
}

// encodeUpdate builds the update request for r. v2 is the Mark 6/7 layout
// (dither_mode and quant_bit after flags); the v1 NTX layout has the
// alternate-buffer data right after flags. The alternate buffer is unused.
func encodeUpdate(r image.Rectangle, wf, mode, marker uint32, v2 bool) []byte {
	n := sizeUpdateV1
	if v2 {
		n = sizeUpdateV2
	}
	b := make([]byte, n)
	le := binary.LittleEndian
	le.PutUint32(b[0:], uint32(r.Min.Y))  // top
	le.PutUint32(b[4:], uint32(r.Min.X))  // left
	le.PutUint32(b[8:], uint32(r.Dx()))   // width
	le.PutUint32(b[12:], uint32(r.Dy()))  // height
	le.PutUint32(b[16:], wf)              // waveform_mode
	le.PutUint32(b[20:], mode)            // update_mode
	le.PutUint32(b[24:], marker)          // update_marker
	le.PutUint32(b[28:], tempAmbient)     // temp
	le.PutUint32(b[32:], 0)               // flags
	// v2: dither_mode, quant_bit (0, 0) and the alternate buffer stay zero
	return b
}

// ---- input ---------------------------------------------------------------------------

// Linux input event types and codes used here.
const (
	evSyn = 0
	evKey = 1
	evAbs = 3

	synReport = 0

	absX          = 0x00
	absY          = 0x01
	absMTSlot     = 0x2f
	absMTPosX     = 0x35
	absMTPosY     = 0x36
	absMTTracking = 0x39

	btnTouch = 0x14a

	keyPageUp   = 104
	keyPageDown = 109
	keyPower    = 116
	keyF23      = 193 // Kobo Forma / Libra / Libra 2: page back
	keyF24      = 194 // page forward
)

// ButtonFor names the app button of a key code ("" = not one of ours).
func ButtonFor(code uint16) string {
	switch code {
	case keyF23, keyPageUp:
		return display.BtnPrev
	case keyF24, keyPageDown:
		return display.BtnNext
	case keyPower:
		return display.BtnPower
	}
	return ""
}

// RawEvent is one evdev record.
type RawEvent struct {
	Type, Code uint16
	Value      int32
}

// Contact is one finger, in raw touch-panel coordinates.
type Contact struct {
	Kind display.EventKind
	ID   int
	X, Y int
}

type mtSlot struct {
	id                    int
	x, y                  int
	down                  bool
	started, ended, moved bool
}

// TouchParser turns multi-touch protocol B records (with the single-touch
// ABS_X / ABS_Y / BTN_TOUCH protocol as a fallback) into finger contacts,
// emitted at each SYN_REPORT.
type TouchParser struct {
	slot  int
	slots [10]mtSlot
	sawMT bool
}

// Feed consumes one record and calls emit for each finished contact change.
func (p *TouchParser) Feed(ev RawEvent, emit func(Contact)) {
	s := &p.slots[p.slot]
	switch ev.Type {
	case evAbs:
		switch ev.Code {
		case absMTSlot:
			p.sawMT = true
			if ev.Value >= 0 && int(ev.Value) < len(p.slots) {
				p.slot = int(ev.Value)
			}
		case absMTTracking:
			p.sawMT = true
			if ev.Value < 0 {
				s.ended = true
			} else {
				s.id, s.started, s.ended = int(ev.Value), true, false
			}
		case absMTPosX:
			p.sawMT = true
			s.x, s.moved = int(ev.Value), true
		case absMTPosY:
			p.sawMT = true
			s.y, s.moved = int(ev.Value), true
		case absX:
			if !p.sawMT {
				p.slots[0].x, p.slots[0].moved = int(ev.Value), true
			}
		case absY:
			if !p.sawMT {
				p.slots[0].y, p.slots[0].moved = int(ev.Value), true
			}
		}
	case evKey:
		if ev.Code == btnTouch {
			if ev.Value == 0 {
				// some panels end a contact only with BTN_TOUCH: every open slot ends
				for i := range p.slots {
					if p.slots[i].down {
						p.slots[i].ended = true
					}
				}
			} else if !p.sawMT && !p.slots[0].down {
				p.slots[0].started = true
			}
		}
	case evSyn:
		if ev.Code != synReport {
			return
		}
		for i := range p.slots {
			s := &p.slots[i]
			id := i // the slot number is the finger id the app sees
			switch {
			case s.started && !s.down:
				s.down = true
				emit(Contact{Kind: display.Down, ID: id, X: s.x, Y: s.y})
				if s.ended { // pressed and lifted within one report
					s.down = false
					emit(Contact{Kind: display.Up, ID: id, X: s.x, Y: s.y})
				}
			case s.ended && s.down:
				s.down = false
				emit(Contact{Kind: display.Up, ID: id, X: s.x, Y: s.y})
			case s.moved && s.down:
				emit(Contact{Kind: display.Move, ID: id, X: s.x, Y: s.y})
			}
			s.started, s.ended, s.moved = false, false, false
		}
	}
}

// AbsRange is a raw axis range (from EVIOCGABS).
type AbsRange struct{ Min, Max int }

// TouchMap maps raw touch coordinates to the screen: each raw axis is first
// normalised to 0..1 over its range, then swapped and mirrored as the
// calibration found.
type TouchMap struct {
	Swap, MirX, MirY bool
	RX, RY           AbsRange // raw ranges of the X and Y axes
	W, H             int      // screen size
}

// ParseMap reads a touch_map value ("s", "x", "y" in any combination, "-" =
// none).
func ParseMap(s string) (swap, mx, my bool) {
	return strings.Contains(s, "s"), strings.Contains(s, "x"), strings.Contains(s, "y")
}

// FormatMap is the touch_map value of a transform.
func FormatMap(swap, mx, my bool) string {
	out := ""
	if swap {
		out += "s"
	}
	if mx {
		out += "x"
	}
	if my {
		out += "y"
	}
	if out == "" {
		out = "-"
	}
	return out
}

func norm(v int, r AbsRange) float64 {
	if r.Max <= r.Min {
		return 0
	}
	f := float64(v-r.Min) / float64(r.Max-r.Min)
	return math.Max(0, math.Min(1, f))
}

// Normalized is the raw point on 0..1 axes, before swapping or mirroring.
func (m TouchMap) Normalized(x, y int) (float64, float64) { return norm(x, m.RX), norm(y, m.RY) }

// Apply maps a raw point to screen pixels.
func (m TouchMap) Apply(x, y int) (int, int) {
	nx, ny := m.Normalized(x, y)
	if m.Swap {
		nx, ny = ny, nx
	}
	if m.MirX {
		nx = 1 - nx
	}
	if m.MirY {
		ny = 1 - ny
	}
	return int(nx*float64(m.W-1) + 0.5), int(ny*float64(m.H-1) + 0.5)
}

// GuessMap is the transform used until a calibration succeeds: a panel whose
// long axis is not the screen's long axis is turned a quarter; Kobo panels
// turned that way are mirrored on the screen's x axis (Nickel's portrait).
func GuessMap(rx, ry AbsRange, w, h int) string {
	if (rx.Max-rx.Min > ry.Max-ry.Min) != (w > h) {
		return "sx"
	}
	return "-"
}

// Calibration targets, as fractions of the screen: far apart on both axes, by
// clearly different amounts, so one pair of taps tells a swap from a mirror.
var (
	CalA = [2]float64{0.18, 0.18}
	CalB = [2]float64{0.82, 0.42}
)

// Calibrate derives the transform from the normalised raw points of taps on
// CalA and CalB. ok is false when the taps do not fit any transform (a missed
// target), and the calibration should be repeated.
func Calibrate(a, b [2]float64) (swap, mx, my bool, ok bool) {
	wantX, wantY := CalB[0]-CalA[0], CalB[1]-CalA[1] // 0.64, 0.24
	gx, gy := b[0]-a[0], b[1]-a[1]
	// the raw axis that moved most is the screen's x axis (it moved most)
	if math.Abs(gy) > math.Abs(gx) {
		swap = true
		gx, gy = gy, gx
	}
	if math.Abs(gx) < wantX*0.6 || math.Abs(gx) > wantX*1.4 || math.Abs(gy) < wantY*0.5 || math.Abs(gy) > wantY*1.6 {
		return false, false, false, false
	}
	mx = gx < 0
	my = gy < 0
	return swap, mx, my, true
}
