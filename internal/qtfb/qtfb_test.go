package qtfb

import (
	"errors"
	"image"
	"io"
	"sync"
	"testing"
	"time"

	"rmkavita/internal/display"
)

func TestMessageLayoutMatchesReference(t *testing.T) {
	// offsets verified on the device with luajit ffi.offsetof/sizeof against KOReader's qtfb_h.lua
	b := EncodeInit(245209899, FBFmtRMPPRGB565)
	if len(b) != 24 || b[0] != MsgInitialize || le.Uint32(b[4:]) != 245209899 || b[8] != 3 {
		t.Errorf("init = % x", b)
	}
	u := EncodeUpdate(10, 20, 300, 400)
	if len(u) != 24 || u[0] != MsgUpdate || le.Uint32(u[4:]) != 1 || le.Uint32(u[8:]) != 10 || le.Uint32(u[12:]) != 20 ||
		le.Uint32(u[16:]) != 300 || le.Uint32(u[20:]) != 400 {
		t.Errorf("update = % x", u)
	}
	m := EncodeSetMode(RefreshUI)
	if len(m) != 24 || m[0] != MsgSetRefreshMode || le.Uint32(m[4:]) != 4 {
		t.Errorf("mode = % x", m)
	}
	if f := EncodeFullRefresh(); len(f) != 24 || f[0] != MsgRequestFullRefresh {
		t.Errorf("full = % x", f)
	}
	if f := EncodeTerminate(); len(f) != 24 || f[0] != MsgTerminate {
		t.Errorf("terminate = % x", f)
	}
	r := EncodeInitResponse(InitResponse{ShmKey: 77, ShmSize: 1620 * 2160 * 2})
	if len(r) != 32 {
		t.Fatalf("len %d", len(r))
	}
	got, err := DecodeInitResponse(r)
	if err != nil || got.ShmKey != 77 || got.ShmSize != 1620*2160*2 {
		t.Errorf("init response = %+v %v", got, err)
	}
	in := EncodeInput(Input{Type: InTouchPress, Dev: 2, X: 800, Y: 1000, D: 5})
	dec, err := DecodeInput(in)
	if err != nil || dec.Type != InTouchPress || dec.Dev != 2 || dec.X != 800 || dec.Y != 1000 || dec.D != 5 {
		t.Errorf("input = %+v %v", dec, err)
	}
	if _, err := DecodeInput(in[:20]); err == nil {
		t.Error("short input accepted")
	}
	if _, err := DecodeInput(EncodeInitResponse(InitResponse{})); !errors.Is(err, ErrNotInput) {
		t.Errorf("non-input = %v", err)
	}
}

func TestModelFromMachine(t *testing.T) {
	for in, want := range map[string]Model{
		"reMarkable Ferrari\n": {Name: "Paper Pro", W: 1620, H: 2160, FBType: 3, RGBAType: 2, DPI: 229},
		"reMarkable Chiappa":   {Name: "Paper Pro Move", W: 954, H: 1696, FBType: 6, RGBAType: 5, DPI: 264},
	} {
		m, err := ModelFromMachine(in)
		if err != nil || m != want {
			t.Errorf("%q -> %+v %v", in, m, err)
		}
	}
	if _, err := ModelFromMachine("reMarkable 2.0"); err == nil {
		t.Error("rM2 must be refused")
	}
}

func TestGray565IsNeutralAndMonotonic(t *testing.T) {
	l := Gray565LUT()
	prev := -1
	for g := 0; g < 256; g++ {
		v := l[g]
		r5, g6, b5 := int(v>>11)&31, int(v>>5)&63, int(v)&31
		if r5 != b5 {
			t.Fatalf("g=%d: r5=%d b5=%d", g, r5, b5)
		}
		if g6 != r5<<1|r5>>4 {
			t.Fatalf("g=%d: green %d is not the replication of %d", g, g6, r5)
		}
		if int(v) < prev {
			t.Fatalf("not monotonic at %d", g)
		}
		prev = int(v)
	}
	if l[0] != 0 || l[255] != 0xFFFF {
		t.Errorf("ends: %#x %#x", l[0], l[255])
	}
}

func TestConvert565(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 8, 4))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 8)
	}
	dst := make([]byte, 8*4*2)
	for i := range dst {
		dst[i] = 0xEE
	}
	lut := Gray565LUT()
	Convert565(dst, 16, src, image.Rect(2, 1, 5, 3), &lut)
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			off := y*16 + x*2
			inside := x >= 2 && x < 5 && y >= 1 && y < 3
			v := uint16(dst[off]) | uint16(dst[off+1])<<8
			if inside && v != lut[src.Pix[y*8+x]] {
				t.Errorf("(%d,%d) = %#x", x, y, v)
			}
			if !inside && (dst[off] != 0xEE || dst[off+1] != 0xEE) {
				t.Errorf("(%d,%d) outside the rect was written", x, y)
			}
		}
	}
}

// ---- panel over an in-memory packet connection --------------------------------

type fakeConn struct {
	mu     sync.Mutex
	writes [][]byte
	in     chan []byte
	closed bool
}

func newFakeConn() *fakeConn { return &fakeConn{in: make(chan []byte, 16)} }

func (f *fakeConn) Read(b []byte) (int, error) {
	m, ok := <-f.in
	if !ok {
		return 0, io.EOF
	}
	return copy(b, m), nil
}
func (f *fakeConn) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), b...))
	return len(b), nil
}
func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.in)
	}
	return nil
}
func (f *fakeConn) types() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []byte
	for _, w := range f.writes {
		out = append(out, w[0])
	}
	return out
}

func newPanel(t *testing.T) (*Panel, *fakeConn, []byte) {
	t.Helper()
	m := Model{Name: "test", W: 64, H: 48, FBType: 3, RGBAType: 2}
	c := newFakeConn()
	shm := make([]byte, m.W*m.H*2)
	p, err := NewPanel(m, FmtRGB565, c, shm, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, c, shm
}

func TestPanelShowOrderAndStickyMode(t *testing.T) {
	p, c, shm := newPanel(t)
	g := image.NewGray(image.Rect(0, 0, 64, 48))
	for i := range g.Pix {
		g.Pix[i] = 0
	}
	if err := p.Show(display.Frame{Gray: g, Rect: g.Bounds(), Mode: display.ModeUI}); err != nil {
		t.Fatal(err)
	}
	if shm[0] != 0 || shm[1] != 0 {
		t.Errorf("pixels not converted: % x", shm[:4])
	}
	// same mode again: no second SET_REFRESH_MODE
	p.Show(display.Frame{Gray: g, Rect: image.Rect(0, 0, 10, 10), Mode: display.ModeUI})
	// clearing frame: mode change, update, full refresh request
	p.Show(display.Frame{Gray: g, Rect: g.Bounds(), Mode: display.ModeContent, Full: true})
	want := []byte{MsgSetRefreshMode, MsgUpdate, MsgUpdate, MsgSetRefreshMode, MsgUpdate, MsgRequestFullRefresh}
	got := c.types()
	if string(got) != string(want) {
		t.Errorf("message types = %v, want %v", got, want)
	}
	if v := le.Uint32(c.writes[3][4:]); v != RefreshContent {
		t.Errorf("clear mode = %d", v)
	}
	if v := le.Uint32(c.writes[0][4:]); v != RefreshUI {
		t.Errorf("ui mode = %d", v)
	}
	// never UFAST
	for _, w := range c.writes {
		if w[0] == MsgSetRefreshMode && le.Uint32(w[4:]) == RefreshUFast {
			t.Error("UFAST sent")
		}
	}
}

func TestPanelInputAndDisconnect(t *testing.T) {
	p, c, _ := newPanel(t)
	c.in <- EncodeInput(Input{Type: InTouchPress, Dev: 1, X: 5, Y: 6})
	c.in <- EncodeInput(Input{Type: InTouchUpdate, Dev: 1, X: 7, Y: 8})
	c.in <- EncodeInput(Input{Type: InPenRelease, Dev: 0, X: 9, Y: 10})
	c.in <- EncodeInput(Input{Type: InBtnPress, X: 1})
	c.in <- make([]byte, 5) // runt packet is ignored
	ev := p.Events()
	want := []display.Event{{Kind: display.Down, X: 5, Y: 6, ID: 1}, {Kind: display.Move, X: 7, Y: 8, ID: 1}, {Kind: display.Up, X: 9, Y: 10, ID: 0}}
	for i, w := range want {
		select {
		case e := <-ev:
			if e.Kind != w.Kind || e.X != w.X || e.Y != w.Y || e.ID != w.ID {
				t.Errorf("event %d = %+v", i, e)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %d missing", i)
		}
	}
	// the server going away closes the event channel: this is how the app learns AppLoad's X was pressed
	close(c.in)
	select {
	case _, ok := <-ev:
		if ok {
			t.Error("unexpected event")
		}
	case <-time.After(time.Second):
		t.Fatal("events not closed after disconnect")
	}
}

func TestPanelCloseSendsTerminateOnce(t *testing.T) {
	p, c, _ := newPanel(t)
	p.Close()
	p.Close()
	got := c.types()
	if len(got) != 1 || got[0] != MsgTerminate {
		t.Errorf("writes = %v", got)
	}
	if err := p.Show(display.Frame{Gray: image.NewGray(image.Rect(0, 0, 64, 48)), Rect: image.Rect(0, 0, 4, 4)}); err == nil {
		t.Error("Show after Close must fail, not touch unmapped memory")
	}
}

// A UI that is busy (not reading the channel) must never make the read loop
// block or lose a press or a release: a burst of move samples is coalesced and,
// when the queue is full, moves are shed before presses and releases.
func TestPanelInputQueueNeverBlocksAndKeepsPressesAndReleases(t *testing.T) {
	p, c, _ := newPanel(t)
	feed := func(in Input) {
		select {
		case c.in <- EncodeInput(in):
		case <-time.After(2 * time.Second):
			t.Fatal("the read loop stopped reading: it is blocked on the UI")
		}
	}
	// the consumer reads nothing yet
	for i := 0; i < 40; i++ { // pairs of press / release with a few moves in between
		feed(Input{Type: InTouchPress, Dev: i, X: i, Y: 1})
		for j := 0; j < 20; j++ {
			feed(Input{Type: InTouchUpdate, Dev: i, X: i + j, Y: 2})
		}
		feed(Input{Type: InTouchRelease, Dev: i, X: i, Y: 3})
	}
	// let the loop drain the socket
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ic := p.InputCounters(); ic.Down == 40 && ic.Up == 40 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	ic := p.InputCounters()
	if ic.Down != 40 || ic.Up != 40 || ic.Move != 800 {
		t.Fatalf("counters = %+v", ic)
	}
	if ic.Coalesced == 0 {
		t.Error("move samples were not coalesced")
	}
	downs, ups := 0, 0
	timeout := time.After(2 * time.Second)
	for downs+ups < 80 {
		select {
		case e, ok := <-p.Events():
			if !ok {
				t.Fatalf("closed early: %d downs %d ups", downs, ups)
			}
			switch e.Kind {
			case display.Down:
				downs++
			case display.Up:
				ups++
			}
		case <-timeout:
			t.Fatalf("only %d downs and %d ups arrived", downs, ups)
		}
	}
	if ic := p.InputCounters(); ic.Dropped != 0 {
		t.Errorf("%d events dropped although the queue never filled", ic.Dropped)
	}
}

// When the queue does fill (a stalled UI and a flood of alternating events), the
// oldest move goes first and the counter says so.
func TestPanelInputQueueShedsMovesFirst(t *testing.T) {
	p, c, _ := newPanel(t)
	// alternate IDs so nothing coalesces: 2 x 300 moves, then one press and one release
	for i := 0; i < 300; i++ {
		c.in <- EncodeInput(Input{Type: InTouchUpdate, Dev: 1, X: i, Y: 1})
		c.in <- EncodeInput(Input{Type: InTouchUpdate, Dev: 2, X: i, Y: 2})
	}
	c.in <- EncodeInput(Input{Type: InTouchPress, Dev: 7, X: 1, Y: 1})
	c.in <- EncodeInput(Input{Type: InTouchRelease, Dev: 7, X: 2, Y: 2})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ic := p.InputCounters(); ic.Down == 1 && ic.Up == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	ic := p.InputCounters()
	if ic.Dropped == 0 {
		t.Error("nothing was shed although 600 samples were queued")
	}
	sawDown, sawUp := false, false
	for {
		select {
		case e := <-p.Events():
			if e.Kind == display.Down && e.ID == 7 {
				sawDown = true
			}
			if e.Kind == display.Up && e.ID == 7 {
				sawUp = true
			}
			continue
		case <-time.After(150 * time.Millisecond):
		}
		break
	}
	if !sawDown || !sawUp {
		t.Errorf("press %v / release %v lost in the flood", sawDown, sawUp)
	}
}
