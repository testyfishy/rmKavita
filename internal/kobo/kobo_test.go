package kobo

import (
	"encoding/binary"
	"image"
	"testing"

	"rmkavita/internal/display"
)

func TestEncodeUpdateLayout(t *testing.T) {
	b := encodeUpdate(image.Rect(10, 20, 110, 70), wfGC16, updFull, 7, true)
	if len(b) != 72 {
		t.Fatalf("v2 size %d, want 72", len(b))
	}
	le := binary.LittleEndian
	want := []uint32{20, 10, 100, 50, wfGC16, updFull, 7, tempAmbient, 0}
	for i, w := range want {
		if g := le.Uint32(b[4*i:]); g != w {
			t.Errorf("field %d = %d, want %d", i, g, w)
		}
	}
	if n := len(encodeUpdate(image.Rect(0, 0, 1, 1), wfAuto, updPartial, 1, false)); n != 68 {
		t.Fatalf("v1 size %d, want 68", n)
	}
	// the ioctl numbers carry the struct sizes
	if (ioSendV2>>16)&0x3fff != 72 || (ioSendV1NTX>>16)&0x3fff != 68 {
		t.Fatal("ioctl sizes do not match the layouts")
	}
}

func TestWaveFor(t *testing.T) {
	cases := []struct {
		m    display.Mode
		full bool
		wf   uint32
		um   uint32
	}{
		{display.ModeUI, false, wfAuto, updPartial},
		{display.ModeContent, false, wfGC16, updPartial},
		{display.ModeFast, false, wfDU, updPartial},
		{display.ModeUI, true, wfGC16, updFull},
	}
	for _, c := range cases {
		if wf, um := waveFor(c.m, c.full); wf != c.wf || um != c.um {
			t.Errorf("%v full=%v: %d/%d, want %d/%d", c.m, c.full, wf, um, c.wf, c.um)
		}
	}
}

func TestConvertFormats(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 4, 2))
	for i := range g.Pix {
		g.Pix[i] = uint8(i * 30)
	}
	f := display.Frame{Gray: g, Rect: g.Bounds()}
	// 8 bpp with a stride wider than the screen and a y offset (double buffer)
	fi := FBInfo{XRes: 4, YRes: 2, BPP: 8, Stride: 8, YOff: 2}
	fb := make([]byte, 8*4)
	Convert(fb, fi, f, image.Rect(1, 0, 3, 2))
	if fb[2*8+1] != 30 || fb[2*8+2] != 60 || fb[3*8+1] != 150 || fb[2*8+0] != 0 || fb[2*8+3] != 0 {
		t.Fatalf("8bpp: %v", fb)
	}
	// 32 bpp: grey in every channel, alpha opaque
	fi = FBInfo{XRes: 4, YRes: 2, BPP: 32, Stride: 16, AOff: 3, ALen: 8}
	fb = make([]byte, 32)
	Convert(fb, fi, f, g.Bounds())
	if p := fb[4*5 : 4*5+4]; p[0] != 150 || p[1] != 150 || p[2] != 150 || p[3] != 255 {
		t.Fatalf("32bpp pixel 5: %v", p)
	}
	// 16 bpp: a neutral 565 grey
	fi = FBInfo{XRes: 4, YRes: 2, BPP: 16, Stride: 8}
	fb = make([]byte, 16)
	Convert(fb, fi, f, g.Bounds())
	if v := binary.LittleEndian.Uint16(fb[14:]); v>>11 != v&31 {
		t.Fatalf("565 grey is tinted: %04x", v)
	}
}

func TestConvertColourLayerToGrey(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 2, 1))
	g.Pix[0], g.Pix[1] = 9, 9
	l := &display.Layer{Pix: []uint16{0xFFFF, 0x0000}, W: 2, H: 1}
	f := display.Frame{Gray: g, Rect: g.Bounds(), Color: l, Opaque: []image.Rectangle{image.Rect(1, 0, 2, 1)}}
	fb := make([]byte, 2)
	Convert(fb, FBInfo{XRes: 2, YRes: 1, BPP: 8, Stride: 2}, f, g.Bounds())
	if fb[0] != 255 || fb[1] != 9 {
		t.Fatalf("got %v, want [255 9] (layer white, the opaque UI pixel from the canvas)", fb)
	}
}

func feedAll(p *TouchParser, evs []RawEvent) []Contact {
	var out []Contact
	for _, e := range evs {
		p.Feed(e, func(c Contact) { out = append(out, c) })
	}
	return out
}

func syn() RawEvent { return RawEvent{Type: evSyn, Code: synReport} }

func TestTouchProtocolB(t *testing.T) {
	var p TouchParser
	got := feedAll(&p, []RawEvent{
		{evAbs, absMTSlot, 0}, {evAbs, absMTTracking, 41}, {evAbs, absMTPosX, 100}, {evAbs, absMTPosY, 200}, syn(),
		{evAbs, absMTPosX, 110}, syn(),
		{evAbs, absMTSlot, 1}, {evAbs, absMTTracking, 42}, {evAbs, absMTPosX, 500}, {evAbs, absMTPosY, 600}, syn(),
		{evAbs, absMTSlot, 0}, {evAbs, absMTTracking, -1}, syn(),
		{evAbs, absMTSlot, 1}, {evAbs, absMTTracking, -1}, syn(),
	})
	want := []Contact{
		{display.Down, 0, 100, 200}, {display.Move, 0, 110, 200}, {display.Down, 1, 500, 600},
		{display.Up, 0, 110, 200}, {display.Up, 1, 500, 600},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTouchEndedByBtnTouch(t *testing.T) {
	var p TouchParser
	got := feedAll(&p, []RawEvent{
		{evAbs, absMTTracking, 3}, {evAbs, absMTPosX, 10}, {evAbs, absMTPosY, 20}, {evKey, btnTouch, 1}, syn(),
		{evKey, btnTouch, 0}, syn(),
	})
	if len(got) != 2 || got[1].Kind != display.Up {
		t.Fatalf("got %v", got)
	}
}

func TestTouchSingleTouchFallback(t *testing.T) {
	var p TouchParser
	got := feedAll(&p, []RawEvent{
		{evAbs, absX, 5}, {evAbs, absY, 6}, {evKey, btnTouch, 1}, syn(),
		{evAbs, absX, 7}, syn(),
		{evKey, btnTouch, 0}, syn(),
	})
	if len(got) != 3 || got[0].Kind != display.Down || got[1].Kind != display.Move || got[2].Kind != display.Up || got[2].X != 7 {
		t.Fatalf("got %v", got)
	}
}

func TestTouchMapAndCalibration(t *testing.T) {
	W, H := 1264, 1680
	rx, ry := AbsRange{0, 1679}, AbsRange{0, 1263} // a panel turned a quarter
	for _, c := range []struct{ swap, mx, my bool }{
		{false, false, false}, {true, false, false}, {true, true, false}, {true, false, true}, {false, true, true},
	} {
		dev := TouchMap{Swap: c.swap, MirX: c.mx, MirY: c.my, RX: rx, RY: ry, W: W, H: H}
		// where would the panel report a tap on screen point (fx, fy)? invert the map
		rawFor := func(fx, fy float64) (int, int) {
			nx, ny := fx, fy
			if c.my {
				ny = 1 - ny
			}
			if c.mx {
				nx = 1 - nx
			}
			if c.swap {
				nx, ny = ny, nx
			}
			return int(nx * float64(rx.Max)), int(ny * float64(ry.Max))
		}
		ax, ay := rawFor(CalA[0], CalA[1])
		bx, by := rawFor(CalB[0], CalB[1])
		na := [2]float64{}
		na[0], na[1] = dev.Normalized(ax, ay)
		nb := [2]float64{}
		nb[0], nb[1] = dev.Normalized(bx, by)
		swap, mx, my, ok := Calibrate(na, nb)
		if !ok || swap != c.swap || mx != c.mx || my != c.my {
			t.Errorf("%+v: calibrated %v %v %v ok=%v", c, swap, mx, my, ok)
			continue
		}
		x, y := dev.Apply(rawFor(0.5, 0.25))
		if x < W/2-2 || x > W/2+2 || y < H/4-2 || y > H/4+2 {
			t.Errorf("%+v: (0.5, 0.25) mapped to %d,%d", c, x, y)
		}
		if s := FormatMap(swap, mx, my); func() bool { a, b, c2 := ParseMap(s); return a != swap || b != mx || c2 != my }() {
			t.Errorf("format/parse %q", s)
		}
	}
	// a tap far from the second target is refused
	if _, _, _, ok := Calibrate([2]float64{0.2, 0.2}, [2]float64{0.25, 0.22}); ok {
		t.Error("a missed target was accepted")
	}
}

// The Libra 2's panel reports x 0..1680, y 0..1264 on a 1264x1680 portrait
// screen: a quarter turn.
func TestGuessMap(t *testing.T) {
	if g := GuessMap(AbsRange{0, 1680}, AbsRange{0, 1264}, 1264, 1680); g != "sx" {
		t.Fatalf("Libra 2 guess %q", g)
	}
	if g := GuessMap(AbsRange{0, 1264}, AbsRange{0, 1680}, 1264, 1680); g != "-" {
		t.Fatalf("upright panel guess %q", g)
	}
}

func TestButtonFor(t *testing.T) {
	if ButtonFor(193) != display.BtnPrev || ButtonFor(194) != display.BtnNext || ButtonFor(116) != display.BtnPower || ButtonFor(30) != "" {
		t.Fatal("key mapping")
	}
}
