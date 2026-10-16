package qtfb

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"testing"

	"rmkavita/internal/display"
)

func newRGBAPanel(t *testing.T) (*Panel, *fakeConn, []byte) {
	t.Helper()
	m := Model{Name: "test", W: 64, H: 48, FBType: 3, RGBAType: 2}
	c := newFakeConn()
	shm := make([]byte, m.W*m.H*4)
	p, err := NewPanel(m, FmtRGBA8888, c, shm, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, c, shm
}

func TestRGBAStartsWhiteAndGrayIsNeutralOpaque(t *testing.T) {
	p, c, shm := newRGBAPanel(t)
	for i := range shm {
		if shm[i] != 0xFF {
			t.Fatalf("byte %d = %#x before the first frame, want 0xFF", i, shm[i])
		}
	}
	g := image.NewGray(image.Rect(0, 0, 64, 48))
	for i := range g.Pix {
		g.Pix[i] = uint8(i % 256)
	}
	if err := p.Show(display.Frame{Gray: g, Rect: g.Bounds(), Mode: display.ModeFast}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64*48; i++ {
		v := g.Pix[i]
		px := shm[4*i : 4*i+4]
		if px[0] != v || px[1] != v || px[2] != v || px[3] != 0xFF {
			t.Fatalf("pixel %d: gray %d -> % x, want %d %d %d 255", i, v, px, v, v, v)
		}
	}
	got := c.types()
	if string(got) != string([]byte{MsgSetRefreshMode, MsgUpdate}) {
		t.Errorf("messages = %v", got)
	}
}

func TestRGBALayerNeutralStaysNeutralAndColourSurvives(t *testing.T) {
	p, _, shm := newRGBAPanel(t)
	g := image.NewGray(image.Rect(0, 0, 64, 48))
	for i := range g.Pix {
		g.Pix[i] = 255
	}
	layer := &display.Layer{Pix: make([]uint16, 64*48), W: 64, H: 48}
	lut := Gray565LUT()
	// pixel 0: a neutral gray of the page cache; pixel 1: pure red; pixel 2: white
	layer.Pix[0] = lut[100]
	layer.Pix[1] = 31 << 11
	layer.Pix[2] = 0xFFFF
	if err := p.Show(display.Frame{Gray: g, Rect: g.Bounds(), Mode: display.ModeUI, Color: layer}); err != nil {
		t.Fatal(err)
	}
	if shm[0] != shm[1] || shm[1] != shm[2] || shm[3] != 0xFF {
		t.Errorf("neutral gray of a colour page tinted: % x", shm[0:4])
	}
	if shm[4] != 255 || shm[5] != 0 || shm[6] != 0 || shm[7] != 0xFF {
		t.Errorf("red = % x", shm[4:8])
	}
	if shm[8] != 255 || shm[9] != 255 || shm[10] != 255 {
		t.Errorf("white = % x", shm[8:12])
	}
	// the opaque rectangle (chrome over the page) wins over the layer
	g.Pix[0] = 0
	if err := p.Show(display.Frame{Gray: g, Rect: g.Bounds(), Mode: display.ModeUI, Color: layer, Opaque: []image.Rectangle{image.Rect(0, 0, 1, 1)}}); err != nil {
		t.Fatal(err)
	}
	if shm[0] != 0 || shm[1] != 0 || shm[2] != 0 {
		t.Errorf("opaque pixel = % x", shm[0:4])
	}
}

// Every gray level the colour page cache produces must expand to R == G == B
// (the point of the RGBA path); there are 32 of them.
func TestExpand565NeutralLevels(t *testing.T) {
	lut := Gray565LUT()
	seen := map[uint32]bool{}
	for g := 0; g < 256; g++ {
		e := expand565(lut[g])
		r, gg, b, a := e&0xFF, e>>8&0xFF, e>>16&0xFF, e>>24
		if r != gg || gg != b || a != 0xFF {
			t.Fatalf("gray %d expands to %d %d %d %d", g, r, gg, b, a)
		}
		seen[e] = true
	}
	if len(seen) != 32 {
		t.Errorf("%d distinct levels, want 32", len(seen))
	}
}

func TestArmSendsOnlyOnChangeAndKeepNeverSends(t *testing.T) {
	p, c, _ := newPanel(t)
	g := image.NewGray(image.Rect(0, 0, 64, 48))
	r := image.Rect(0, 0, 8, 8)
	steps := []struct {
		mode display.Mode
		want []byte
	}{
		{display.ModeKeep, []byte{MsgUpdate}},                       // nothing armed yet: no SET_MODE for Keep
		{display.ModeFast, []byte{MsgSetRefreshMode, MsgUpdate}},    // first real mode
		{display.ModeFast, []byte{MsgUpdate}},                       // unchanged
		{display.ModeKeep, []byte{MsgUpdate}},                       // keep
		{display.ModeContent, []byte{MsgSetRefreshMode, MsgUpdate}}, // flip
	}
	for i, s := range steps {
		before := len(c.types())
		if err := p.Show(display.Frame{Gray: g, Rect: r, Mode: s.mode}); err != nil {
			t.Fatal(err)
		}
		got := c.types()[before:]
		if string(got) != string(s.want) {
			t.Errorf("step %d (%v): %v, want %v", i, s.mode, got, s.want)
		}
	}
	// Arm: no-op when equal, one message when different, never for Keep
	before := len(c.types())
	p.Arm(display.ModeContent)
	p.Arm(display.ModeKeep)
	if len(c.types()) != before {
		t.Errorf("Arm sent %d messages for an armed mode", len(c.types())-before)
	}
	p.Arm(display.ModeFast)
	got := c.types()[before:]
	if string(got) != string([]byte{MsgSetRefreshMode}) {
		t.Errorf("Arm(FAST) = %v", got)
	}
	if p.Counters().ModeChanges != 3 {
		t.Errorf("mode changes = %d, want 3", p.Counters().ModeChanges)
	}
}

// ---- handshake and negotiation ----------------------------------------------------

type scriptedConn struct {
	writes  [][]byte
	reply   []byte // nil = close without a reply
	closed  bool
	readErr error
}

func (s *scriptedConn) Write(b []byte) (int, error) {
	s.writes = append(s.writes, append([]byte(nil), b...))
	return len(b), nil
}
func (s *scriptedConn) Read(b []byte) (int, error) {
	if s.readErr != nil {
		return 0, s.readErr
	}
	if s.reply == nil {
		return 0, io.EOF
	}
	return copy(b, s.reply), nil
}
func (s *scriptedConn) Close() error { s.closed = true; return nil }

func okMapper(sizes *[]uint64) ShmMapper {
	return func(key int32, size uint64) ([]byte, func(), error) {
		*sizes = append(*sizes, size)
		return make([]byte, size), nil, nil
	}
}

var ppModel = Model{Name: "Paper Pro", W: 1620, H: 2160, FBType: 3, RGBAType: 2, DPI: 229}

func TestNegotiateRGBAFirst(t *testing.T) {
	var conns []*scriptedConn
	connect := func() (PacketConn, error) {
		c := &scriptedConn{reply: EncodeInitResponse(InitResponse{ShmKey: 7, ShmSize: 1620 * 2160 * 4})}
		conns = append(conns, c)
		return c, nil
	}
	var sizes []uint64
	p, rej, err := Negotiate(FormatOrder("auto"), connect, 42, ppModel, okMapper(&sizes))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Format() != FmtRGBA8888 || p.PixelFormat() != "rgba8888" || len(rej) != 0 || len(conns) != 1 {
		t.Fatalf("format %v rejected %v conns %d", p.Format(), rej, len(conns))
	}
	init := conns[0].writes[0]
	if init[0] != MsgInitialize || init[8] != 2 || binary.LittleEndian.Uint32(init[4:]) != 42 {
		t.Errorf("INITIALIZE = % x, want type 2", init)
	}
	if len(sizes) != 1 || sizes[0] != 13996800 {
		t.Errorf("shm %v, want 13,996,800", sizes)
	}
}

func TestNegotiateFallsBackWhenTheServerClosesWithoutAReply(t *testing.T) {
	n := 0
	var conns []*scriptedConn
	connect := func() (PacketConn, error) {
		n++
		c := &scriptedConn{}
		if n == 2 {
			c.reply = EncodeInitResponse(InitResponse{ShmKey: 9, ShmSize: 1620 * 2160 * 2})
		}
		conns = append(conns, c)
		return c, nil
	}
	var sizes []uint64
	p, rej, err := Negotiate(FormatOrder("auto"), connect, 42, ppModel, okMapper(&sizes))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Format() != FmtRGB565 || len(rej) != 1 || !errors.Is(rej[0], ErrRejected) {
		t.Fatalf("format %v rejections %v", p.Format(), rej)
	}
	if conns[0].writes[0][8] != 2 || conns[1].writes[0][8] != 3 || !conns[0].closed {
		t.Errorf("INITIALIZE types %d then %d (first closed %v)", conns[0].writes[0][8], conns[1].writes[0][8], conns[0].closed)
	}
}

func TestNegotiateFallsBackOnASmallFramebuffer(t *testing.T) {
	n := 0
	connect := func() (PacketConn, error) {
		n++
		size := uint64(1620 * 2160 * 2) // a server that accepts the type but only has 565 memory
		return &scriptedConn{reply: EncodeInitResponse(InitResponse{ShmKey: int32(n), ShmSize: size})}, nil
	}
	var sizes []uint64
	p, rej, err := Negotiate(FormatOrder("auto"), connect, 1, ppModel, okMapper(&sizes))
	if err != nil || p.Format() != FmtRGB565 || len(rej) != 1 {
		t.Fatalf("%v %v %v", p, rej, err)
	}
	p.Close()
}

func TestNegotiateForcedFormatsAndHardErrors(t *testing.T) {
	// forced rgba: a rejection is an error, no second attempt
	attempts := 0
	connect := func() (PacketConn, error) { attempts++; return &scriptedConn{}, nil }
	var sizes []uint64
	if _, _, err := Negotiate(FormatOrder("rgba"), connect, 1, ppModel, okMapper(&sizes)); err == nil || attempts != 1 {
		t.Errorf("forced rgba: err %v attempts %d", err, attempts)
	}
	// forced rgb565 asks for type 3 only
	var first *scriptedConn
	connect = func() (PacketConn, error) {
		first = &scriptedConn{reply: EncodeInitResponse(InitResponse{ShmKey: 1, ShmSize: 1620 * 2160 * 2})}
		return first, nil
	}
	p, _, err := Negotiate(FormatOrder("rgb565"), connect, 1, ppModel, okMapper(&sizes))
	if err != nil || first.writes[0][8] != 3 || p.Format() != FmtRGB565 {
		t.Fatalf("forced 565: %v", err)
	}
	p.Close()
	// a timeout is not a rejection: no fallback
	attempts = 0
	connect = func() (PacketConn, error) {
		attempts++
		return &scriptedConn{readErr: os.ErrDeadlineExceeded}, nil
	}
	if _, rej, err := Negotiate(FormatOrder("auto"), connect, 1, ppModel, okMapper(&sizes)); err == nil || attempts != 1 || len(rej) != 0 {
		t.Errorf("timeout: err %v attempts %d rej %v", err, attempts, rej)
	}
	// the socket missing is not a rejection either
	connect = func() (PacketConn, error) { return nil, errors.New("connect: no such file") }
	if _, _, err := Negotiate(FormatOrder("auto"), connect, 1, ppModel, okMapper(&sizes)); err == nil {
		t.Error("connect error swallowed")
	}
}

func TestFormatOrder(t *testing.T) {
	for in, want := range map[string][]PixFmt{
		"": {FmtRGBA8888, FmtRGB565}, "auto": {FmtRGBA8888, FmtRGB565}, "bogus": {FmtRGBA8888, FmtRGB565},
		"rgba": {FmtRGBA8888}, "rgb565": {FmtRGB565},
	} {
		got := FormatOrder(in)
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("FormatOrder(%q) = %v", in, got)
		}
	}
	if m, _ := ModelFromMachine("reMarkable Ferrari"); m.TypeFor(FmtRGBA8888) != 2 || m.TypeFor(FmtRGB565) != 3 {
		t.Errorf("Paper Pro types %d %d", m.TypeFor(FmtRGBA8888), m.TypeFor(FmtRGB565))
	}
}

// With RMK_RENDER_DIR set, the RGBA framebuffer is written out as a PNG so the
// path can be looked at on a PC: a 16-step grey ramp, the same ramp through
// the colour layer. Every pixel must be neutral (R = G = B) and opaque.
func TestRenderRGBAFramebuffer(t *testing.T) {
	m := Model{Name: "t", W: 320, H: 120, FBType: 3, RGBAType: 2}
	c := newFakeConn()
	shm := make([]byte, m.W*m.H*4)
	p, err := NewPanel(m, FmtRGBA8888, c, shm, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := image.NewGray(image.Rect(0, 0, m.W, m.H))
	for x := 0; x < m.W; x++ {
		v := uint8((x * 16 / m.W) * 17)
		for y := 0; y < m.H/2; y++ {
			g.Pix[y*g.Stride+x] = v
		}
		for y := m.H / 2; y < m.H; y++ {
			g.Pix[y*g.Stride+x] = 255
		}
	}
	lut := Gray565LUT()
	layer := &display.Layer{Pix: make([]uint16, m.W*m.H), W: m.W, H: m.H}
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			layer.Pix[y*m.W+x] = lut[uint8((x*16/m.W)*17)]
		}
	}
	if err := p.Show(display.Frame{Gray: g, Rect: g.Bounds(), Mode: display.ModeUI, Color: layer, Opaque: []image.Rectangle{image.Rect(0, 0, m.W, m.H/2)}}); err != nil {
		t.Fatal(err)
	}
	out := image.NewRGBA(image.Rect(0, 0, m.W, m.H))
	for i := 0; i < m.W*m.H; i++ {
		r, gg, b, a := shm[4*i], shm[4*i+1], shm[4*i+2], shm[4*i+3]
		if r != gg || gg != b || a != 255 {
			t.Fatalf("pixel %d is % x: not neutral/opaque", i, shm[4*i:4*i+4])
		}
		out.SetRGBA(i%m.W, i/m.W, color.RGBA{r, gg, b, a})
	}
	if dir := os.Getenv("RMK_RENDER_DIR"); dir != "" {
		f, err := os.Create(dir + "/rgba-ramp.png")
		if err == nil {
			png.Encode(f, out)
			f.Close()
		}
	}
}
