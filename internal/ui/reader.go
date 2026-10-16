package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/imgproc"
	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

type slot struct {
	id     int // guards against a late answer for an evicted-and-recreated slot
	state  int // 0 queued, 1 loading, 2 ready, 3 failed
	img    *imgproc.Fitted
	err    *opds.Error
	cancel context.CancelFunc
}

type reader struct {
	title       string
	ids         opds.IDs
	st          opds.Stream
	doc         opds.Doc
	count       int
	unknown     bool // the feed gave no page count
	page        int  // the page at the top of the viewport
	off         int  // pixels scrolled along the page's scroll axis (reading-direction coordinates)
	startPM     int  // pending per-mille offset for `page` (-1 none)
	toEnd       bool // pending: show the end of `page` when it arrives (came back from the next)
	slots       map[int]*slot
	peakHeld    int  // most decoded pages held at once in this chapter (for the RAM bound test)
	nextLoading bool // the series feed is being fetched to find the next chapter
	overlay     bool
	lastReq     int // page most recently requested from the server
	from        *listView
	fromIdx     int
	shown       int  // page currently on screen, -1 none
	first       bool // nothing drawn yet: the first frame is a clearing refresh
	need        bool // a redraw is waiting for pages
	gen         int  // bumped when a setting changes how pages are fitted

	wave       display.Mode // the turn waveform of this chapter
	border     uint8        // luminance of the margin around the page (Border: Match page)
	borderPage int          // page the border was last taken from, -1 none
	warm       *warmPage    // the next chapter's first page, fetched ahead (once)
	seriesLV   *listView    // the series feed fetched by the warm-up (opened from Continue)
}

// ---- effective settings of a reader ---------------------------------------------

func (a *App) prefsOf(r *reader) store.Prefs {
	if r.doc == opds.DocPDF {
		return a.Set.PDF
	}
	return a.Set.Comic
}

func (a *App) fitOf(r *reader) imgproc.FitMode { return a.fitForDoc(r.doc) }

func (a *App) fitForDoc(doc opds.Doc) imgproc.FitMode {
	switch a.prefsFor(doc).Fit {
	case store.FitWidth:
		return imgproc.FitWidth
	case store.FitHeight:
		return imgproc.FitHeight
	}
	return imgproc.FitContain
}

// rtlOf: only comics read right to left; a PDF never does.
func (a *App) rtlOf(r *reader) bool { return r.doc != opds.DocPDF && a.Set.RTL }

func (a *App) specOf(r *reader) pageSpec {
	sp := a.specFor(r.doc)
	sp.gen = r.gen
	return sp
}

// specFor is what pages of a document kind are prepared for under the current
// settings (gen is the reader's, filled in by specOf).
func (a *App) specFor(doc opds.Doc) pageSpec {
	k := imgproc.CatmullRom
	if doc == opds.DocPDF {
		k = imgproc.Lanczos3 // text pages: a little sharper
	}
	switch a.Cfg.Kernel {
	case "catmull":
		k = imgproc.CatmullRom
	case "lanczos":
		k = imgproc.Lanczos3
	case "smooth":
		k = imgproc.Smooth
	}
	sp := pageSpec{fit: a.fitForDoc(doc), colour: a.colourOn(), kernel: k,
		crop:   a.Cfg.Crop == "trim",
		border: a.Cfg.Border == "match" && doc != opds.DocPDF,
		rotate: a.Cfg.RotateSpread, boxH: a.vh()}
	if a.speedMode() {
		// the page is rendered in grey (no chroma decode, no colour layer) and
		// reduced to a few grey levels while it is prepared, not when it is shown
		sp.levels = a.Cfg.SpeedLevels
		sp.dither = imgproc.ParseDither(a.Cfg.SpeedDither)
	}
	return sp
}

func (r *reader) kindName() string {
	if r.doc == opds.DocPDF {
		return "pdf"
	}
	return ""
}

// ready returns the fitted page n when it is decoded.
func (r *reader) ready(n int) *imgproc.Fitted {
	if s := r.slots[n]; s != nil && s.state == 2 {
		return s.img
	}
	return nil
}

func (r *reader) savePos(a *App) {
	if r.unknown || r.ids.Chapter == 0 {
		return
	}
	y := 0
	if f := r.ready(r.page); f != nil && r.off > 0 {
		ext, _, _ := a.scrollGeom(f)
		if ext > 0 {
			y = r.off * 1000 / ext
		}
	}
	a.Pos.Set(store.Pos{Library: r.ids.Library, Series: r.ids.Series, Volume: r.ids.Volume,
		Chapter: r.ids.Chapter, Title: r.title, Page: r.page, Count: r.count, Y: y, Kind: r.kindName()})
}

// scrollGeom reports how a fitted page scrolls in page-by-page mode: its extent
// along the scroll axis and the viewport size (extent <= view means no scroll).
// vertical is true for a page taller than the screen.
func (a *App) scrollGeom(f *imgproc.Fitted) (ext, view int, vertical bool) {
	switch {
	case f.H > a.vh():
		return f.H, a.vh(), true
	case f.W > a.W:
		return f.W, a.W, false
	}
	return 0, 0, true
}

// startPage decides where to open a chapter: the local position, unless the
// server has moved clearly past it (read on another device). A chapter the
// server has finished starts over.
func (a *App) startPage(ids opds.IDs, st *opds.Stream) int {
	count := st.Count
	local, hasLocal := a.Pos.Get(ids.Chapter)
	server := st.LastRead
	if count > 0 && server >= count {
		// "One past the end" is how this app (and Kavita's reader) marks a
		// chapter finished: opening it again must not land on its last page.
		return 0
	}
	pick := 0
	switch {
	case hasLocal && (server < 0 || server <= local.Page+a.Cfg.ReadAhead+1):
		pick = local.Page
	case server >= 0:
		pick = server
	case hasLocal:
		pick = local.Page
	}
	if count > 0 && pick >= count {
		pick = count - 1
	}
	if pick < 0 {
		pick = 0
	}
	return pick
}

func (a *App) openReader(lv *listView, idx int) {
	row := lv.rows[idx]
	if row.stream == nil {
		return
	}
	st := *row.stream
	r := &reader{title: chapterTitle(row.title), ids: row.ids, st: st, doc: row.doc, count: st.Count, from: lv, fromIdx: idx,
		slots: map[int]*slot{}, shown: -1, lastReq: -1, first: true, startPM: -1,
		wave: a.baseWave(), border: paperGray, borderPage: -1}
	if r.count <= 0 {
		r.count, r.unknown = 9999, true
	}
	r.page = 0
	if !r.unknown {
		r.page = a.startPage(row.ids, &st)
		if p, ok := a.Pos.Get(row.ids.Chapter); ok && p.Page == r.page && p.Y > 0 {
			r.startPM = p.Y
		}
	}
	a.rd = r
	a.pop = popover{}
	a.logf("open chapter %d page %d/%d", r.ids.Chapter, r.page+1, r.count)
	a.installWarm(r, row)
	a.rdWindow()
	a.readerRedraw("first")
}

// closeReaderState saves the position, cancels the page loads and tells Kavita
// where the reader stopped. It does not draw. With sync the progress is sent
// before returning (sign-out needs the key still valid).
func (a *App) closeReaderState(sync bool) {
	r := a.rd
	if r == nil {
		return
	}
	a.ovT.Stop()
	r.savePos(a)
	for _, s := range r.slots {
		if s.cancel != nil {
			s.cancel()
		}
	}
	r.dropWarm()
	if sync {
		a.syncNow(r, 1500*time.Millisecond)
	} else {
		a.syncSoon(r)
	}
	a.rd = nil
	_ = a.Pos.Flush()
}

func (a *App) closeReader() {
	if a.rd == nil {
		return
	}
	a.closePopover()
	a.closeReaderState(false)
	lv := a.top()
	a.refreshContinue(lv)
	a.hits = a.hits[:0]
	a.drawList(lv)
	a.clearScreen("reader-close")
	a.wantThumbs(lv)
}

// ---- page pipeline ------------------------------------------------------------

// window lists the pages worth holding, most useful first: the current page,
// the next one, the ones just behind it (going back is the second most likely
// move), the rest of the read-ahead. When the byte budget or the memory guard
// says so, the tail of this list is what goes first: far-ahead pages, then
// the farthest pages behind. The current page is never dropped.
func (a *App) window(r *reader) []int {
	ahead, behind := a.Cfg.ReadAhead, a.Cfg.KeepBehind
	switch {
	case a.belowFloor():
		ahead, behind = 0, 0 // nothing but the page on screen
	case a.lowMem():
		ahead = min(ahead, 1) // shrink the read-ahead first ...
		behind = min(behind, 1)
	}
	var order []int
	order = append(order, r.page)
	if ahead >= 1 {
		order = append(order, r.page+1)
	}
	for i := 1; i <= behind; i++ {
		order = append(order, r.page-i)
	}
	for i := 2; i <= ahead; i++ {
		order = append(order, r.page+i)
	}
	return order
}

// cacheBudgetBytes is the byte budget of the fitted pages held in RAM.
func (a *App) cacheBudgetBytes() int {
	mb := a.Cfg.CacheMB
	if mb <= 0 {
		mb = 96
	}
	return mb << 20
}

// pageBytesGuess is what a page that is not decoded yet will take: a fitted
// grey page is one byte a pixel, a colour page two (RGB565 is what is stored).
func (a *App) pageBytesGuess() int {
	g := a.W * a.vh()
	if a.colourOn() {
		g *= 2
	}
	return g
}

// cacheBytes is what the held pages occupy now.
func (r *reader) cacheBytes() int { return r.heldBytes() }

func (a *App) rdWindow() {
	r := a.rd
	guess := a.pageBytesGuess()
	budget := a.cacheBudgetBytes()
	keep := map[int]bool{}
	used := 0
	var order []int
	for _, n := range a.window(r) {
		if n < 0 || n >= r.count || keep[n] {
			continue
		}
		sz := guess
		if f := r.ready(n); f != nil {
			sz = f.Bytes()
		}
		if n != r.page && used+sz > budget {
			continue // over the byte budget: this page is not held
		}
		used += sz
		keep[n] = true
		order = append(order, n)
	}
	for n, s := range r.slots {
		if !keep[n] {
			if s.cancel != nil {
				s.cancel()
			}
			delete(r.slots, n)
		}
	}
	for _, n := range order {
		s := r.slots[n]
		if s == nil || (s.state == 3 && n == r.page) {
			// a failed prefetch is retried only once it becomes the current page
			a.slotSeq++
			r.slots[n] = &slot{id: a.slotSeq, state: 0}
		}
	}
	a.dispatch()
	a.maybeWarm(r)
}

// lowMem reports MemAvailable under the guard threshold (mem_guard_mb, 0 =
// off): the read-ahead shrinks and nothing new is warmed up. One log line per
// transition.
func (a *App) lowMem() bool {
	if a.Cfg.MemGuardMB <= 0 || a.memAvail == nil {
		return false
	}
	m := a.memAvail()
	if m >= 0 && m < a.Cfg.MemGuardMB {
		if !a.memWarned {
			a.memWarned = true
			a.logf("memory guard: %d MB available (< %d): the read-ahead is reduced", m, a.Cfg.MemGuardMB)
		}
		return true
	}
	if a.memWarned {
		a.memWarned = false
		a.logf("memory guard: %d MB available again: the read-ahead is back", m)
	}
	return false
}

// belowFloor reports MemAvailable under the floor (mem_floor_mb, 0 = off): no
// new decode buffer is allocated, even for the current page (it shows the error
// state with Retry instead of risking the out-of-memory killer, which would
// take xochitl with it).
func (a *App) belowFloor() bool {
	if a.Cfg.MemFloorMB <= 0 || a.memAvail == nil {
		return false
	}
	m := a.memAvail()
	if m >= 0 && m < a.Cfg.MemFloorMB {
		if !a.floorWarned {
			a.floorWarned = true
			a.logf("memory floor: %d MB available (< %d): no new page is decoded until memory frees up", m, a.Cfg.MemFloorMB)
		}
		return true
	}
	if a.floorWarned {
		a.floorWarned = false
		a.logf("memory floor: %d MB available again: decoding resumes", m)
	}
	return false
}

// maxFetch bounds simultaneous page downloads. Each one is a connection
// through a forwarder (one process per connection), and the current page must not
// share its bandwidth with more than one prefetch... two at a time is the
// balance: the current page and the next one.
const maxFetch = 2

// dispatch starts queued pages, nearest to the current page first (pages
// behind the reader come last).
func (a *App) dispatch() {
	r := a.rd
	if r == nil {
		return
	}
	loading := 0
	for _, s := range r.slots {
		if s.state == 1 {
			loading++
		}
	}
	for loading < maxFetch {
		best, bestPri := -1, 1<<30
		for n, s := range r.slots {
			if s.state != 0 {
				continue
			}
			pri := n - r.page
			if pri < 0 {
				pri = 1000 - pri // pages behind the reader come last
			}
			if pri < bestPri {
				best, bestPri = n, pri
			}
		}
		if best < 0 {
			return
		}
		if a.belowFloor() {
			// no new decode buffer: the page on screen shows the error state (Retry
			// tries again), a read-ahead page simply waits
			if best == r.page {
				s := r.slots[best]
				s.state, s.err = 3, &opds.Error{Kind: opds.KindData, Msg: "Not enough free memory to open this page"}
				a.readerRedraw("ui")
			}
			return
		}
		a.startLoad(r, best)
		loading++
	}
}

func (a *App) startLoad(r *reader, n int) {
	ctx, cancel := context.WithCancel(a.sctx)
	s := r.slots[n]
	s.state, s.cancel = 1, cancel
	id := s.id
	r.lastReq = n
	url := r.st.PageURL(n, a.W, a.H)
	sp := a.specOf(r)
	a.async(func() any {
		ctx2, c2 := context.WithTimeout(ctx, 60*time.Second)
		defer c2()
		t0 := time.Now()
		data, err := a.Cl.GetImage(ctx2, url)
		net := time.Since(t0)
		if err != nil {
			return pageMsg{r: r, n: n, id: id, gen: sp.gen, err: errOf(err), net: net}
		}
		if ctx.Err() != nil {
			return pageMsg{r: r, n: n, id: id, gen: sp.gen, err: &opds.Error{Kind: opds.KindNetwork, Msg: "cancelled"}}
		}
		t1 := time.Now()
		img, err := a.prepare(data, sp)
		cpu := time.Since(t1)
		if err != nil {
			return pageMsg{r: r, n: n, id: id, gen: sp.gen, err: &opds.Error{Kind: opds.KindData, Msg: err.Error()}}
		}
		return pageMsg{r: r, n: n, id: id, gen: sp.gen, img: img, net: net, cpu: cpu}
	})
}

func (a *App) onPage(m pageMsg) {
	r := m.r
	if a.rd != r {
		return
	}
	s := r.slots[m.n]
	if s == nil || s.state != 1 || s.id != m.id || m.gen != r.gen {
		a.dispatch()
		return // evicted while loading, or fitted for settings that no longer apply
	}
	defer a.dispatch()
	a.noteDecoded()
	if m.err != nil {
		if m.err.Msg == "cancelled" {
			delete(r.slots, m.n)
			return
		}
		if m.err.Kind == opds.KindAuth && m.n == r.page {
			a.confirmAuth() // a background 401 alone never signs anyone out
		}
		// A page past the end: the feed did not tell us the count.
		if r.unknown && m.n > 0 && (m.err.Status == 400 || m.err.Status == 404) {
			r.count = m.n
			delete(r.slots, m.n)
			if r.page >= r.count {
				r.page = r.count - 1
				r.off = 0
				a.rdWindow()
				a.readerRedraw("turn")
			}
			return
		}
		s.state, s.err = 3, m.err
		a.logf("page %d failed: %s", m.n, m.err.Error())
		if m.n == r.page {
			a.readerRedraw("ui")
		}
		return
	}
	s.state, s.img = 2, m.img
	if m.n == r.page && (r.need || r.first || r.shown != r.page) {
		// the page on screen was waited for: what it cost goes into the telemetry
		// line of the update that shows it
		a.waitNet, a.waitCPU = m.net, m.cpu
	}
	if n := r.held(); n > r.peakHeld {
		r.peakHeld = n
	}
	if a.rd != r || a.screen() != scrReader {
		return
	}
	a.settle(r)
	if a.viewportReady(r) && (r.need || r.shown != r.page || r.first) {
		r.need = false
		if r.first {
			a.readerRedraw("first")
		} else {
			a.readerRedraw("turn")
		}
	}
}

// settle applies a pending offset (restored position, or "end of the page")
// once the page it belongs to is decoded.
func (a *App) settle(r *reader) {
	f := r.ready(r.page)
	if f == nil || (r.startPM < 0 && !r.toEnd) {
		return
	}
	if ext, view, _ := a.scrollGeom(f); ext > view {
		starts := segStarts(ext, view)
		switch {
		case r.toEnd:
			r.off = starts[len(starts)-1]
		case r.startPM > 0:
			r.off = starts[segIndexFor(starts, r.startPM*ext/1000)]
		}
	}
	r.startPM, r.toEnd = -1, false
}

// viewportReady reports whether every page the viewport shows is decoded.
func (a *App) viewportReady(r *reader) bool {
	if a.errPage(r) >= 0 {
		return false
	}
	return r.ready(r.page) != nil
}

// ---- navigation ----------------------------------------------------------------

// readerStep is one tap or swipe forward (dir > 0) or back: it scrolls within a
// page that does not fit, or turns the page.
func (a *App) readerStep(dir int) {
	r := a.rd
	if r == nil {
		return
	}
	if f := r.ready(r.page); f != nil {
		if ext, view, _ := a.scrollGeom(f); ext > view {
			starts := segStarts(ext, view)
			i := segIndexFor(starts, r.off)
			switch {
			case dir > 0 && i < len(starts)-1:
				r.off = starts[i+1]
				a.afterMove()
				return
			case dir < 0 && i > 0:
				r.off = starts[i-1]
				a.afterMove()
				return
			}
		}
	}
	if dir > 0 {
		a.readerGo(1)
	} else {
		a.readerGo(-1)
	}
}

// afterMove runs after the position changed: the overlay goes away, the
// position is remembered, pages are (pre)fetched and the screen is redrawn.
func (a *App) afterMove() {
	r := a.rd
	r.overlay = false
	a.ovT.Stop()
	a.pop = popover{}
	r.savePos(a)
	a.rdWindow()
	a.syncT.Reset(a.syncDelay)
	a.settle(r)
	if a.viewportReady(r) {
		r.need = false
		a.readerRedraw("turn")
		return
	}
	r.need = true
	a.readerRedraw("loading")
}

// readerGo moves by whole pages (the slider, the ends of a page).
func (a *App) readerGo(delta int) {
	r := a.rd
	if r == nil {
		return
	}
	np := r.page + delta
	if np >= r.count {
		a.nextChapter()
		return
	}
	if np < 0 {
		return
	}
	a.moveToPage(np, delta < 0) // coming back from the next page: land at the end of this one
}

func (a *App) readerJump(np int) { a.moveToPage(np, false) }

// moveToPage shows page np from its start (or from its end, when stepping back
// into it) as one screen update.
func (a *App) moveToPage(np int, toEnd bool) {
	r := a.rd
	if np < 0 {
		np = 0
	}
	if np >= r.count {
		np = r.count - 1
	}
	if np == r.page {
		return
	}
	r.page, r.off, r.startPM, r.toEnd = np, 0, -1, toEnd
	a.afterMove()
}

// chapterAfter finds the chapter that follows `chapter` in a chapter list. The
// current chapter is located by its id, not by the row it was opened from:
// Kavita's "Continue Reading from" copy sits at the top of the list, ahead of
// chapters that come before the one it stands for. found reports whether the
// chapter is in the list at all.
func chapterAfter(lv *listView, chapter, hint int) (idx int, found bool) {
	if lv == nil {
		return 0, false
	}
	base := -1
	for i, x := range lv.rows {
		if x.kind == rowChapter && !x.shortcut && x.ids.Chapter == chapter {
			base = i
			break
		}
	}
	if base < 0 {
		return 0, false
	}
	for i := base + 1; i < len(lv.rows); i++ {
		x := lv.rows[i]
		if x.kind == rowChapter && !x.shortcut && x.stream != nil && x.ids.Chapter != chapter {
			return i, true
		}
	}
	return 0, true
}

// advanceTo leaves the finished chapter and opens row i of lv.
func (a *App) advanceTo(r *reader, lv *listView, i int) {
	r.page = r.count - 1
	r.savePos(a)
	for _, s := range r.slots {
		if s.cancel != nil {
			s.cancel()
		}
	}
	a.syncSoon(r)
	a.rd = nil
	a.ovT.Stop()
	a.handOver = r.warm // openReader takes it when it is the chapter being opened
	a.openReader(lv, i)
	a.handOver = nil
}

// nextChapter runs when the reader turns past the last page. With a chapter
// list behind it (the series screen) it opens the next row; when it was
// opened from the home "Continue" row there is no list, so the series feed is
// fetched first. At the end of the series it returns to the list.
func (a *App) nextChapter() {
	r := a.rd
	if r.nextLoading {
		return
	}
	idx, found := chapterAfter(r.from, r.ids.Chapter, r.fromIdx)
	if !found && r.seriesLV != nil {
		if i2, f2 := chapterAfter(r.seriesLV, r.ids.Chapter, 0); f2 {
			r.from, idx, found = r.seriesLV, i2, true // fetched ahead by the warm-up
		}
	}
	switch {
	case found && idx > 0:
		a.advanceTo(r, r.from, idx)
		return
	case found:
		a.closeReader() // last chapter of the series
		return
	case r.ids.Series == 0 || r.unknown:
		a.closeReader()
		return
	}
	r.nextLoading = true
	a.readerRedraw("loading")
	url := a.resolve(fmt.Sprintf("%s/series/%d", strings.TrimRight(a.rootPath, "/"), r.ids.Series), "")
	sctx := a.sctx
	a.async(func() any {
		ctx, cancel := context.WithTimeout(sctx, 30*time.Second)
		defer cancel()
		f, err := a.Cl.GetFeed(ctx, url)
		if err != nil {
			return nextMsg{r: r, err: errOf(err)}
		}
		rows, _ := a.rowsFromFeed(f, url)
		return nextMsg{r: r, lv: &listView{title: f.Title, url: url, rows: rows}}
	})
}

func (a *App) onNext(m nextMsg) {
	r := m.r
	if a.rd != r {
		return
	}
	r.nextLoading = false
	if m.err != nil {
		a.logf("next chapter: %s", m.err.Error())
		a.closeReader()
		return
	}
	idx, found := chapterAfter(m.lv, r.ids.Chapter, 0)
	if !found || idx == 0 {
		a.closeReader()
		return
	}
	a.advanceTo(r, m.lv, idx)
}

// readerTapZone: three columns. The left and the right column turn pages
// (direction per setting), the middle
// column opens the top and the bottom bar; a tap anywhere while they are open
// closes them.
func (a *App) readerTapZone(x, y int) {
	r := a.rd
	if r.overlay {
		a.hideOverlay()
		return
	}
	if a.errPage(r) >= 0 {
		return
	}
	switch zoneAt(x, y, a.W, a.H, a.Cfg.ZoneMiddle) {
	case zoneReveal:
		a.showOverlay()
	case zoneLeft:
		if a.rtlOf(r) {
			a.readerStep(1) // right to left: the left column goes forward
		} else {
			a.readerStep(-1)
		}
	case zoneRight:
		if a.rtlOf(r) {
			a.readerStep(-1)
		} else {
			a.readerStep(1)
		}
	}
}

// readerSwipe: a downward swipe from the top band reveals the controls; a
// vertical swipe scrolls (up = forward); a sideways swipe turns the page.
func (a *App) readerSwipe(sx, sy int, fromTop bool) {
	r := a.rd
	if a.errPage(r) >= 0 {
		return
	}
	if fromTop && sy > 0 {
		if !r.overlay {
			a.showOverlay()
		}
		return
	}
	if r.overlay {
		return
	}
	if sy != 0 {
		a.readerStep(-sy) // finger moving up (sy < 0) = forward
		return
	}
	if sx != 0 {
		// Right to left: the finger moving right pulls the page edge = forward.
		fwd := sx > 0
		if !a.rtlOf(r) {
			fwd = sx < 0
		}
		if fwd {
			a.readerStep(1)
		} else {
			a.readerStep(-1)
		}
	}
}

// ---- progress sync ------------------------------------------------------------

// Kavita saves reading progress whenever a page is streamed, so prefetching
// leaves its progress a page or two ahead. After a pause (and on exit) the
// current page is requested again, headers only, to put it right.
func (a *App) syncTarget(r *reader) int {
	// Kavita marks a chapter read only when the page number reaches the page
	// count; on the last page we report "one past the end" (it serves the last
	// page image again and records the chapter as finished).
	if r.page >= r.count-1 {
		return r.count
	}
	return r.page
}

func (a *App) syncSoon(r *reader) {
	if !a.Cfg.SyncProgress || r == nil || r.unknown || r.lastReq < 0 {
		return
	}
	want := a.syncTarget(r)
	if r.lastReq == want {
		return
	}
	r.lastReq = want
	url := r.st.PageURL(want, a.W, a.H)
	a.async(func() any {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()
		_ = a.Cl.Touch(ctx, url)
		return syncMsg{r: r, page: want}
	})
}

func (a *App) syncNow(r *reader, timeout time.Duration) {
	if !a.Cfg.SyncProgress || r == nil || r.unknown || r.lastReq < 0 {
		return
	}
	want := a.syncTarget(r)
	if r.lastReq == want {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = a.Cl.Touch(ctx, r.st.PageURL(want, a.W, a.H))
	r.lastReq = want
}

func (a *App) onSyncTimer() {
	if a.rd != nil {
		a.syncSoon(a.rd)
	}
}

func (a *App) onSync(m syncMsg) {}

// held counts the decoded pages currently in RAM.
func (r *reader) held() int {
	n := 0
	for _, s := range r.slots {
		if s.img != nil {
			n++
		}
	}
	return n
}

// heldBytes is the memory those pages occupy.
func (r *reader) heldBytes() int {
	n := 0
	for _, s := range r.slots {
		if s.img != nil {
			n += s.img.Bytes()
		}
	}
	return n
}

var reContinue = regexp.MustCompile(`(?i)^\s*continue(\s+reading)?\s+from\s*:?\s*`)

// chapterTitle is the title a chapter is remembered and shown by in the
// reader: Kavita's "Continue Reading from: " lead-in is not part of the name.
func chapterTitle(t string) string {
	if s := reContinue.ReplaceAllString(t, ""); s != "" {
		return s
	}
	return t
}

// ---- page-turn waveform -----------------------------------------------------------

// baseWave is the turn waveform of a chapter: the configured page_mode. "auto"
// is UI (the maintainer's panel preference: subjectively the fastest, and it keeps
// colour); fast and content stay selectable.
func (a *App) baseWave() display.Mode {
	switch a.Cfg.Render {
	case "quality":
		return display.ModeContent
	}
	switch a.Cfg.PageMode {
	case "fast", "content", "animate":
		return display.ParseMode(a.Cfg.PageMode)
	}
	return display.ModeUI
}

// speedMode reports whether render = speed.
func (a *App) speedMode() bool { return a.Cfg.Render == "speed" }

// colourOn reports whether pages are decoded and shown in colour: the Colour
// setting, except in speed mode, which is black and white. The setting itself
// is never changed by the mode (so leaving Speed brings the user's choice back
// exactly, also after a restart); every reader of "is colour on" asks this.
func (a *App) colourOn() bool { return a.Set.Colour && !a.speedMode() }

// readerMode is the waveform of the reader's own non-turn frames (the loading
// marker, a bar going away): ui_mode as in every release before, except in
// speed mode, where a different mode would be a message and a second of
// waiting for it.
func (a *App) readerMode() display.Mode {
	if a.speedMode() {
		return a.baseWave()
	}
	return a.uiMode()
}

// turnWave is the waveform of an ordinary page turn. It is the same for black
// and white and for colour pages, so a chapter that mixes the two never flips
// the server's refresh mode between turns.
func (a *App) turnWave(r *reader, colour bool) display.Mode {
	r.wave = a.baseWave()
	return r.wave
}

// refreshEvery is the number of turns between clearing refreshes of black and
// white pages (0 = never).
func (a *App) refreshEvery() int { return a.Set.RefreshEvery() }

// refreshEveryFor is the interval that applies to the page just drawn: colour
// pages ghost more, so they have their own (default every 5).
func (a *App) refreshEveryFor(colour bool) int {
	if colour {
		return a.Set.ColourEveryN()
	}
	return a.Set.RefreshEvery()
}
