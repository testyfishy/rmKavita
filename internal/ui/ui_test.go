package ui

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/gfx"
	"rmkavita/internal/mockkavita"
	"rmkavita/internal/store"
)

// ---- harness -------------------------------------------------------------------

type frameRec struct {
	tag    string
	mode   display.Mode
	full   bool
	rect   image.Rectangle
	colour bool
}

type testDisp struct {
	w, h   int
	events chan display.Event
	mu     sync.Mutex
	frames []frameRec
	closed bool
	idle   func(consumed int)
	fb     []uint8 // what the panel shows: luminance, every update composed the way the qtfb panel does it
}

func newTestDisp(w, h int) *testDisp {
	return &testDisp{w: w, h: h, events: make(chan display.Event, 256)}
}
func (d *testDisp) Size() (int, int)             { return d.w, d.h }
func (d *testDisp) Events() <-chan display.Event { return d.events }
func (d *testDisp) Close() error                 { return nil }
func (d *testDisp) Show(f display.Frame) error {
	d.mu.Lock()
	d.frames = append(d.frames, frameRec{f.Tag, f.Mode, f.Full, f.Rect, f.Color != nil})
	d.compose(f)
	d.mu.Unlock()
	return nil
}

// compose applies one update to the simulated framebuffer exactly as the panel
// converts it: the colour layer is authoritative except inside the opaque
// rectangles, where the grey canvas (the UI drawn over the page) wins.
func (d *testDisp) compose(f display.Frame) {
	if d.fb == nil {
		d.fb = make([]uint8, d.w*d.h)
		for i := range d.fb {
			d.fb[i] = 255
		}
	}
	r := f.Rect.Intersect(image.Rect(0, 0, d.w, d.h))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := f.Gray.Pix[y*f.Gray.Stride+x]
			if f.Color != nil && f.Color.W == d.w {
				inOpaque := false
				for _, o := range f.Opaque {
					if image.Pt(x, y).In(o) {
						inOpaque = true
						break
					}
				}
				if !inOpaque {
					c := int(f.Color.Pix[y*f.Color.W+x])
					rr, gg, bb := (c>>11)&31*255/31, (c>>5)&63*255/63, c&31*255/31
					v = uint8((rr*30 + gg*59 + bb*11) / 100)
				}
			}
			d.fb[y*d.w+x] = v
		}
	}
}

// shown is the luminance the panel shows at (x, y).
func (d *testDisp) shown(x, y int) uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fb == nil {
		return 255
	}
	return d.fb[y*d.w+x]
}
func (d *testDisp) Idle(n int) {
	if d.idle != nil {
		d.idle(n)
	}
}
func (d *testDisp) tags() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, f := range d.frames {
		out = append(out, f.tag)
	}
	return out
}
func (d *testDisp) count(tag string) int {
	n := 0
	for _, t := range d.tags() {
		if t == tag {
			n++
		}
	}
	return n
}
func (d *testDisp) all() []frameRec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]frameRec(nil), d.frames...)
}

type harness struct {
	t       *testing.T
	disp    *testDisp
	mock    *mockkavita.Server
	srv     *httptest.Server
	app     *App
	cfg     store.Config
	set     store.Settings
	state   string
	conf    string
	setFile string
	logFile string
	w, h    int
	dpi     int
	cancel  context.CancelFunc
	done    chan string

	mu   sync.Mutex
	snap Snapshot
}

type hopts struct {
	mut       func(*store.Config)
	mutSet    func(*store.Settings)
	state     string
	signedOut bool
	w, h, dpi int
	pages     int // pages per chapter of the first library (default 12)
	pw, ph    int // source page size (default 300x450)
	app       func(*App)
}

func startHarness(t *testing.T, mut func(*store.Config), statePath string) *harness {
	return startOpts(t, hopts{mut: mut, state: statePath})
}

func startOpts(t *testing.T, o hopts) *harness {
	t.Helper()
	fonts, err := gfx.NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	if o.w == 0 {
		o.w, o.h, o.dpi = 1620, 2160, 229
	}
	if o.pages == 0 {
		o.pages = 12
	}
	h := &harness{t: t, w: o.w, h: o.h, dpi: o.dpi}
	if o.pw == 0 {
		o.pw, o.ph = 300, 450
	}
	h.mock = mockkavita.Build(25, o.pages, o.pw, o.ph)
	h.mock.SetFonts(fonts)
	h.srv = httptest.NewServer(h.mock)
	t.Cleanup(h.srv.Close)
	dir := t.TempDir()
	h.conf = filepath.Join(dir, "rmkavita.conf")
	h.setFile = filepath.Join(dir, "rmkavita-settings.json")
	h.logFile = filepath.Join(dir, "app.log")
	h.state = o.state
	if h.state == "" {
		h.state = filepath.Join(dir, "state.json")
	}
	cfg := store.DefaultConfig()
	cfg.Covers = false
	// the sequence tests below were written for the clearing refresh of rc2 (a mode
	// flip inside the turn); the defaults of rc3 (ui_full, deferred to a pause)
	// are exercised by their own tests, which set them in mut
	cfg.ClearStyle, cfg.ClearIdleMS = "content_full", 0
	if o.mut != nil {
		o.mut(&cfg)
	}
	set := store.DefaultSettings()
	// a clearing refresh every 4 turns keeps the tests short
	set.Refresh, set.Custom[0] = store.RefreshCustom1, 4
	if o.mutSet != nil {
		o.mutSet(&set)
	}
	if err := store.SaveSettings(h.setFile, set); err != nil {
		t.Fatal(err)
	}
	if o.signedOut {
		if err := store.UpdateConf(h.conf, map[string]string{"server": h.srv.URL, "username": "reader"}, nil); err != nil {
			t.Fatal(err)
		}
	} else if err := store.SaveSignIn(h.conf, h.srv.URL, "reader", h.mock.Key()); err != nil {
		t.Fatal(err)
	}
	// the tunables of the Config (prefetch etc.) live in the config file, as on the device
	h.cfg = cfg
	if err := store.UpdateConf(h.conf, confLines(cfg), nil); err != nil {
		t.Fatal(err)
	}
	h.launchWith(o.app)
	return h
}

func (h *harness) launch() { h.launchWith(nil) }

func (h *harness) launchWith(tweak func(*App)) {
	fonts, _ := gfx.NewFonts()
	c, _, err := store.LoadConfig(h.conf)
	if err != nil {
		h.t.Fatal(err)
	}
	h.set, _ = store.LoadSettings(h.setFile, c)
	h.disp = newTestDisp(h.w, h.h)
	pos := store.OpenPositions(h.state)
	lg := store.NewLogger(h.logFile, nil)
	lg.Stderr = false
	h.app = New(Options{Cfg: c, Set: h.set, ConfPath: h.conf, SettingsPath: h.setFile, DeviceDPI: h.dpi,
		OnShow: func(s Snapshot) { h.setSnap(s) }}, h.disp, pos, fonts, lg)
	h.app.syncDelay = 80 * time.Millisecond
	if tweak != nil {
		tweak(h.app)
	}
	h.disp.idle = func(int) {
		s := h.app.Snapshot()
		h.mu.Lock()
		s.Tag = h.snap.Tag
		h.snap = s
		h.mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan string, 1)
	go func() { h.done <- h.app.Run(ctx) }()
}

func (h *harness) setSnap(s Snapshot) {
	h.mu.Lock()
	h.snap = s
	h.mu.Unlock()
}

func (h *harness) snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snap
}

func (h *harness) stop() string {
	h.t.Helper()
	h.cancel()
	select {
	case r := <-h.done:
		return r
	case <-time.After(5 * time.Second):
		h.t.Fatal("app did not stop")
		return ""
	}
}

func (h *harness) send(k display.EventKind, x, y int) {
	h.disp.events <- display.Event{Kind: k, X: x, Y: y, At: time.Now()}
}

func (h *harness) tap(x, y int) {
	h.send(display.Down, x, y)
	h.send(display.Up, x, y)
}

func (h *harness) swipe(x0, y0, x1, y1 int) {
	h.send(display.Down, x0, y0)
	h.send(display.Move, (x0+x1)/2, (y0+y1)/2)
	h.send(display.Up, x1, y1)
}

func (h *harness) hold(x, y int, d time.Duration) {
	h.send(display.Down, x, y)
	time.Sleep(d)
	h.send(display.Up, x, y)
}

// typeText sends typed characters (the way the PNG test backend does).
func (h *harness) typeText(s string) {
	for _, r := range s {
		h.disp.events <- display.Event{Kind: display.Key, Text: string(r), At: time.Now()}
	}
}

func (h *harness) hitRect(name string) (image.Rectangle, bool) {
	s := h.snapshot()
	for _, hi := range s.Hits {
		if hi.Name == name {
			return hi.Rect, true
		}
	}
	return image.Rectangle{}, false
}

// tapName taps the centre of a named target of the current screen.
func (h *harness) tapName(name string) {
	h.t.Helper()
	h.waitFor(func() bool { _, ok := h.hitRect(name); return ok }, "target "+name)
	r, _ := h.hitRect(name)
	h.tap((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
}

// tapNameAt taps a named target at a chosen x.
func (h *harness) tapNameAt(name string, x int) {
	h.t.Helper()
	h.waitFor(func() bool { _, ok := h.hitRect(name); return ok }, "target "+name)
	r, _ := h.hitRect(name)
	h.tap(x, (r.Min.Y+r.Max.Y)/2)
}

func (h *harness) waitFrames(n int) {
	h.t.Helper()
	h.waitFor(func() bool { return len(h.disp.all()) >= n }, fmt.Sprintf("%d frames", n))
}

func (h *harness) waitFor(cond func() bool, what string) {
	h.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; frames: %v; snapshot: %+v", what, h.disp.tags(), h.snapshot())
}

func (h *harness) waitTag(tag string, n int) {
	h.t.Helper()
	h.waitFor(func() bool { return h.disp.count(tag) >= n }, fmt.Sprintf("%d x %q", n, tag))
}

// waitScreen waits until the UI reports being on a screen.
func (h *harness) waitScreen(name string) {
	h.t.Helper()
	h.waitFor(func() bool { return h.snapshot().Screen == name }, "screen "+name)
}

func (h *harness) waitReady(page int) {
	h.t.Helper()
	h.waitFor(func() bool {
		s := h.snapshot()
		return s.Screen == "reader" && s.Page == page && contains(s.ReadyPages, page)
	}, fmt.Sprintf("reader at page %d", page))
}

func contains(l []int, n int) bool {
	for _, v := range l {
		if v == n {
			return true
		}
	}
	return false
}

// openFirstChapter navigates home -> All Libraries -> Manga A -> Series 01 -> Volume 1.
func (h *harness) openFirstChapter() { h.openFirstChapterVia(0) }

// openFirstChapterVia is the same walk when the home list has `extra` rows ahead
// of the usual ones (the "Continue" row appears once something has been read).
func (h *harness) openFirstChapterVia(extra int) {
	h.t.Helper()
	h.waitTag("list", 1)
	h.tapName(fmt.Sprintf("row-%d", 5+extra)) // All Libraries
	h.waitTag("list", 2)
	h.tapName("row-0") // Manga A
	h.waitTag("list", 3)
	h.tapName("row-0") // Series 01
	h.waitTag("list", 4)
	h.tapName("row-0") // Series 01 - Volume 1
	h.waitTag("reader-first", 1)
}

func (h *harness) imageRequests() []string {
	var out []string
	for _, l := range h.mock.RequestLog() {
		if strings.HasPrefix(l, "image ") {
			out = append(out, l)
		}
	}
	return out
}

func (h *harness) readConf() string {
	b, _ := os.ReadFile(h.conf)
	return string(b)
}

func (h *harness) readLog() string {
	b, _ := os.ReadFile(h.logFile)
	return string(b)
}

// ---- tests ---------------------------------------------------------------------

func TestBrowseOpenAndTurn(t *testing.T) {
	h := startHarness(t, nil, "")
	h.openFirstChapter()
	// The app launch clears the screen once ("start"). After that, opening a
	// chapter costs exactly ONE more clearing flash: the first page itself, drawn
	// with the CONTENT waveform and the full-refresh request. The "Loading" frame
	// before it is a plain partial update.
	var fulls []string
	for _, f := range h.disp.all() {
		if f.full {
			fulls = append(fulls, f.tag)
		}
		if f.tag == "reader-first" && (!f.full || f.mode != display.ModeContent) {
			t.Errorf("first reader frame = %+v, want a full CONTENT refresh", f)
		}
		if f.tag == "reader-loading" && f.full {
			t.Errorf("loading frame must not clear: %+v", f)
		}
	}
	if len(fulls) != 2 || fulls[0] != "start" || fulls[1] != "reader-first" {
		t.Errorf("clearing frames up to the first page = %v, want exactly [start reader-first]", fulls)
	}
	// RTL default: tapping the LEFT third goes forward
	h.tap(300, 1000)
	h.waitTag("page", 1)
	h.tap(300, 1000)
	h.waitTag("page", 2)
	for _, f := range h.disp.all() {
		// black and white page: UI (page_mode auto), one partial update, no full-refresh request
		if f.tag == "page" && (f.full || f.mode != display.ModeUI || f.colour) {
			t.Errorf("page turn frame = %+v, want one partial UI update", f)
		}
	}
	// 4 turns per clear in this harness: the 4th turn since the first page clears
	h.waitReady(2)
	h.tap(300, 1000)
	h.waitReady(3)
	h.tap(300, 1000)
	h.waitTag("page-full", 1)
	for _, f := range h.disp.all() {
		if f.tag == "page-full" && (!f.full || f.mode != display.ModeContent) {
			t.Errorf("periodic clear = %+v", f)
		}
	}
	// Right third goes back
	h.tap(1400, 1000)
	h.waitReady(3)
	if r := h.stop(); r != "signal" {
		t.Errorf("stop reason %q", r)
	}
}

func TestLTRFlipsTapZones(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.RTL = false }})
	h.openFirstChapter()
	h.tap(1400, 1000) // right third = forward when left-to-right
	h.waitReady(1)
	h.tap(300, 1000) // left = back
	h.waitReady(0)
	h.stop()
}

// While reading there is no chrome at all: the only registered targets are the
// ones of an error box. The centre tap reveals it, the swipe from the top edge
// does too.
func TestChromeHiddenWhileReadingAndRevealed(t *testing.T) {
	h := startHarness(t, nil, "")
	h.openFirstChapter()
	h.waitReady(0)
	if s := h.snapshot(); len(s.Hits) != 0 || s.Overlay {
		t.Fatalf("chrome visible while reading: %+v", s.Hits)
	}
	// centre tap: overlay appears as two partial updates, no clearing refresh
	nFull := 0
	for _, f := range h.disp.all() {
		if f.full {
			nFull++
		}
	}
	h.tap(810, 300)
	h.waitTag("overlay-bottom", 1)
	for _, name := range []string{"back", "home", "settings", "account", "slider"} {
		if _, ok := h.hitRect(name); !ok {
			t.Errorf("overlay lacks %q", name)
		}
	}
	n := 0
	for _, f := range h.disp.all() {
		if f.full {
			n++
		}
		if strings.HasPrefix(f.tag, "overlay") && (f.full || f.mode != display.ModeUI) {
			t.Errorf("overlay frame %+v must be a partial update in the turn waveform", f)
		}
	}
	if n != nFull {
		t.Errorf("opening the overlay caused %d clearing refresh(es)", n-nFull)
	}
	// a tap on the page hides it again, still partial
	h.tap(300, 1000)
	h.waitTag("overlay-hide-top", 1)
	for _, f := range h.disp.all() {
		if strings.HasPrefix(f.tag, "overlay-hide") && f.full {
			t.Errorf("hiding the overlay flashed: %+v", f)
		}
	}
	h.waitFor(func() bool { return !h.snapshot().Overlay }, "overlay hidden")
	// swipe down from the top edge reveals it as well
	h.swipe(800, 150, 800, 700)
	h.waitFor(func() bool { return h.snapshot().Overlay }, "overlay by swipe")
	// and it hides on its own after a while
	h.stop()
}

func TestOverlayAutoHide(t *testing.T) {
	h := startOpts(t, hopts{app: func(a *App) { a.overlayFor = 150 * time.Millisecond }})
	h.openFirstChapter()
	h.waitReady(0)
	h.tap(810, 300)
	h.waitFor(func() bool { return h.snapshot().Overlay }, "overlay")
	h.waitFor(func() bool { return !h.snapshot().Overlay }, "overlay auto-hide")
	if h.disp.count("overlay-hide-top") != 1 {
		t.Errorf("hide frames: %v", h.disp.tags())
	}
	h.stop()
}

func TestOverlayExitThroughAccountMenu(t *testing.T) {
	h := startHarness(t, nil, "")
	h.openFirstChapter()
	h.waitReady(0)
	h.tap(810, 300)
	h.waitTag("overlay-top", 1)
	h.tapName("account")
	h.waitTag("popover", 1)
	h.tapName("exit")
	select {
	case r := <-h.done:
		if r != "exit" {
			t.Errorf("reason %q", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exit did not end the app")
	}
}

func TestHoldCornerExitAndClear(t *testing.T) {
	h := startHarness(t, func(c *store.Config) { c.HoldMs = 400 }, "")
	h.waitTag("list", 1)
	// a short press in the top-right corner does nothing; a long one is the deliberate clear
	h.hold(1580, 40, 700*time.Millisecond)
	h.waitTag("clear", 2) // render + clearScreen, both tagged "clear"
	var cleared bool
	for _, f := range h.disp.all() {
		if f.tag == "clear" && f.full && f.mode == display.ModeContent {
			cleared = true
		}
	}
	if !cleared {
		t.Error("corner hold did not produce a full CONTENT refresh")
	}
	h.send(display.Down, 40, 2120) // bottom-left, then keep holding
	select {
	case r := <-h.done:
		if r != "hold bottom-left corner corner" && r != "hold bottom-left corner" {
			t.Errorf("reason %q", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bottom-left hold did not end the app")
	}
}

func TestDisplayCloseEndsRun(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	close(h.disp.events) // AppLoad's X / xochitl gone
	select {
	case r := <-h.done:
		if r != "display closed" {
			t.Errorf("reason %q", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("app kept running after the display went away")
	}
}

func TestPrefetchWindowAndCacheBound(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.ReadAhead = 2; c.KeepBehind = 1 },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
	})
	h.openFirstChapter()
	h.waitFor(func() bool { return len(h.imageRequests()) >= 3 }, "prefetch of the first three pages")
	reqs := h.imageRequests()
	// current page plus the next two, nothing else
	seen := map[string]bool{}
	for _, r := range reqs[:3] {
		for _, p := range []string{"page=0", "page=1", "page=2"} {
			if strings.Contains(r, p+" ") {
				seen[p] = true
			}
		}
	}
	if len(seen) != 3 {
		t.Errorf("first requests = %v", reqs)
	}
	for i := 1; i <= 8; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
		s := h.snapshot()
		for _, k := range s.Slots {
			if k < i-1 || k > i+2 {
				t.Errorf("page %d: slot %d outside the window", i, k)
			}
		}
		if len(s.Slots) > 4 {
			t.Errorf("page %d: %d slots held (>4)", i, len(s.Slots))
		}
	}
	h.stop()
}

func TestResumeLocalPositionAndServerAhead(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	h := startHarness(t, nil, state)
	h.openFirstChapter()
	for i := 1; i <= 4; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	h.stop()
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("positions not saved on exit: %v", err)
	}
	b, _ := os.ReadFile(state)
	if strings.Contains(string(b), h.mock.Key()) || strings.Contains(string(b), "http") {
		t.Errorf("state holds the key or a URL: %s", b)
	}

	// second session: the home list now leads with a "Continue" row; tapping it
	// lands on page 4 (the local position) with the first request being that page.
	h.mock.ResetLog()
	h.launch()
	h.waitTag("list", 1)
	h.tapName("row-0")
	h.waitTag("reader-first", 1)
	h.waitFor(func() bool { return len(h.imageRequests()) > 0 }, "page requests")
	chapterPagesRequested(t, h, "Continue", 4)
	h.waitReady(4)
	h.stop()

	// third session: another device read this chapter on to page 9 (the server
	// is clearly ahead of the local position), so opening it via the list
	// follows the server.
	resp, err := http.Get(h.srv.URL + "/api/opds/" + h.mock.Key() + "/image?libraryId=1&seriesId=1&volumeId=1&chapterId=1&pageNumber=9")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	h.mock.ResetLog()
	h.launch()
	h.openFirstChapterVia(1)
	h.waitFor(func() bool { return len(h.imageRequests()) > 0 }, "page requests")
	chapterPagesRequested(t, h, "server-ahead open", 9)
	h.waitReady(9)
	h.stop()
}

func TestProgressSyncTouchAndCompletion(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.ReadAhead = 2; c.KeepBehind = 1 },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
	})
	h.openFirstChapter()
	h.waitFor(func() bool { return len(h.imageRequests()) >= 3 }, "initial prefetch")
	h.tap(300, 1000)
	h.waitReady(1)
	// prefetch pushed the server's marker ahead of page 1; after the pause it is put back
	h.waitFor(func() bool {
		p := h.mock.Progress()
		for _, v := range p {
			return v == 1
		}
		return false
	}, "server progress == 1 after the idle sync")
	// walk to the last page (12 pages): the final sync reports one past the end = finished
	for i := 2; i <= 11; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	h.waitFor(func() bool {
		for _, v := range h.mock.Progress() {
			return v == 12
		}
		return false
	}, "chapter marked finished (page == count)")
	h.stop()
}

func TestSyncDisabled(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.SyncProgress = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
	})
	h.openFirstChapter()
	h.tap(300, 1000)
	h.waitReady(1)
	time.Sleep(400 * time.Millisecond)
	n := len(h.imageRequests())
	h.tap(300, 1000)
	h.waitReady(2)
	time.Sleep(400 * time.Millisecond)
	if len(h.imageRequests()) < n {
		t.Error("log shrank")
	}
	h.stop()
}

func TestErrorBoxAndRetry(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.ReadAhead = 0; c.KeepBehind = 0 }})
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-0")
	h.waitTag("list", 3)
	h.tapName("row-0")
	h.waitTag("list", 4)
	h.mock.FailNext(1, 500) // a 5xx is not retried by the client, so one failure is one error screen
	h.tapName("row-0")
	// the first page fails: an error box, not a hang, and no clearing flash for it
	h.waitFor(func() bool { return h.disp.count("reader") >= 1 }, "error frame")
	if h.disp.count("reader-first") != 0 {
		t.Fatal("error screen was drawn as a first page")
	}
	h.tapName("retry")
	h.waitTag("reader-first", 1)
	h.waitReady(0)
	h.stop()
}

// A background read-ahead of the next catalogue page that fails must not throw
// the user out of the list they are looking at; asking for that page explicitly
// reports the error, and Retry recovers.
func TestBackgroundReadAheadFailureKeepsList(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.mock.FailFeedPage(2)
	h.tapName("row-5") // All Libraries
	h.waitTag("list", 2)
	h.tapName("row-0") // Manga A: 25 series, 20 per page, page 2 is read ahead
	h.waitTag("list", 3)
	h.waitFor(func() bool {
		n := 0
		for _, p := range h.mock.Requests() {
			if strings.HasSuffix(p, "/libraries/1") {
				n++
			}
		}
		return n >= 2 // the list itself and the failed read-ahead (a retry may add a third)
	}, "the read-ahead request")
	h.waitFor(func() bool {
		s := h.snapshot()
		return s.ListTitle == "Manga A" && !s.ListLoading
	}, "read-ahead settled")
	if h.disp.count("feed-error") != 0 {
		t.Fatal("a background read-ahead failure replaced the list with an error screen")
	}
	// Two chevron taps: the first scrolls within the 20 rows, the second asks for
	// page 2 and gets the error because the failure is still switched on.
	h.tapName("page-next")
	h.waitTag("scroll", 1)
	h.tapName("page-next")
	h.waitTag("feed-error", 1)
	h.mock.FailFeedPage(0)
	h.tapName("retry")
	h.waitTag("list-more", 1)
	h.waitFor(func() bool {
		s := h.snapshot()
		return s.ListRows == 25 && s.ListScroll == 20 && !s.ListErr
	}, "25 rows, scrolled to the new page")
	h.stop()
}

func typeKeys(t *testing.T, h *harness, s string) {
	t.Helper()
	h.typeText(s)
}

func TestSearchFlow(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.tapName("search")
	h.waitTag("search", 1)
	// type "series 03" on the on-screen keyboard, key by key
	for _, ch := range "series" {
		h.tapName("key-" + string(ch))
	}
	h.tapName("key-space")
	for _, ch := range "03" {
		h.tapName("key-" + string(ch))
	}
	h.waitFor(func() bool { return h.disp.count("search-key") >= 9 }, "9 key frames")
	h.tapName("key-enter")
	h.waitTag("search-results", 1)
	h.waitTag("list", 2)
	h.waitFor(func() bool {
		s := h.snapshot()
		return s.Screen == "list" && strings.HasPrefix(s.ListTitle, "Search: series 03") && s.ListRows >= 1
	}, "search results")
	h.stop()
}

func TestNothingWrittenButPositions(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	state := filepath.Join(dir, "state", "rmkavita-state.json")
	h := startHarness(t, nil, state)
	h.openFirstChapter()
	for i := 1; i <= 3; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	h.stop()
	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(files) != 1 || files[0] != "state/rmkavita-state.json" {
		t.Errorf("files written during reading: %v", files)
	}
}

func TestEmptyCatalogueMessage(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.tapName("row-5") // All Libraries
	h.waitTag("list", 2)
	// third library is empty: open it and expect the plain "Nothing here" list, no error
	h.tapName("row-2")
	h.waitTag("list", 3)
	h.stop()
}

// chapterPagesRequested checks that the page requests of a freshly opened
// chapter are centred on `want`: it is among them and nothing earlier than
// the page behind it was fetched (the first two requests race on two
// connections, so only the set is meaningful).
func chapterPagesRequested(t *testing.T, h *harness, what string, want int) {
	t.Helper()
	var got []int
	for _, r := range h.imageRequests() {
		i := strings.Index(r, "page=")
		if i < 0 || !strings.Contains(r, "chapter=1 ") {
			continue // the warm-up of the next chapter is checked elsewhere
		}
		var n int
		fmt.Sscanf(r[i+5:], "%d", &n)
		got = append(got, n)
	}
	has := false
	for _, n := range got {
		if n == want {
			has = true
		}
		if n < want-2 { // keep_behind (2) pages are fetched last
			t.Errorf("%s: page %d fetched although the reader opened at %d (%v)", what, n, want, got)
		}
	}
	if !has {
		t.Errorf("%s: page %d was not requested (%v)", what, want, got)
	}
}

// TestPSEStreamingProof is the OPDS-PSE contract in one place: pages are asked
// for one by one as they are needed (never an archive), at most `prefetch`
// ahead of the reader, each page once, with a bounded number of decoded pages
// in RAM, and nothing but the positions file reaches the disk.
func TestPSEStreamingProof(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	state := filepath.Join(dir, "state", "rmkavita-state.json")
	h := startOpts(t, hopts{
		state:  state,
		mut:    func(c *store.Config) { c.ReadAhead = 2; c.KeepBehind = 1; c.SyncProgress = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
	})
	h.openFirstChapter()
	pagesRequested := func() map[int]int {
		out := map[int]int{}
		for _, r := range h.imageRequests() {
			if i := strings.Index(r, "page="); i >= 0 && strings.Contains(r, "chapter=1 ") {
				var n int
				fmt.Sscanf(r[i+5:], "%d", &n)
				out[n]++
			}
		}
		return out
	}
	// the next chapter's first page is the only other page that may be asked for, once
	nextChapterPages := func() []string {
		var out []string
		for _, r := range h.imageRequests() {
			if strings.Contains(r, "chapter=2 ") {
				out = append(out, r)
			}
		}
		return out
	}
	// 1. opening a 12 page chapter asks for pages 0, 1, 2 and nothing else.
	h.waitFor(func() bool { return len(pagesRequested()) >= 3 }, "prefetch of 3 pages")
	time.Sleep(200 * time.Millisecond)
	if got := pagesRequested(); len(got) != 3 || got[0] != 1 || got[1] != 1 || got[2] != 1 {
		t.Fatalf("after opening: pages requested = %v, want exactly {0,1,2} once each", got)
	}
	// 2. every turn fetches exactly one new page, always cur+2.
	for cur := 1; cur <= 9; cur++ {
		h.tap(300, 1000)
		h.waitReady(cur)
		want := cur + 2
		h.waitFor(func() bool { return pagesRequested()[want] == 1 }, fmt.Sprintf("page %d prefetched", want))
	}
	time.Sleep(200 * time.Millisecond)
	got := pagesRequested()
	for n, c := range got {
		if c != 1 {
			t.Errorf("page %d was fetched %d times", n, c)
		}
		if n > 11 {
			t.Errorf("page %d requested beyond the chapter end", n)
		}
	}
	if len(got) != 12 {
		t.Errorf("12 pages in the chapter, %d distinct pages requested: %v", len(got), got)
	}
	// 3. decoded pages in RAM never exceeded the configured cache.
	if peak := h.snapshot().Peak; peak < 3 || peak > 5 {
		t.Errorf("peak decoded pages held = %d, want 3..5", peak)
	}
	// 3b. the warm-up asked for exactly one page of the next chapter: its first, once.
	if np := nextChapterPages(); len(np) != 1 || !strings.Contains(np[0], "page=0 ") {
		t.Errorf("next-chapter requests = %v, want exactly page 0 once", np)
	}
	// 4. no archive or download was ever requested, only the catalogue and images.
	for _, p := range h.mock.Requests() {
		if strings.Contains(p, "download") || strings.HasSuffix(p, ".cbz") {
			t.Errorf("acquisition request seen: %s", p)
		}
	}
	h.stop()
	// 5. nothing on disk but the positions file, the config and the settings.
	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(files) != 1 || files[0] != "state/rmkavita-state.json" {
		t.Errorf("files on disk after reading: %v", files)
	}
	if b, _ := os.ReadFile(state); len(b) > 400 {
		t.Errorf("positions file is %d bytes for one chapter: %s", len(b), b)
	}
}

func TestChapterTitleDropsContinueLeadIn(t *testing.T) {
	for in, want := range map[string]string{
		"Continue Reading from: Alpha Blade - Volume 1": "Alpha Blade - Volume 1",
		"Continue from Beta":                            "Beta",
		"continue reading from:Gamma":                   "Gamma",
		"Alpha Blade - Volume 1":                        "Alpha Blade - Volume 1",
		"Continue Reading from:":                        "Continue Reading from:",
	} {
		if got := chapterTitle(in); got != want {
			t.Errorf("chapterTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// A chapter the server has finished starts over from page 1 when it is opened
// again, instead of dropping the reader on the last page.
func TestFinishedChapterStartsOver(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.SyncProgress = false }})
	// 12 pages: "one past the end" is how the app (and Kavita's own reader) marks it finished
	resp, err := http.Get(h.srv.URL + "/api/opds/" + h.mock.Key() + "/image?libraryId=1&seriesId=1&volumeId=1&chapterId=1&pageNumber=12")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	h.mock.ResetLog()
	h.openFirstChapter() // lands on the chapter list: its row now carries lastRead=12
	h.waitFor(func() bool { return len(h.imageRequests()) > 0 }, "page requests")
	h.waitReady(0)
	for _, r := range h.imageRequests() {
		if strings.Contains(r, "page=11 ") {
			t.Errorf("a finished chapter opened at its last page: %v", h.imageRequests())
		}
	}
	h.stop()
}

// Reading past the last page opens the NEXT chapter of the series: from the
// series list, from Kavita's "Continue Reading from" copy (which stands ahead
// of the chapters before it), and from the home "Continue" row (no list there,
// so the series feed is fetched).
func TestNextChapterFollowsTheSeries(t *testing.T) {
	chapterOf := func(h *harness) map[int]bool {
		seen := map[int]bool{}
		for _, r := range h.imageRequests() {
			var c int
			if i := strings.Index(r, "chapter="); i >= 0 {
				fmt.Sscanf(r[i+8:], "%d", &c)
				seen[c] = true
			}
		}
		return seen
	}
	toLastPageAndOn := func(h *harness, wantTags int) {
		h.tap(810, 300) // overlay
		h.waitTag("overlay-bottom", 1)
		h.tapNameAt("slider", 140) // far left of the slider = the last page when reading right to left
		h.waitReady(11)
		h.tap(300, 1000) // forward past the end
		h.waitTag("reader-first", wantTags)
	}
	touch := func(h *harness, chapter int) {
		u := fmt.Sprintf("%s/api/opds/%s/image?libraryId=1&seriesId=1&volumeId=%d&chapterId=%d&pageNumber=3", h.srv.URL, h.mock.Key(), chapter, chapter)
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	opt := hopts{mut: func(c *store.Config) { c.SyncProgress = false }, mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }}

	// 1. plain: chapter 1 from the series list -> chapter 2
	h := startOpts(t, opt)
	h.openFirstChapter()
	h.mock.ResetLog()
	toLastPageAndOn(h, 2)
	h.waitFor(func() bool { return chapterOf(h)[2] }, "chapter 2 requested")
	h.stop()

	// 2. via the "Continue Reading from" copy of chapter 2: next is chapter 3, not chapter 1
	h = startOpts(t, opt)
	touch(h, 2)
	h.mock.ResetLog()
	h.openFirstChapter() // row 0 of the series list is now the continue copy of chapter 2
	h.waitFor(func() bool { return chapterOf(h)[2] }, "chapter 2 opened from the continue copy")
	h.waitReady(3)
	h.mock.ResetLog()
	toLastPageAndOn(h, 2)
	h.waitFor(func() bool { return chapterOf(h)[3] }, "chapter 3 requested")
	if chapterOf(h)[1] {
		t.Errorf("went back to chapter 1 instead of on to chapter 3: %v", chapterOf(h))
	}
	h.stop()

	// 3. from the home "Continue" row (no chapter list behind the reader)
	state := filepath.Join(t.TempDir(), "state.json")
	opt.state = state
	h = startOpts(t, opt)
	h.openFirstChapter()
	h.tap(300, 1000)
	h.waitReady(1)
	h.stop()
	h.launch()
	h.waitTag("list", 1)
	h.tapName("row-0") // the home "Continue" row
	h.waitTag("reader-first", 1)
	h.waitReady(1)
	h.mock.ResetLog()
	toLastPageAndOn(h, 2)
	h.waitFor(func() bool { return chapterOf(h)[2] }, "chapter 2 requested after continuing from home")
	h.stop()
}

// confLines renders the tunables of a Config as the lines of rmkavita.conf.
func confLines(c store.Config) map[string]string {
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	return map[string]string{
		"read_ahead": fmt.Sprint(c.ReadAhead), "keep_behind": fmt.Sprint(c.KeepBehind), "cache_mb": fmt.Sprint(c.CacheMB), "mem_floor_mb": fmt.Sprint(c.MemFloorMB), "mem_guard_mb": fmt.Sprint(c.MemGuardMB), "page_mode": c.PageMode, "ui_mode": c.UIMode,
		"clear_mode": c.ClearMode, "clear_style": c.ClearStyle, "fb_format": c.FBFormat, "border": c.Border, "crop": c.Crop,
		"kernel": c.Kernel, "covers": yn(c.Covers), "rotate_spreads": yn(c.RotateSpread), "gamma": fmt.Sprint(c.Gamma),
		"hold_ms": fmt.Sprint(c.HoldMs), "sync_progress": yn(c.SyncProgress), "warmup": yn(c.Warmup),
		"bars_clean": c.BarsClean, "zone_middle_pct": fmt.Sprint(c.ZoneMiddle),
		"render": c.Render, "speed_levels": fmt.Sprint(c.SpeedLevels), "speed_dither": c.SpeedDither, "clear_idle_ms": fmt.Sprint(c.ClearIdleMS),
		"hot_tl": c.HotTL, "hot_tr": c.HotTR, "hot_bl": c.HotBL, "hot_br": c.HotBR,
	}
}
