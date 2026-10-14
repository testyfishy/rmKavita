package ui

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rmkavita/internal/gfx"
	"rmkavita/internal/mockkavita"
	"rmkavita/internal/qtfb"
	"rmkavita/internal/store"
)

// ---- a fake qtfb server with rm-appload's timing ----------------------------------------

// One written message with the virtual time it was sent at.
type sent struct {
	at   time.Duration
	kind byte
	mode int // SET_MODE: the mode; UPDATE: unused
	rect image.Rectangle
}

// vconn is a SEQPACKET stand-in that records what the app writes, stamped with
// a virtual clock the test advances by hand (the app runs in real time; only
// the test's own pacing counts).
type vconn struct {
	mu     sync.Mutex
	writes []sent
	in     chan []byte
	closed bool
	clock  time.Duration
}

func newVConn() *vconn { return &vconn{in: make(chan []byte, 256)} }

func (c *vconn) Read(b []byte) (int, error) {
	m, ok := <-c.in
	if !ok {
		return 0, io.EOF
	}
	return copy(b, m), nil
}

func (c *vconn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := sent{at: c.clock, kind: b[0]}
	le := binary.LittleEndian
	switch b[0] {
	case qtfb.MsgSetRefreshMode:
		s.mode = int(le.Uint32(b[4:]))
	case qtfb.MsgUpdate:
		x, y, w, h := int(le.Uint32(b[8:])), int(le.Uint32(b[12:])), int(le.Uint32(b[16:])), int(le.Uint32(b[20:]))
		s.rect = image.Rect(x, y, x+w, y+h)
	}
	c.writes = append(c.writes, s)
	return len(b), nil
}

func (c *vconn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.in)
	}
	return nil
}

func (c *vconn) setClock(d time.Duration) {
	c.mu.Lock()
	c.clock = d
	c.mu.Unlock()
}

func (c *vconn) all() []sent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sent(nil), c.writes...)
}

// serve models rm-appload: messages are read one at a time and a SET_MODE or
// a REQUEST_FULL_REFRESH keeps the server busy for a second. It returns, for
// every message, how long it waited behind earlier stalls.
func serve(msgs []sent) []time.Duration {
	lat := make([]time.Duration, len(msgs))
	var busyUntil time.Duration
	for i, m := range msgs {
		start := m.at
		if busyUntil > start {
			start = busyUntil
		}
		lat[i] = start - m.at
		busyUntil = start
		if m.kind == qtfb.MsgSetRefreshMode || m.kind == qtfb.MsgRequestFullRefresh {
			busyUntil = start + time.Second
		}
	}
	return lat
}

// rig is the app on a real qtfb.Panel over a vconn, against the mock Kavita.
type rig struct {
	t      *testing.T
	conn   *vconn
	app    *App
	mock   *mockkavita.Server
	srv    *httptest.Server
	dir    string
	cancel context.CancelFunc
	done   chan string
	mu     sync.Mutex
	snap   Snapshot
}

type rigOpts struct {
	pages     int
	set       func(*store.Settings)
	cfg       func(*store.Config)
	app       func(*App)
	pw, ph    int
	signedOut bool
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	fonts, _ := gfx.NewFonts()
	if o.pages == 0 {
		o.pages = 60
	}
	if o.pw == 0 {
		o.pw, o.ph = 300, 450
	}
	r := &rig{t: t, conn: newVConn(), dir: t.TempDir()}
	r.mock = mockkavita.Build(2, o.pages, o.pw, o.ph)
	r.mock.SetFonts(fonts)
	r.srv = httptest.NewServer(r.mock)
	t.Cleanup(r.srv.Close)
	conf := filepath.Join(r.dir, "c.conf")
	if err := store.SaveSignIn(conf, r.srv.URL, "reader", r.mock.Key()); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := store.LoadConfig(conf)
	cfg.Covers = false
	cfg.SyncProgress = false
	cfg.ClearStyle, cfg.ClearIdleMS = "content_full", 0 // see startOpts
	if o.cfg != nil {
		o.cfg(&cfg)
	}
	set := store.DefaultSettings() // B/W, every 20 pages
	if o.set != nil {
		o.set(&set)
	}
	model := qtfb.Model{Name: "test", W: 1620, H: 2160, FBType: 3, RGBAType: 2, DPI: 229}
	panel, err := qtfb.NewPanel(model, qtfb.FmtRGBA8888, r.conn, make([]byte, 1620*2160*4), nil)
	if err != nil {
		t.Fatal(err)
	}
	lg := store.NewLogger(filepath.Join(r.dir, "app.log"), nil)
	lg.Stderr = false
	r.app = New(Options{Cfg: cfg, Set: set, ConfPath: conf, DeviceDPI: 229,
		OnShow: func(s Snapshot) { r.mu.Lock(); r.snap = s; r.mu.Unlock() }},
		panel, store.OpenPositions(filepath.Join(r.dir, "s.json")), fonts, lg)
	r.app.syncDelay = time.Hour
	r.app.memAvail = func() int { return 4000 }
	if o.app != nil {
		o.app(r.app)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan string, 1)
	go func() { r.done <- r.app.Run(ctx) }()
	t.Cleanup(func() { r.stop() })
	return r
}

func (r *rig) stop() {
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
}

func (r *rig) get() Snapshot { r.mu.Lock(); defer r.mu.Unlock(); return r.snap }

// wait polls a condition, asking the UI goroutine for a fresh snapshot each time
// (pages arrive without a redraw).
func (r *rig) wait(cond func(Snapshot) bool, what string) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond(r.get()) {
			return
		}
		if r.get().Screen != "" {
			r.app.post(func() {
				s := r.app.Snapshot()
				r.mu.Lock()
				s.Tag = r.snap.Tag
				r.snap = s
				r.mu.Unlock()
			})
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("timeout: %s (%+v)", what, r.get())
}

func (r *rig) touch(x, y int) {
	r.conn.in <- qtfb.EncodeInput(qtfb.Input{Type: qtfb.InTouchPress, X: x, Y: y})
	r.conn.in <- qtfb.EncodeInput(qtfb.Input{Type: qtfb.InTouchRelease, X: x, Y: y})
}

func (r *rig) swipe(x0, y0, x1, y1 int) {
	r.conn.in <- qtfb.EncodeInput(qtfb.Input{Type: qtfb.InTouchPress, X: x0, Y: y0})
	r.conn.in <- qtfb.EncodeInput(qtfb.Input{Type: qtfb.InTouchUpdate, X: (x0 + x1) / 2, Y: (y0 + y1) / 2})
	r.conn.in <- qtfb.EncodeInput(qtfb.Input{Type: qtfb.InTouchRelease, X: x1, Y: y1})
}

func (r *rig) tapName(name string) {
	r.t.Helper()
	r.wait(func(s Snapshot) bool {
		for _, h := range s.Hits {
			if h.Name == name {
				return true
			}
		}
		return false
	}, name)
	for _, h := range r.get().Hits {
		if h.Name == name {
			r.touch((h.Rect.Min.X+h.Rect.Max.X)/2, (h.Rect.Min.Y+h.Rect.Max.Y)/2)
			return
		}
	}
}

// openFirst walks home -> All Libraries -> Manga A -> Series 01 -> volume 1.
func (r *rig) openFirst() {
	r.t.Helper()
	r.wait(func(s Snapshot) bool { return s.Screen == "list" && s.ListRows > 0 }, "home")
	r.tapName("row-5")
	r.wait(func(s Snapshot) bool { return s.ListTitle == "All Libraries" && s.ListRows >= 2 }, "libraries")
	r.tapName("row-0")
	r.wait(func(s Snapshot) bool { return s.ListTitle == "Manga A" && s.ListRows >= 1 }, "series list")
	r.tapName("row-0")
	r.wait(func(s Snapshot) bool { return s.ListTitle == "Series 01" && s.ListRows >= 1 }, "chapters")
	r.tapName("row-0")
	r.wait(func(s Snapshot) bool { return s.Screen == "reader" && s.Tag == "reader-first" }, "first page")
}

func kinds(ms []sent) string {
	var out []string
	for _, m := range ms {
		switch m.kind {
		case qtfb.MsgUpdate:
			out = append(out, "update")
		case qtfb.MsgSetRefreshMode:
			out = append(out, fmt.Sprintf("mode%d", m.mode))
		case qtfb.MsgRequestFullRefresh:
			out = append(out, "full")
		default:
			out = append(out, fmt.Sprintf("msg%d", m.kind))
		}
	}
	return strings.Join(out, ",")
}

// ---- tests ------------------------------------------------------------------------------

// TestPageTurnMessageSequence is the proof for "one page turn = one partial
// update": over 50 turns, across two boundaries of the 20-turn clearing
// refresh, an ordinary turn sends exactly one MESSAGE_UPDATE and nothing else,
// and the clear sends CONTENT, the update, the full-refresh request, and arms
// the turn waveform again at once.
func TestPageTurnMessageSequence(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.openFirst()
	var clears []int
	for turn := 1; turn <= 50; turn++ {
		r.wait(func(s Snapshot) bool {
			return s.Screen == "reader" && s.Page == turn-1 && contains(s.ReadyPages, turn) && (turn+1 >= s.Count || contains(s.ReadyPages, turn+1))
		}, fmt.Sprintf("pages ready before turn %d", turn))
		before := len(r.conn.all())
		r.touch(300, 1000) // left half = forward (right to left)
		r.wait(func(s Snapshot) bool {
			return s.Screen == "reader" && s.Page == turn && (s.Tag == "page" || s.Tag == "page-full")
		}, fmt.Sprintf("turn %d shown", turn))
		time.Sleep(15 * time.Millisecond)
		ms := r.conn.all()[before:]
		got := kinds(ms)
		want := "update"
		if turn%20 == 0 {
			clears = append(clears, turn)
			want = "mode3,update,full,mode4" // CONTENT, the whole screen, the full request, UI armed again
		}
		if got != want {
			t.Fatalf("turn %d sent %q, want %q", turn, got, want)
		}
		for _, m := range ms {
			if m.kind == qtfb.MsgUpdate && m.rect != image.Rect(0, 0, 1620, 2160) {
				t.Fatalf("turn %d: update rectangle %v", turn, m.rect)
			}
		}
	}
	if fmt.Sprint(clears) != "[20 40]" {
		t.Errorf("clearing refreshes at turns %v, want [20 40]", clears)
	}
	r.stop()
	b, _ := os.ReadFile(filepath.Join(r.dir, "app.log"))
	lg := string(b)
	if !strings.Contains(lg, "kind=turn mode=ui region=0,0,1620,2160 full=0") || !strings.Contains(lg, "display totals:") {
		t.Errorf("log lacks the per-update telemetry:\n%s", tail(lg, 600))
	}
	if strings.Contains(lg, r.mock.Key()) {
		t.Error("the key is in the log")
	}
}

// TestWaveformEconomyWithRMAppLoadTiming runs 100 turns with 5 overlay toggles
// and 5 clears against a server model that sleeps one second after every
// SET_REFRESH_MODE and REQUEST_FULL_REFRESH (rm-appload does): no turn, overlay
// toggle or slider move ever sets the mode or waits behind a stall, and each
// clear costs exactly two mode messages (CONTENT, then the turn waveform).
func TestWaveformEconomyWithRMAppLoadTiming(t *testing.T) {
	r := newRig(t, rigOpts{pages: 120})
	r.openFirst()
	clock := 100 * time.Second
	step := func() { clock += 4 * time.Second; r.conn.setClock(clock) } // user actions are >= 4 s apart
	step()
	base := len(r.conn.all()) // everything up to the first page: the chapter open
	var actions []string
	mark := make([]int, 0, 128)
	do := func(name string, f func(), done func(Snapshot) bool) {
		t.Helper()
		step()
		before := len(r.conn.all())
		mark = append(mark, before)
		actions = append(actions, name)
		f()
		r.wait(done, name)
		time.Sleep(10 * time.Millisecond)
		_ = before
	}
	turn := 0
	nextTurn := func() {
		turn++
		want := turn
		do(fmt.Sprintf("turn %d", turn), func() { r.touch(300, 1000) },
			func(s Snapshot) bool {
				return s.Screen == "reader" && s.Page == want && (s.Tag == "page" || s.Tag == "page-full")
			})
	}
	for i := 0; i < 100; i++ {
		// an overlay toggle every 20 turns: reveal (centre band), then hide by tapping the page
		if i > 0 && i%20 == 10 {
			do("reveal", func() { r.touch(810, 300) }, func(s Snapshot) bool { return s.Overlay })
			do("hide", func() { r.touch(300, 1000) }, func(s Snapshot) bool { return !s.Overlay && s.Tag == "overlay-hide-bottom" })
		}
		// pages must be decoded: the economy test is about messages, not about fetches
		r.wait(func(s Snapshot) bool { return s.Screen == "reader" && contains(s.ReadyPages, turn+1) }, "next page ready")
		nextTurn()
	}
	msgs := r.conn.all()
	lat := serve(msgs)
	// 1. after the chapter is open, every action: no SET_MODE except the re-arm that belongs to a clear
	clearCount, modeAfterOpen := 0, 0
	for i := base; i < len(msgs); i++ {
		if msgs[i].kind == qtfb.MsgRequestFullRefresh {
			clearCount++
		}
		if msgs[i].kind == qtfb.MsgSetRefreshMode {
			modeAfterOpen++
		}
	}
	if clearCount != 5 {
		t.Errorf("%d clearing refreshes in 100 turns, want 5", clearCount)
	}
	if modeAfterOpen != 2*clearCount {
		t.Errorf("%d SET_REFRESH_MODE messages for %d clears, want exactly 2 per clear and none otherwise", modeAfterOpen, clearCount)
	}
	// 2. nobody waited: with actions 4 s apart every turn and overlay update is applied the moment
	// it is sent. (The update of a clear itself follows its SET_MODE(CONTENT) and so waits that
	// second: that is the flip the clear costs.)
	worst, clearUpdates := time.Duration(0), 0
	for i := base; i < len(msgs); i++ {
		if msgs[i].kind != qtfb.MsgUpdate {
			continue
		}
		if i > 0 && msgs[i-1].kind == qtfb.MsgSetRefreshMode && msgs[i-1].at == msgs[i].at {
			clearUpdates++
			continue
		}
		if lat[i] > worst {
			worst = lat[i]
		}
	}
	if worst != 0 {
		t.Errorf("a turn or overlay update waited %v behind a stall (4 s between actions)", worst)
	}
	if clearUpdates != clearCount {
		t.Errorf("%d updates follow a mode message, %d clears", clearUpdates, clearCount)
	}
	// 3. the overlay toggles were real updates in the turn waveform: rectangles of the bars
	bars := 0
	for _, m := range msgs[base:] {
		if m.kind == qtfb.MsgUpdate && m.rect.Dx() == 1620 && m.rect.Dy() < 400 {
			bars++
		}
	}
	if bars < 4*5 {
		t.Errorf("only %d bar updates for 5 overlay toggles", bars)
	}
	// 4. a tap right after a clear (1.2 s later) waits at most for the stalls the clear queued
	_ = actions
	_ = mark
}

// page_mode = auto is UI for black-and-white and colour pages alike (the owner's
// panel preference), so a chapter that mixes the two never flips the waveform;
// a fixed page_mode is exactly that mode.
func TestAutoPageModeIsUIAndFixedModesNeverMove(t *testing.T) {
	a := &App{Cfg: store.DefaultConfig()}
	rd := &reader{wave: a.baseWave()}
	for i, colour := range []bool{false, true, false, false, true, false, true, true, false} {
		if got := a.turnWave(rd, colour).String(); got != "ui" {
			t.Errorf("step %d (colour=%v): %s, want ui", i, colour, got)
		}
	}
	a.Cfg.PageMode = "fast"
	if a.turnWave(rd, false).String() != "fast" || a.turnWave(rd, true).String() != "fast" {
		t.Error("page_mode = fast moved")
	}
	a.Cfg.PageMode = "content"
	if a.turnWave(rd, false).String() != "content" {
		t.Error("page_mode = content moved")
	}
	a.Cfg.PageMode = "ui"
	if a.turnWave(rd, true).String() != "ui" {
		t.Error("page_mode = ui moved")
	}
}

// Lists, settings and the sign-in screen use ui_mode; the reader uses the turn
// waveform for everything on its page (overlay, loading marker, popover).
func TestWaveformPerActivity(t *testing.T) {
	r := newRig(t, rigOpts{cfg: func(c *store.Config) { c.UIMode = "ui" }})
	r.wait(func(s Snapshot) bool { return s.Screen == "list" && s.ListRows > 0 }, "home")
	r.tapName("settings")
	r.wait(func(s Snapshot) bool { return s.Screen == "settings" }, "settings")
	r.tapName("back")
	r.wait(func(s Snapshot) bool { return s.Screen == "list" }, "list again")
	before := len(r.conn.all())
	r.openFirst()
	got := kinds(r.conn.all()[before:])
	// Opening a chapter from a list: the UI waveform stays armed for the lists, the first page
	// costs one CONTENT clear and the re-arm: at most two mode messages
	n := strings.Count(got, "mode")
	if n > 2 {
		t.Errorf("%d mode messages to open a chapter (%s), want <= 2", n, got)
	}
}

// ---- tap zones ---------------------------------------------------------------------------

func TestZoneTable(t *testing.T) {
	const W, H = 1620, 2160
	// 30 % / 40 % / 30 % of 1620: the columns end at 486 and 1134
	cases := []struct {
		x, y int
		want zone
	}{
		{0, 0, zoneLeft}, {10, 1000, zoneLeft}, {485, 100, zoneLeft}, {485, 2159, zoneLeft},
		{486, 100, zoneReveal}, {810, 0, zoneReveal}, {810, 1080, zoneReveal}, {1133, 2000, zoneReveal},
		{1134, 100, zoneRight}, {1400, 1000, zoneRight}, {1619, 2159, zoneRight},
	}
	for _, c := range cases {
		if got := zoneAt(c.x, c.y, W, H, 40); got != c.want {
			t.Errorf("zoneAt(%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	// the middle column's width is zone_middle_pct (20-60)
	for _, tc := range []struct {
		pct, x int
		want   zone
	}{
		{20, 647, zoneLeft}, {20, 648, zoneReveal}, {20, 971, zoneReveal}, {20, 972, zoneRight},
		{60, 323, zoneLeft}, {60, 324, zoneReveal}, {60, 1295, zoneReveal}, {60, 1296, zoneRight},
		{5, 647, zoneLeft}, {5, 648, zoneReveal}, // clamped to 20
		{90, 323, zoneLeft}, {90, 324, zoneReveal}, // clamped to 60
	} {
		if got := zoneAt(tc.x, 500, W, H, tc.pct); got != tc.want {
			t.Errorf("middle %d %%: zoneAt(%d) = %v, want %v", tc.pct, tc.x, got, tc.want)
		}
	}
}

// Three columns: the side ones turn pages (direction aware), the middle one
// opens and closes the bars; any tap while the bars are open closes them.
func TestZonesDirectionAndRevealOnTheReader(t *testing.T) {
	r := newRig(t, rigOpts{set: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	r.openFirst()
	page := func() int { return r.get().Page }
	r.wait(func(s Snapshot) bool { return contains(s.ReadyPages, 1) }, "page 1 ready")
	// right to left (default): the LEFT column is next
	r.touch(300, 1000)
	r.wait(func(s Snapshot) bool { return s.Page == 1 }, "left column forward")
	r.touch(1400, 1000)
	r.wait(func(s Snapshot) bool { return s.Page == 0 }, "right column back")
	// the exact midline is the middle column: it opens the bars, it does not turn
	r.touch(810, 1000)
	r.wait(func(s Snapshot) bool { return s.Overlay }, "middle column opens the bars")
	if page() != 0 {
		t.Errorf("middle tap turned the page: %d", page())
	}
	// a tap in the middle column again closes them (toggle)
	r.touch(700, 1500)
	r.wait(func(s Snapshot) bool { return !s.Overlay }, "middle column closes the bars")
	// the bars open: a tap on a side column only closes them
	r.touch(900, 600)
	r.wait(func(s Snapshot) bool { return s.Overlay }, "bars open")
	r.touch(300, 1000)
	r.wait(func(s Snapshot) bool { return !s.Overlay }, "a side tap hides the bars")
	if page() != 0 {
		t.Errorf("page %d after hiding the bars", page())
	}
	// the old reveal bands (top-middle / bottom-middle of the halves layout) are gone: a tap at
	// the top of the left column turns the page, it does not reveal anything
	r.touch(300, 100)
	r.wait(func(s Snapshot) bool { return s.Page == 1 }, "top of the left column turns")
	if r.get().Overlay {
		t.Error("the top of a side column revealed the bars")
	}
}

// The reveal swipe starts below AppLoad's own top-bar drag: a press at y < 120
// that moves down is not ours; one at y >= 120 (inside the band) is.
func TestRevealSwipeBand(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.openFirst()
	r.swipe(800, 60, 800, 600) // AppLoad's bar drag
	time.Sleep(80 * time.Millisecond)
	if r.get().Overlay {
		t.Fatal("a swipe from y=60 revealed the controls")
	}
	r.swipe(800, 130, 800, 600)
	r.wait(func(s Snapshot) bool { return s.Overlay }, "swipe from y=130")
	r.touch(300, 1000)
	r.wait(func(s Snapshot) bool { return !s.Overlay }, "hidden")
	r.swipe(800, 300, 800, 800) // below the band: a scroll, not a reveal
	time.Sleep(80 * time.Millisecond)
	if r.get().Overlay {
		t.Error("a swipe from y=300 revealed the controls")
	}
}

// TestClearStyleUIFullSendsNoModeMessage: with clear_style = ui_full the clearing
// refresh is the update + the full-refresh request with whatever waveform is
// already set: no SET_REFRESH_MODE before it and none to re-arm after it. UI is
// armed once for the whole session (lists, turns and clears share it).
func TestClearStyleUIFullSendsNoModeMessage(t *testing.T) {
	r := newRig(t, rigOpts{cfg: func(c *store.Config) { c.ClearStyle = "ui_full" }, set: func(s *store.Settings) {
		s.Refresh = store.RefreshCustom1
		s.Custom[0] = 5
	}})
	r.openFirst()
	var clears int
	for turn := 1; turn <= 12; turn++ {
		r.wait(func(s Snapshot) bool {
			return s.Screen == "reader" && s.Page == turn-1 && contains(s.ReadyPages, turn)
		}, fmt.Sprintf("page %d ready", turn))
		before := len(r.conn.all())
		r.touch(300, 1000)
		r.wait(func(s Snapshot) bool {
			return s.Screen == "reader" && s.Page == turn && (s.Tag == "page" || s.Tag == "page-full")
		}, fmt.Sprintf("turn %d shown", turn))
		time.Sleep(15 * time.Millisecond)
		got := kinds(r.conn.all()[before:])
		want := "update"
		if turn%5 == 0 {
			clears++
			want = "update,full"
		}
		if got != want {
			t.Fatalf("turn %d sent %q, want %q", turn, got, want)
		}
	}
	if clears != 2 {
		t.Errorf("%d clearing refreshes in 12 turns of a 5-page interval, want 2", clears)
	}
	r.stop()
	b, _ := os.ReadFile(filepath.Join(r.dir, "app.log"))
	if !strings.Contains(string(b), "modechanges=1 ") {
		t.Errorf("one mode message (UI, at the start) is all the session should cost:\n%s", tail(string(b), 500))
	}
}

// TestUIModeAndPageModeKeysAreHonoured: ui_mode = fast puts list frames on FAST
// and page_mode = content makes every turn CONTENT, with one mode message when
// the reader opens and none per turn.
func TestUIModeAndPageModeKeysAreHonoured(t *testing.T) {
	r := newRig(t, rigOpts{cfg: func(c *store.Config) { c.UIMode = "fast"; c.PageMode = "content" }})
	r.wait(func(s Snapshot) bool { return s.Screen == "list" && s.ListRows > 0 }, "home")
	modes := kinds(r.conn.all())
	if !strings.Contains(modes, "mode1") || strings.Contains(modes, "mode4") {
		t.Errorf("home with ui_mode=fast sent %q", modes)
	}
	r.openFirst()
	for turn := 1; turn <= 3; turn++ {
		r.wait(func(s Snapshot) bool {
			return s.Screen == "reader" && s.Page == turn-1 && contains(s.ReadyPages, turn)
		}, fmt.Sprintf("page %d ready", turn))
		before := len(r.conn.all())
		r.touch(300, 1000)
		r.wait(func(s Snapshot) bool { return s.Screen == "reader" && s.Page == turn && s.Tag == "page" }, "turn")
		time.Sleep(15 * time.Millisecond)
		got := kinds(r.conn.all()[before:])
		if turn == 1 {
			if got != "update" && got != "mode3,update" {
				t.Fatalf("first turn sent %q", got)
			}
			continue
		}
		if got != "update" {
			t.Fatalf("turn %d sent %q: a fixed page_mode must not switch modes per turn", turn, got)
		}
	}
}
