// Package ui is the whole app: the Sign-in screen, a list browser over OPDS
// feeds, a search keyboard, the Settings screen and the page reader. It draws
// into one gray canvas (plus a colour layer for colour pages) and asks the
// display for refreshes; it never touches the disk except for the small
// config, settings and positions files.
package ui

import (
	"context"
	"fmt"
	"image"
	"net/url"
	"strings"
	"sync"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/gfx"
	"rmkavita/internal/imgproc"
	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

// Colours (8-bit gray). High contrast: ink is true black.
const (
	inkBlack  = 0
	inkDim    = 50
	inkRule   = 140
	paperGray = 255
)

// baseDPI is the density the layout numbers are written for (the Paper Pro).
const baseDPI = 229

// Messages from background goroutines to the UI loop.
type (
	done    struct{ m any }
	feedMsg struct {
		lv     *listView
		feed   *opds.Feed
		err    *opds.Error
		appnd  bool
		reqURL string
	}
	pageMsg struct {
		r   *reader
		n   int
		id  int
		gen int
		img *imgproc.Fitted
		err *opds.Error
		net time.Duration // fetching the page
		cpu time.Duration // decoding, fitting (and quantising) it
	}
	thumbMsg struct {
		url string
		img *image.Gray
	}
	searchMsg struct {
		q     string
		url   string
		title string
		err   *opds.Error
	}
	syncMsg struct {
		r    *reader
		page int
	}
	nextMsg struct {
		r   *reader
		lv  *listView
		err *opds.Error
	}
	loginMsg struct {
		si     *signin
		server string
		user   string
		key    string
		err    *opds.Error
	}
	authMsg struct {
		sess int
		err  *opds.Error
	}
)

type screenKind int

const (
	scrList screenKind = iota
	scrReader
	scrSearch
	scrSettings
	scrSignin
)

// Options configures the app.
type Options struct {
	Cfg          store.Config
	Set          store.Settings
	ConfPath     string
	SettingsPath string
	DeviceDPI    int            // the device's own density (0 = the Paper Pro's)
	Debug        bool           // debug-level log lines
	Version      string         // shown on the About screen
	OnShow       func(Snapshot) // called after every update sent to the display (tests, diagnostics)
}

// App is the UI state machine.
type App struct {
	Cfg   store.Config
	Set   store.Settings
	Cl    *opds.Client
	Disp  display.Display
	Pos   *store.Positions
	Fonts *gfx.Fonts
	Log   *store.Logger

	confPath, setPath string
	deviceDPI, dpi    int
	debug             bool
	version           string
	onShow            func(Snapshot)

	W, H int
	k    float64
	cv   *gfx.Canvas

	// colour layer: a full-screen RGB565 picture used while a colour page is on
	// screen; opaque lists the rectangles where the gray canvas (overlay, popover)
	// wins over it.
	col         []uint16
	layerActive bool
	opaque      []image.Rectangle

	ctx     context.Context
	cancel  context.CancelFunc
	sctx    context.Context // per signed-in session: logout cancels it
	scancel context.CancelFunc
	sess    int
	msgs    chan any
	pending int

	stack []*listView
	rd    *reader
	kb    *keyboard
	si    *signin
	sv    *settingsView
	pop   popover
	hits  []hit

	touch      touchState
	ts         touchStats
	open       map[int]*openContact // contacts down and not yet up (see touch.go)
	touchT     *time.Timer          // classifies a stationary press as a resting palm
	clrT       *time.Timer          // a due clearing refresh runs when the reader pauses (clear_idle_ms)
	sleepsSeen int                  // mode messages + full requests already accounted in a tel line
	touchGen   int
	palmAfter  time.Duration // tests shorten it; 0 = the default
	memT       *time.Timer   // returns freed memory to the system once the app is idle
	holdGen    int
	holdT      *time.Timer
	syncT      *time.Timer
	thumbT     *time.Timer
	ovT        *time.Timer // overlay auto-hide
	repT       *time.Timer // hold-to-repeat (steppers, backspace)
	thumbs     map[string]*thumb
	thumbOrd   []string

	rootPath    string
	searchURL   string
	rechecking  bool
	sinceFull   int
	syncDelay   time.Duration // pause after a page turn before progress is re-synced
	overlayFor  time.Duration // overlay auto-hide delay
	slotSeq     int
	consumed    int
	decodeSem   chan struct{} // two tokens: a small page takes one, a big one (> 4 MP) both
	decodeMu    sync.Mutex
	quitting    bool
	reason      string
	pagesDirty  bool         // a setting that changes how pages are fitted was edited
	t0          time.Time    // start of the process (telemetry clock)
	armed       display.Mode // the refresh mode the server has been told
	memAvail    func() int   // MemAvailable in MB, -1 when unknown (tests replace it)
	memWarned   bool
	floorWarned bool
	handOver    *warmPage // the warm page of the reader being replaced (openReader takes it)
	showSeq     int
	setDirty    bool // settings changed and not yet written

	// latency telemetry: when the finger that caused the next update lifted, and
	// what the page that update shows cost if it had to be fetched and prepared
	inAt    time.Time
	waitNet time.Duration
	waitCPU time.Duration
	bpp     int // bytes per framebuffer pixel (for the bytes= field)
}

// New builds the app. The list (or the Sign-in screen) appears in Run.
func New(o Options, d display.Display, pos *store.Positions, fonts *gfx.Fonts, log *store.Logger) *App {
	w, h := d.Size()
	a := &App{Cfg: o.Cfg, Set: o.Set, Cl: opds.NewUnsigned(), Disp: d, Pos: pos, Fonts: fonts, Log: log, W: w, H: h,
		confPath: o.ConfPath, setPath: o.SettingsPath, deviceDPI: o.DeviceDPI, debug: o.Debug, version: o.Version, onShow: o.OnShow,
		cv:         gfx.NewCanvas(w, h),
		msgs:       make(chan any, 128),
		thumbs:     map[string]*thumb{},
		open:       map[int]*openContact{},
		decodeSem:  make(chan struct{}, 2),
		syncDelay:  6 * time.Second,
		overlayFor: 8 * time.Second,
		t0:         time.Now(),
		armed:      display.ModeKeep,
		memAvail:   memAvailableMB,
	}
	a.Set.Sanitize()
	a.applyDPI()
	if log != nil {
		log.Redact = a.Cl.Redact
	}
	return a
}

// applyDPI derives the layout scale from the setting (0 = automatic).
func (a *App) applyDPI() {
	d := a.Set.DPI
	if d == 0 {
		d = a.autoDPI()
	}
	a.dpi = d
	a.k = float64(d) / baseDPI
}

// autoDPI is the device's own density.
func (a *App) autoDPI() int {
	if a.deviceDPI > 0 {
		return a.deviceDPI
	}
	return baseDPI
}

// u scales a design pixel (written for the Paper Pro's 229 dpi) to the panel,
// so everything keeps its physical size on any density.
func (a *App) u(v int) int {
	r := int(float64(v)*a.k + 0.5)
	if v > 0 && r < 1 {
		r = 1
	}
	return r
}

func (a *App) st(size int, bold bool, gray uint8) gfx.Style {
	return gfx.Style{Size: a.u(size), Bold: bold, Gray: gray}
}

func (a *App) logf(f string, args ...any) { a.Log.Logf(f, args...) }

func (a *App) debugf(f string, args ...any) {
	if a.debug {
		a.Log.Logf("debug: "+f, args...)
	}
}

func (a *App) full() image.Rectangle { return a.cv.Bounds() }

func (a *App) screen() screenKind {
	switch {
	case a.si != nil:
		return scrSignin
	case a.sv != nil:
		return scrSettings
	case a.kb != nil:
		return scrSearch
	case a.rd != nil:
		return scrReader
	}
	return scrList
}

func (a *App) listVisible() bool { return a.screen() == scrList }

// ---- presenting ---------------------------------------------------------------

func (a *App) frame(r image.Rectangle, mode display.Mode, full bool, tag string, useLayer bool, opaque []image.Rectangle) display.Frame {
	f := display.Frame{Gray: a.cv.Gray, Rect: r, Mode: mode, Full: full, Tag: tag}
	if useLayer && a.col != nil {
		f.Color = &display.Layer{Pix: a.col, W: a.W, H: a.H}
		f.Opaque = opaque
	}
	return f
}

func (a *App) show(f display.Frame) {
	if a.quitting {
		return
	}
	readyAt := time.Now() // the frame is complete: from here on it is the display's
	if err := a.Disp.Show(f); err != nil {
		a.logf("display error: %v", err)
		a.quit("display: " + err.Error())
		return
	}
	showDur := time.Since(readyAt)
	if f.Mode != display.ModeKeep {
		a.armed = f.Mode
	}
	a.showSeq++
	a.logShow(f, readyAt, showDur)
	if a.onShow != nil {
		s := a.Snapshot()
		s.Tag = f.Tag
		a.onShow(s)
	}
}

// updateKind classifies an update for the telemetry line.
func updateKind(f display.Frame) string {
	switch {
	case f.Full:
		return "clear"
	case f.Tag == "page" || f.Tag == "reader":
		return "turn"
	case strings.HasPrefix(f.Tag, "overlay") || strings.HasPrefix(f.Tag, "popover") || f.Tag == "loading" || f.Tag == "reader-loading":
		return "overlay"
	}
	return "list"
}

// logShow writes the telemetry line of one update. It is the comparison format
// for xochitl's journal: one `[QTFB] Updated region x y w h` line there per
// update here, in the same order (match them by region and order; the wall
// clock prefix of the log line aligns the two). flips is the number of
// SET_REFRESH_MODE messages sent so far, full the REQUEST_FULL_REFRESH count.
// No URLs, keys or passwords can appear in it.
func (a *App) logShow(f display.Frame, readyAt time.Time, showDur time.Duration) {
	if f.Rect.Empty() {
		return
	}
	var c display.Counters
	if cd, ok := a.Disp.(display.Counted); ok {
		c = cd.Counters()
	}
	// ready_ms: the finger lifted -> the frame was complete and handed to the
	// display (-1: this update was not caused by a touch). cpu_ms / net_ms: what
	// decoding and fitting, and fetching, the page took when it had to be waited
	// for (0: it was already there). show_ms: converting the rectangle and
	// sending it. bytes: what was written to the framebuffer.
	ready := int64(-1)
	if !a.inAt.IsZero() && readyAt.Sub(a.inAt) < 15*time.Second {
		ready = readyAt.Sub(a.inAt).Milliseconds()
	}
	cpu, net := a.waitCPU.Milliseconds(), a.waitNet.Milliseconds()
	if f.Tag != "loading" && f.Tag != "reader-loading" {
		// the marker that says a page is on its way is not the page: the touch
		// stays pending until the frame it was waiting for goes out
		a.inAt = time.Time{}
		a.waitCPU, a.waitNet = 0, 0
	}
	bpp := a.bpp
	if bpp == 0 {
		bpp = 4
		if fm, ok := a.Disp.(display.Formatted); ok && fm.PixelFormat() == "rgb565" {
			bpp = 2
		}
		a.bpp = bpp
	}
	// sleeps: the mode messages and full-refresh requests this update sent. The
	// panel server sleeps one second after each, so a clearing refresh done as
	// a mode flip + request + flip back costs three of them, and ui_full one.
	sleeps := c.ModeChanges + c.FullReqs - a.sleepsSeen
	a.sleepsSeen = c.ModeChanges + c.FullReqs
	a.logf("tel n=%d t=%d kind=%s mode=%s region=%d,%d,%d,%d full=%d flips=%d fulls=%d tag=%s ready_ms=%d cpu_ms=%d net_ms=%d show_ms=%d bytes=%d sleeps=%d%s",
		a.showSeq, time.Since(a.t0).Milliseconds(), updateKind(f), a.armed, f.Rect.Min.X, f.Rect.Min.Y, f.Rect.Dx(), f.Rect.Dy(),
		btoi(f.Full), c.ModeChanges, c.FullReqs, f.Tag, ready, cpu, net, showDur.Milliseconds(), f.Rect.Dx()*f.Rect.Dy()*bpp, sleeps, a.inputSuffix())
	if a.showSeq%25 == 0 {
		a.logf("%s", a.memLine())
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// present sends a region of the canvas to the display.
func (a *App) present(r image.Rectangle, mode display.Mode, full bool, tag string) {
	a.show(a.frame(r, mode, full, tag, a.layerActive, a.opaque))
}

// presentOpaque sends a region that is entirely UI drawn on the gray canvas
// (the overlay, the popover): it wins over the colour layer.
func (a *App) presentOpaque(r image.Rectangle, mode display.Mode, tag string) {
	a.show(a.frame(r, mode, false, tag, a.layerActive, []image.Rectangle{r}))
}

// uiMode is the waveform of the activity the app is in: the reader's turn
// waveform while a page is on screen (page turns, overlay, slider, loading
// marker all share it, so none of them flips the server's mode), the configured
// ui_mode for lists, settings and sign-in.
func (a *App) uiMode() display.Mode {
	if a.rd != nil && a.screen() == scrReader {
		return a.rd.wave
	}
	return display.ParseMode(a.Cfg.UIMode)
}

// clearScreen redraws everything and asks for a clearing refresh. With
// clear_style = content_full (default) that is CONTENT + UPDATE + REQUEST_FULL,
// followed at once by re-arming the activity's waveform: the server sleeps one
// second after each SET_REFRESH_MODE and REQUEST_FULL_REFRESH, and doing both
// here lets those sleeps overlap the panel's own flash instead of delaying the
// next page turn. With ui_full the update is sent in whatever mode is armed
// (no mode switch at all) followed by REQUEST_FULL_REFRESH.
func (a *App) clearScreen(tag string) {
	a.sinceFull = 0
	if a.Cfg.ClearStyle == "ui_full" {
		a.present(a.full(), display.ModeKeep, true, tag)
		return
	}
	a.present(a.full(), display.ParseMode(a.Cfg.ClearMode), true, tag)
	a.rearm()
}

// rearm arms the waveform of the current activity.
func (a *App) rearm() {
	if a.quitting {
		return
	}
	m := a.uiMode()
	if ar, ok := a.Disp.(display.Armer); ok {
		if err := ar.Arm(m); err != nil {
			a.logf("display error: %v", err)
			return
		}
	}
	a.armed = m
}

func (a *App) quit(reason string) {
	if !a.quitting {
		a.quitting = true
		a.reason = reason
	}
}

// async runs fn in a goroutine and delivers its result to the loop.
func (a *App) async(fn func() any) {
	a.pending++
	go func() {
		m := fn()
		select {
		case a.msgs <- done{m}:
		case <-a.ctx.Done():
		}
	}()
}

func (a *App) post(m any) {
	select {
	case a.msgs <- m:
	case <-a.ctx.Done():
	}
}

// Run is the event loop. It returns when the user closes the app, the
// connection to the panel drops, or ctx is cancelled (SIGTERM).
func (a *App) Run(ctx context.Context) string {
	a.ctx, a.cancel = context.WithCancel(ctx)
	defer a.cancel()
	a.sctx, a.scancel = context.WithCancel(a.ctx)
	mk := func() *time.Timer {
		t := time.NewTimer(time.Hour)
		t.Stop()
		return t
	}
	a.holdT, a.thumbT, a.syncT, a.ovT, a.repT, a.touchT, a.memT, a.clrT = mk(), mk(), mk(), mk(), mk(), mk(), mk(), mk()

	if a.Cfg.APIKey != "" && a.Cfg.Server != "" {
		a.startSession(a.Cfg.Server, a.Cfg.Username, a.Cfg.APIKey)
	} else {
		a.openSignin("")
		// The first frame is a clearing refresh: it wipes whatever AppLoad's
		// window showed before.
		a.drawSignin()
		a.clearScreen("start")
	}
	events := a.Disp.Events()
	if id, ok := a.Disp.(display.Idler); ok {
		id.Idle(0) // the first screen is up (the scripted test backend paces on it)
	}
	for !a.quitting {
		select {
		case <-a.ctx.Done():
			a.quit("signal")
		case ev, ok := <-events:
			if !ok {
				a.quit("display closed")
				break
			}
			a.consumed++
			a.onEvent(ev)
		case m := <-a.msgs:
			if d, ok := m.(done); ok {
				a.pending--
				m = d.m
			}
			a.onMsg(m)
		case <-a.holdT.C:
			a.onHold(a.holdGen)
		case <-a.thumbT.C:
			a.onThumbTick()
		case <-a.syncT.C:
			a.onSyncTimer()
		case <-a.ovT.C:
			a.onOverlayTimer()
		case <-a.repT.C:
			a.onRepeat()
		case <-a.touchT.C:
			a.onTouchStale(a.touchGen)
		case <-a.memT.C:
			a.onIdleMemory()
		case <-a.clrT.C:
			a.onClearTimer()
		}
		if !a.quitting {
			_ = a.Pos.FlushIfDue(20 * time.Second)
			if id, ok := a.Disp.(display.Idler); ok && a.pending == 0 {
				id.Idle(a.consumed)
			}
		}
	}
	a.shutdown()
	return a.reason
}

// shutdown saves state and makes a best-effort progress sync before exit.
func (a *App) shutdown() {
	a.flushSettings()
	if a.rd != nil {
		a.rd.savePos(a)
		a.syncNow(a.rd, 1500*time.Millisecond)
	}
	if err := a.Pos.Flush(); err != nil {
		a.logf("save positions: %v", err)
	}
	if cd, ok := a.Disp.(display.Counted); ok {
		c := cd.Counters()
		a.logf("display totals: updates=%d modechanges=%d fullrefresh=%d pixels=%d", c.Updates, c.ModeChanges, c.FullReqs, c.Pixels)
	}
	a.logf("%s", a.inputTotals())
}

func (a *App) top() *listView { return a.stack[len(a.stack)-1] }

func (a *App) pushList(lv *listView) {
	a.stack = append(a.stack, lv)
}

func (a *App) onMsg(m any) {
	switch v := m.(type) {
	case feedMsg:
		a.onFeed(v)
	case pageMsg:
		a.onPage(v)
	case thumbMsg:
		a.onThumb(v)
	case searchMsg:
		a.onSearch(v)
	case syncMsg:
		a.onSync(v)
	case nextMsg:
		a.onNext(v)
	case loginMsg:
		a.onLogin(v)
	case authMsg:
		a.onAuth(v)
	case prevMsg:
		a.onPrev(v)
	case warmFeedMsg:
		a.onWarmFeed(v)
	case warmPageMsg:
		a.onWarmPage(v)
	case func():
		v() // run on the UI goroutine (tests, diagnostics)
	}
}

// ---- session ---------------------------------------------------------------------

// startSession makes (server, user, key) the live session and opens the home
// list. The key is not written here: sign-in saves it, launch read it.
func (a *App) startSession(server, user, key string) {
	base, err := url.Parse(server)
	if err != nil || base.Host == "" {
		a.openSignin("The stored server address is not usable")
		a.drawSignin()
		a.clearScreen("start")
		return
	}
	if a.scancel != nil {
		a.scancel()
	}
	a.sctx, a.scancel = context.WithCancel(a.ctx)
	a.sess++
	a.Cl.Configure(base, key)
	a.Cfg.Server, a.Cfg.Username, a.Cfg.APIKey = server, user, key
	a.Pos.SetOwner(store.OwnerKey(server, user))
	a.rootPath = "/api/opds/" + key
	a.searchURL = ""
	a.thumbs, a.thumbOrd = map[string]*thumb{}, nil
	a.si, a.sv, a.kb, a.rd = nil, nil, nil, nil
	a.pop = popover{}
	home := &listView{title: "rmKavita", url: opds.RootURL(base, key), home: true, loading: true}
	a.stack = []*listView{home}
	a.loadList(home, false)
	a.hits = a.hits[:0]
	a.drawList(home)
	a.clearScreen("start")
}

// endSession forgets the key (not the positions) and shows the Sign-in screen.
func (a *App) endSession(notice string) {
	a.flushSettings()
	a.ovT.Stop()
	a.repT.Stop()
	if a.rd != nil {
		a.closeReaderState(true) // saves the position and tells Kavita where we were, while the key is still valid
	}
	if a.scancel != nil {
		a.scancel()
	}
	a.sctx, a.scancel = context.WithCancel(a.ctx)
	a.sess++
	a.kb, a.sv, a.rd = nil, nil, nil
	a.pop = popover{}
	a.stack = nil
	a.thumbs, a.thumbOrd = map[string]*thumb{}, nil
	a.searchURL = ""
	_ = a.Pos.Flush()
	a.Pos.SetOwner("")
	if a.confPath != "" {
		if err := store.ClearKey(a.confPath); err != nil {
			a.logf("could not remove the stored key: %v", err)
		}
	}
	a.Cl.Clear()
	a.Cfg.APIKey = ""
	a.logf("signed out")
	a.openSignin(notice)
	a.drawSignin()
	a.clearScreen("signed-out")
}

// logout is the user's Logout.
func (a *App) logout(notice string) { a.endSession(notice) }

// confirmAuth runs after a foreground request was refused with 401/403: it
// asks Kavita once more with a fresh request, and only a second refusal signs
// the user out. (A single 401 from a background cover or prefetch never does.)
func (a *App) confirmAuth() {
	if a.rechecking || !a.Cl.Signed() {
		return
	}
	a.rechecking = true
	sess := a.sess
	sctx := a.sctx
	a.async(func() any {
		ctx, cancel := context.WithTimeout(sctx, 15*time.Second)
		defer cancel()
		return authMsg{sess: sess, err: a.Cl.Recheck(ctx)}
	})
}

func (a *App) onAuth(m authMsg) {
	a.rechecking = false
	if m.sess != a.sess {
		return
	}
	if m.err != nil && m.err.Kind == opds.KindAuth {
		a.endSession("Kavita no longer accepts this sign-in. Sign in again.")
	}
}

// ---- image preparation ----------------------------------------------------------

// pageSpec is what a page is prepared for. It is captured when a fetch starts;
// a page that arrives for an older spec is dropped.
type pageSpec struct {
	fit    imgproc.FitMode
	colour bool
	kernel imgproc.Kernel
	gen    int
	crop   bool // trim blank margins
	border bool // measure the border colour (Border: Match page)
	rotate bool // legacy rotate_spreads
	boxH   int  // height of the area pages are fitted into (the screen less the footer)

	levels int            // speed mode: grey levels (0 = the page is left as it is)
	dither imgproc.Dither // speed mode: how the reduction is dithered
}

// prepare decodes page bytes and fits them to the screen.
func (a *App) prepare(data []byte, sp pageSpec) (*imgproc.Fitted, error) {
	defer a.acquireDecode(data)()
	pl, err := imgproc.DecodePlanes(data, sp.colour)
	if err != nil {
		return nil, err
	}
	b := pl.Bounds()
	if sp.rotate && b.Dx() > b.Dy()*115/100 {
		pl = pl.Rotate90()
		b = pl.Bounds()
	}
	full := pl.Y // the uncropped luminance: the border sampler reads the page's own edges
	if sp.crop {
		if r := imgproc.CropBox(pl.Y, imgproc.DefaultCrop); r != b {
			pl = pl.Crop(r)
			b = pl.Bounds()
		}
	}
	boxH := sp.boxH
	if boxH <= 0 {
		boxH = a.H
	}
	dw, dh := imgproc.FitDims(b.Dx(), b.Dy(), a.W, boxH, sp.fit)
	var level uint8
	var levelOK bool
	if sp.border {
		if sides := imgproc.BorderSides(dw, dh, a.W, boxH); sides != 0 {
			level, levelOK = imgproc.BorderLevel(full, sides, imgproc.DefaultBorder)
			if levelOK && sp.levels > 0 {
				level = imgproc.SnapLevel(level, sp.levels)
			}
		}
	}
	f := imgproc.FitPlanes(pl, dw, dh, sp.kernel, a.Cfg.Gamma)
	f.Border, f.BorderOK = level, levelOK
	if sp.levels > 0 && f.Col == nil {
		imgproc.Quantise(f.Gray, f.W, f.H, sp.levels, sp.dither)
	}
	return f, nil
}

func errOf(err error) *opds.Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*opds.Error); ok {
		return e
	}
	return &opds.Error{Kind: opds.KindData, Msg: err.Error()}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
