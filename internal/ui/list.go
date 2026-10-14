package ui

import (
	"context"
	"fmt"
	"image"
	"strings"
	"time"

	"rmkavita/internal/imgproc"
	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

type thumb struct {
	img   *image.Gray
	state int  // 1 loading, 2 ready, 3 failed
	shown bool // the picture has been drawn on screen (a late cover repaints only its own row)
}

type rowKind int

const (
	rowFolder rowKind = iota
	rowChapter
	rowContinue
)

type row struct {
	kind     rowKind
	title    string
	sub      string
	prog     opds.Progress
	href     string // folder: absolute feed URL
	stream   *opds.Stream
	ids      opds.IDs
	thumb    string // absolute cover URL
	shortcut bool   // Kavita's "Continue from" duplicate of a later row
	read     int    // pages read per the server, -1 unknown
	doc      opds.Doc
}

type listView struct {
	title   string
	url     string
	rows    []row
	next    string
	total   int
	loading bool
	err     *opds.Error
	scroll  int
	home    bool
	libs    bool
	jump    bool // the user asked for the next page: scroll to it once it arrives
	// nextFailed: a background read-ahead of the next page failed; stop trying
	// until the user asks for the next page.
	nextFailed bool
	// shownLoading: the footer on screen currently says "Loading".
	shownLoading bool
	hidden       int // entries of this list that are not shown (no page stream)
}

// ---- geometry ---------------------------------------------------------------

func (a *App) footerH() int    { return a.u(190) }
func (a *App) rowH() int       { return a.u(170) }
func (a *App) listTop() int    { return a.barRect().Max.Y }
func (a *App) listBottom() int { return a.H - a.footerH() }
func (a *App) rowsPerPage() int {
	n := (a.listBottom() - a.listTop()) / a.rowH()
	if n < 1 {
		n = 1
	}
	return n
}

// ---- loading ----------------------------------------------------------------

func (a *App) loadList(lv *listView, appendNext bool) {
	target := lv.url
	if appendNext {
		target = lv.next
	}
	if target == "" {
		return
	}
	lv.loading = true
	lv.err = nil
	lv.nextFailed = false
	sctx := a.sctx
	a.async(func() any {
		ctx, cancel := context.WithTimeout(sctx, 45*time.Second)
		defer cancel()
		f, err := a.Cl.GetFeed(ctx, target)
		return feedMsg{lv: lv, feed: f, err: errOf(err), appnd: appendNext, reqURL: target}
	})
}

func (a *App) inStack(lv *listView) bool {
	for _, s := range a.stack {
		if s == lv {
			return true
		}
	}
	return false
}

func (a *App) resolve(href, from string) string {
	if href == "" {
		return ""
	}
	u, err := a.Cl.Resolve(href, from)
	if err != nil {
		return ""
	}
	return u
}

// rowsFromFeed turns a catalogue page into rows. Entries without a page stream
// (and series that are not made of images) are not listed at all; hidden says
// how many were left out.
func (a *App) rowsFromFeed(f *opds.Feed, from string) (rows []row, hidden int) {
	for _, e := range f.Entries {
		title, prog := opds.SplitProgress(e.Title)
		if title == "" {
			title = "(untitled)"
		}
		thumbHref := e.ThumbHref()
		if strings.Contains(thumbHref, "library-cover") {
			thumbHref = "" // Kavita has no cover for a library: asking only produces a 404 per row
		}
		r := row{title: title, prog: prog, read: -1, thumb: a.resolve(thumbHref, from)}
		switch st := e.Stream(); {
		case st != nil:
			r.kind = rowChapter
			cp := *st
			cp.Template = a.resolve(st.Template, from)
			r.stream = &cp
			r.read = st.LastRead
			r.doc = e.DocKind()
			if ids, ok := opds.ParseIDs(st.Template); ok {
				r.ids = ids
			}
			if st.Count > 0 {
				r.sub = plural(st.Count, "page", "pages")
			}
		case e.NavHref() != "":
			if !e.ImageFormat() {
				hidden++
				continue
			}
			r.kind = rowFolder
			r.href = a.resolve(e.NavHref(), from)
		default:
			hidden++
			continue
		}
		rows = append(rows, r)
	}
	// Kavita lists a "Continue from ..." copy of a chapter ahead of the chapter
	// itself. The copy has no lastRead, so it borrows the real row's.
	seen := map[int]int{}
	for i := len(rows) - 1; i >= 0; i-- {
		c := rows[i].ids.Chapter
		if rows[i].kind != rowChapter || c == 0 {
			continue
		}
		if j, dup := seen[c]; dup {
			rows[i].shortcut = true
			if rows[i].read < 0 && rows[j].read >= 0 {
				rows[i].read = rows[j].read
				if rows[i].stream != nil {
					rows[i].stream.LastRead = rows[j].read
				}
			}
		} else {
			seen[c] = i
		}
	}
	return rows, hidden
}

func (a *App) onFeed(m feedMsg) {
	lv := m.lv
	if !a.inStack(lv) {
		return
	}
	lv.loading = false
	if m.err != nil {
		a.logf("feed error: %s", m.err.Error())
		if m.err.Kind == opds.KindAuth {
			a.confirmAuth()
		}
		if m.appnd && !lv.jump {
			// A read-ahead of the next page failed while nobody asked for it: keep
			// the list on screen. The next-page chevron tries again (and then
			// reports the error if it fails again).
			lv.nextFailed = true
			return
		}
		lv.err = m.err
		if a.listVisible() && a.top() == lv {
			a.render("feed-error")
		}
		return
	}
	f := m.feed
	rows, hidden := a.rowsFromFeed(f, m.reqURL)
	if !m.appnd {
		lv.hidden = 0
	}
	if hidden > 0 {
		a.debugf("hid %d entries without a page stream", hidden)
		lv.hidden += hidden
	}
	titleBefore, searchBefore := lv.title, a.searchURL
	oldLen := len(lv.rows)
	if m.appnd {
		lv.rows = append(lv.rows, rows...)
		if lv.jump {
			lv.scroll = oldLen
		}
	} else {
		lv.rows = rows
		if lv.home {
			a.refreshContinue(lv)
		}
	}
	wasJump := lv.jump
	lv.jump = false
	lv.next = a.resolve(f.Next(), m.reqURL)
	lv.total = f.Total
	lv.libs = f.ID == "libraries"
	if lv.title == "" {
		lv.title = f.Title
	}
	if lv.home && !m.appnd {
		if sd := f.SearchDescription(); sd != "" {
			a.searchURL = a.resolve(sd, m.reqURL)
		}
	}
	// A series with nothing to read (only files that cannot be streamed) is
	// removed from the list it was opened from.
	if !m.appnd && len(rows) == 0 && hidden > 0 && len(a.stack) > 1 && a.top() == lv && !lv.home && !lv.libs && lv.next == "" {
		a.hideCurrent()
		return
	}
	if a.listVisible() && a.top() == lv {
		// the header (title, search icon) only changes when the feed supplied the
		// title or the search link: otherwise just the body below it is sent
		hdrChanged := lv.title != titleBefore || (searchBefore == "" && a.searchURL != "")
		render := func(tag string) {
			if hdrChanged {
				a.render(tag)
			} else {
				a.renderBody(tag)
			}
		}
		switch {
		case !m.appnd:
			render("list")
		case wasJump || lv.shownLoading:
			render("list-more")
		default:
			// The next 20 rows arrived behind the ones on screen: nothing visible
			// changes, so there is no redraw (no flash), just more read-ahead.
			a.prefetchFeed(lv)
		}
	}
}

// hideCurrent drops the open series from its parent list and goes back.
func (a *App) hideCurrent() {
	if len(a.stack) < 2 {
		return
	}
	lv := a.top()
	parent := a.stack[len(a.stack)-2]
	for i, r := range parent.rows {
		if r.kind == rowFolder && r.href == lv.url {
			parent.rows = append(parent.rows[:i], parent.rows[i+1:]...)
			break
		}
	}
	a.stack = a.stack[:len(a.stack)-1]
	a.render("hide")
}

// refreshContinue puts the home list's "Continue" row (the last chapter read)
// at the top, replacing any earlier one.
func (a *App) refreshContinue(lv *listView) {
	if len(lv.rows) > 0 && lv.rows[0].kind == rowContinue {
		lv.rows = lv.rows[1:]
	}
	if p, ok := a.Pos.Last(); ok && p.Count > 0 && p.Page < p.Count-1 {
		lv.rows = append([]row{a.continueRow(p)}, lv.rows...)
	}
}

func (a *App) continueRow(p store.Pos) row {
	ids := opds.IDs{Library: p.Library, Series: p.Series, Volume: p.Volume, Chapter: p.Chapter}
	tpl := a.resolve(opds.StreamTemplate(a.rootPath, ids), "")
	doc := opds.DocComic
	if p.Kind == "pdf" {
		doc = opds.DocPDF
	}
	return row{kind: rowContinue, title: p.Title, sub: fmt.Sprintf("page %d of %d", p.Page+1, p.Count),
		stream: &opds.Stream{Template: tpl, Count: p.Count, LastRead: -1}, ids: ids, read: -1, doc: doc}
}

// ---- drawing ---------------------------------------------------------------

func (a *App) pieFrac(r *row) (float64, bool) {
	if r.ids.Chapter != 0 {
		if p, ok := a.Pos.Get(r.ids.Chapter); ok && p.Count > 0 {
			f := float64(p.Page+1) / float64(p.Count)
			if p.Page == 0 {
				f = 0
			}
			return f, true
		}
	}
	switch r.prog {
	case opds.ProgressNone:
		return 0, true
	case opds.ProgressQuarter:
		return 0.25, true
	case opds.ProgressHalf:
		return 0.5, true
	case opds.ProgressMost:
		return 0.75, true
	case opds.ProgressFull:
		return 1, true
	}
	if r.read >= 0 && r.stream != nil && r.stream.Count > 0 {
		return float64(r.read) / float64(r.stream.Count), true
	}
	return 0, false
}

func (a *App) drawRow(r *row, y int, withThumb bool) {
	c := a.cv
	h := a.rowH()
	m := a.margin()
	x := m
	if withThumb {
		tw, th := a.u(112), a.u(150)
		ty := y + (h-th)/2
		if t := a.thumbs[r.thumb]; t != nil && t.state == 2 && t.img != nil {
			b := t.img.Bounds()
			c.Blit(t.img, image.Pt(m+(tw-b.Dx())/2, ty+(th-b.Dy())/2))
			t.shown = true
		} else if r.thumb != "" {
			c.Frame(image.Rect(m, ty, m+tw, ty+th), a.u(2), inkRule)
		}
		x = m + tw + a.u(28)
	}
	right := a.W - m - a.u(70)
	switch r.kind {
	case rowFolder:
		c.Chevron(a.W-m-a.u(20), y+h/2, a.u(40), 1, a.u(7), inkBlack)
	case rowChapter, rowContinue:
		cx, cy, rad := a.W-m-a.u(30), y+h/2, a.u(26)
		c.Ring(cx, cy, rad, a.u(4), inkBlack)
		if f, ok := a.pieFrac(r); ok {
			c.Pie(cx, cy, rad-a.u(8), f, inkBlack)
		}
	}
	ts := a.st(44, r.kind == rowContinue, inkBlack)
	ss := a.st(32, false, inkDim)
	sub := a.rowSub(r)
	maxW := right - x
	if sub != "" {
		t := a.Fonts.Ellipsize(r.title, maxW, ts)
		a.Fonts.Draw(c, x, y+h/2-a.u(6), t, ts)
		a.Fonts.Draw(c, x, y+h/2+a.u(42), a.Fonts.Ellipsize(sub, maxW, ss), ss)
	} else {
		lines := a.Fonts.Wrap(r.title, maxW, ts, 2)
		if len(lines) == 1 {
			a.Fonts.Draw(c, x, a.textBaseline(y, h, ts.Size), lines[0], ts)
		} else {
			a.Fonts.Draw(c, x, y+h/2-a.u(8), lines[0], ts)
			a.Fonts.Draw(c, x, y+h/2+a.u(44), lines[1], ts)
		}
	}
	c.Fill(image.Rect(m, y+h-a.u(2), a.W-m, y+h), inkRule)
}

func (a *App) rowSub(r *row) string {
	if r.kind == rowChapter && r.ids.Chapter != 0 {
		if p, ok := a.Pos.Get(r.ids.Chapter); ok && p.Count > 0 && p.Page > 0 {
			return fmt.Sprintf("page %d of %d", p.Page+1, p.Count)
		}
	}
	return r.sub
}

func (a *App) drawList(lv *listView) {
	c := a.cv
	a.layerActive = false
	a.opaque = a.opaque[:0]
	c.Fill(a.full(), paperGray)
	a.hits = a.hits[:0]
	a.drawHeader(hdr{title: lv.title, back: len(a.stack) > 1, home: true, homeActive: len(a.stack) > 1,
		search: a.searchURL != "", gear: true, account: true})

	n := a.rowsPerPage()
	top := a.listTop()
	withThumb := false
	if a.Cfg.Covers {
		for i := range lv.rows {
			if lv.rows[i].thumb != "" {
				withThumb = true
				break
			}
		}
	}
	switch {
	case lv.err != nil:
		a.drawErrorBox(lv.err, func() { a.loadList(lv, lv.next != "" && len(lv.rows) > 0 && lv.url != ""); a.render("retry") }, a.back)
	case lv.loading && len(lv.rows) == 0:
		s := a.st(46, false, inkDim)
		w := a.Fonts.Width("Loading", s)
		a.Fonts.Draw(c, (a.W-w)/2, a.H/2, "Loading", s)
	case len(lv.rows) == 0:
		msg := "Nothing here"
		if lv.libs {
			msg = "No libraries"
		}
		s := a.st(50, false, inkDim)
		w := a.Fonts.Width(msg, s)
		a.Fonts.Draw(c, (a.W-w)/2, a.H/2, msg, s)
	default:
		lo := lv.scroll
		if lo >= len(lv.rows) {
			lo = 0
			lv.scroll = 0
		}
		for i := 0; i < n && lo+i < len(lv.rows); i++ {
			r := &lv.rows[lo+i]
			y := top + i*a.rowH()
			a.drawRow(r, y, withThumb)
			idx := lo + i
			a.addNamedHit(fmt.Sprintf("row-%d", i), image.Rect(0, y, a.W, y+a.rowH()), func() { a.rowTap(lv, idx) })
		}
	}
	_ = top
	a.drawFooter(lv, n)
	a.drawPopover()
}

func (a *App) drawFooter(lv *listView, n int) {
	c := a.cv
	top := a.listBottom()
	c.Fill(image.Rect(0, top, a.W, a.H), paperGray)
	c.Fill(image.Rect(0, top, a.W, top+a.u(3)), inkBlack)
	if len(lv.rows) == 0 && lv.err != nil {
		return
	}
	h := a.footerH()
	cy := top + h/2
	hasPrev := lv.scroll > 0
	hasNext := lv.scroll+n < len(lv.rows) || lv.next != ""
	if hasPrev {
		c.Chevron(a.u(160), cy, a.u(60), -1, a.u(9), inkBlack)
		a.addNamedHit("page-prev", image.Rect(0, top, a.u(420), a.H), func() { a.listPage(-1) })
	}
	if hasNext {
		c.Chevron(a.W-a.u(160), cy, a.u(60), 1, a.u(9), inkBlack)
		a.addNamedHit("page-next", image.Rect(a.W-a.u(420), top, a.W, a.H), func() { a.listPage(1) })
	}
	if len(lv.rows) > 0 {
		hi := lv.scroll + n
		if hi > len(lv.rows) {
			hi = len(lv.rows)
		}
		label := fmt.Sprintf("%d-%d", lv.scroll+1, hi)
		if lv.total-lv.hidden > 0 {
			label += fmt.Sprintf(" of %d", lv.total-lv.hidden)
		} else if lv.next != "" {
			label += " ..."
		}
		lv.shownLoading = lv.loading
		if lv.loading {
			label = "Loading"
		}
		s := a.st(38, false, inkDim)
		w := a.Fonts.Width(label, s)
		a.Fonts.Draw(c, (a.W-w)/2, a.textBaseline(top, h, s.Size), label, s)
	}
}

// drawErrorBox draws a one-line message with Retry and Back buttons.
func (a *App) drawErrorBox(e *opds.Error, retry, back func()) {
	c := a.cv
	s := a.st(50, true, inkBlack)
	msg := e.Short()
	w := a.Fonts.Width(msg, s)
	cy := a.H/2 - a.u(60)
	a.Fonts.Draw(c, (a.W-w)/2, cy, msg, s)
	ds := a.st(32, false, inkDim)
	detail := a.Fonts.Ellipsize(a.Cl.Redact(e.Msg), a.W-2*a.margin(), ds)
	dw := a.Fonts.Width(detail, ds)
	a.Fonts.Draw(c, (a.W-dw)/2, cy+a.u(60), detail, ds)
	bw, bh := a.u(420), a.u(140)
	by := cy + a.u(130)
	labels := []string{"Retry"}
	names := []string{"retry"}
	acts := []func(){retry}
	if back != nil && (len(a.stack) > 1 || a.rd != nil) {
		labels = append(labels, "Back")
		names = append(names, "err-back")
		acts = append(acts, back)
	}
	gap := a.u(60)
	total := len(labels)*bw + (len(labels)-1)*gap
	x := (a.W - total) / 2
	bs := a.st(46, true, inkBlack)
	for i, l := range labels {
		r := image.Rect(x, by, x+bw, by+bh)
		c.Frame(r, a.u(5), inkBlack)
		tw := a.Fonts.Width(l, bs)
		a.Fonts.Draw(c, x+(bw-tw)/2, a.textBaseline(by, bh, bs.Size), l, bs)
		a.addNamedHit(names[i], r, acts[i])
		x += bw + gap
	}
}

// ---- interaction -------------------------------------------------------------

// render redraws the current screen completely and presents it (partial UI
// update; the clearing refresh is a separate, deliberate call).
func (a *App) render(tag string) {
	switch a.screen() {
	case scrSignin:
		a.drawSignin()
		a.present(a.full(), a.uiMode(), false, tag)
	case scrSettings:
		a.drawSettings()
		a.present(a.full(), a.uiMode(), false, tag)
	case scrReader:
		a.readerRedraw("ui")
	case scrSearch:
		a.drawSearch()
		a.present(a.full(), a.uiMode(), false, tag)
	default:
		lv := a.top()
		a.drawList(lv)
		a.present(a.full(), a.uiMode(), false, tag)
		a.wantThumbs(lv)
		a.prefetchFeed(lv)
	}
}

func (a *App) prefetchFeed(lv *listView) {
	if lv.next != "" && !lv.loading && lv.err == nil && !lv.nextFailed && lv.scroll+2*a.rowsPerPage() >= len(lv.rows) {
		a.loadList(lv, true)
	}
}

func (a *App) rowTap(lv *listView, idx int) {
	if idx >= len(lv.rows) {
		return
	}
	r := lv.rows[idx]
	switch r.kind {
	case rowFolder:
		nv := &listView{title: r.title, url: r.href, loading: true}
		a.pushList(nv)
		a.loadList(nv, false)
		a.render("open")
	case rowChapter, rowContinue:
		a.openReader(lv, idx)
	}
}

func (a *App) listPage(dir int) {
	if a.screen() != scrList {
		return
	}
	lv := a.top()
	n := a.rowsPerPage()
	if dir > 0 {
		switch {
		case lv.scroll+n < len(lv.rows):
			lv.scroll += n
		case lv.next != "":
			lv.jump = true
			if !lv.loading {
				a.loadList(lv, true)
			}
		default:
			return
		}
	} else {
		if lv.scroll == 0 {
			return
		}
		lv.scroll -= n
		if lv.scroll < 0 {
			lv.scroll = 0
		}
	}
	a.renderBody("scroll")
}

// renderBody redraws the list and sends the area below the header only: the
// header (title, buttons) did not change when the list scrolled.
func (a *App) renderBody(tag string) {
	if a.screen() != scrList || a.pop.open {
		a.render(tag)
		return
	}
	lv := a.top()
	a.drawList(lv)
	a.present(image.Rect(0, a.listTop(), a.W, a.H), a.uiMode(), false, tag)
	a.wantThumbs(lv)
	a.prefetchFeed(lv)
}

// back leaves the current screen.
func (a *App) back() {
	a.closePopover()
	switch a.screen() {
	case scrSettings:
		a.closeSettings()
	case scrSearch:
		a.kb = nil
		a.render("search-back")
	case scrReader:
		a.closeReader()
	default:
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
			a.render("back")
		}
	}
}

// goHome returns to the root list from anywhere.
func (a *App) goHome() {
	a.closePopover()
	if a.screen() == scrSettings {
		a.flushSettings()
		a.sv = nil
	}
	a.kb = nil
	wasReader := a.rd != nil
	if wasReader {
		a.closeReaderState(false)
	}
	if len(a.stack) > 1 {
		a.stack = a.stack[:1]
	}
	lv := a.top()
	a.refreshContinue(lv)
	lv.scroll = 0
	a.hits = a.hits[:0]
	a.drawList(lv)
	if wasReader {
		a.clearScreen("home") // leaving a page: wipe it properly
	} else {
		a.present(a.full(), a.uiMode(), false, "home")
	}
	a.wantThumbs(lv)
	a.prefetchFeed(lv)
}

// ---- covers -----------------------------------------------------------------

func (a *App) wantThumbs(lv *listView) {
	if !a.Cfg.Covers || lv.err != nil {
		return
	}
	n := a.rowsPerPage()
	for i := 0; i < n && lv.scroll+i < len(lv.rows); i++ {
		u := lv.rows[lv.scroll+i].thumb
		if u == "" || a.thumbs[u] != nil {
			continue
		}
		if a.loadingThumbs() >= 2 {
			return
		}
		a.thumbs[u] = &thumb{state: 1}
		a.thumbOrd = append(a.thumbOrd, u)
		url := u
		tw, th := a.u(112), a.u(150)
		sctx := a.sctx
		a.async(func() any {
			ctx, cancel := context.WithTimeout(sctx, 30*time.Second)
			defer cancel()
			b, err := a.Cl.GetImage(ctx, url)
			if err != nil {
				a.logf("cover fetch failed: %v", err)
				return thumbMsg{url: url}
			}
			defer a.acquireDecode(b)()
			g, err := imgproc.DecodeGray(b)
			if err != nil {
				a.logf("cover decode failed: %v", err)
				return thumbMsg{url: url}
			}
			return thumbMsg{url: url, img: imgproc.Fit(g, tw, th)}
		})
	}
}

func (a *App) loadingThumbs() int {
	n := 0
	for _, t := range a.thumbs {
		if t.state == 1 {
			n++
		}
	}
	return n
}

func (a *App) onThumb(m thumbMsg) {
	a.noteDecoded()
	t := a.thumbs[m.url]
	if t == nil {
		return
	}
	if m.img != nil {
		t.img, t.state = m.img, 2
	} else {
		t.state = 3
	}
	// bound the cache
	for len(a.thumbOrd) > 60 {
		old := a.thumbOrd[0]
		a.thumbOrd = a.thumbOrd[1:]
		if ot := a.thumbs[old]; ot != nil && ot.state != 1 {
			delete(a.thumbs, old)
		}
	}
	if a.listVisible() {
		a.thumbT.Reset(450 * time.Millisecond)
		a.wantThumbs(a.top())
	}
}

func (a *App) onThumbTick() {
	if a.listVisible() && !a.quitting {
		a.renderCovers()
	}
}

// renderCovers repaints the list and sends only the rows whose cover arrived
// since they were last drawn (not the whole screen).
func (a *App) renderCovers() {
	lv := a.top()
	if a.pop.open {
		a.render("covers")
		return
	}
	n := a.rowsPerPage()
	top := a.listTop()
	var rows []image.Rectangle
	for i := 0; i < n && lv.scroll+i < len(lv.rows); i++ {
		if t := a.thumbs[lv.rows[lv.scroll+i].thumb]; t != nil && t.state == 2 && t.img != nil && !t.shown {
			y := top + i*a.rowH()
			rows = append(rows, image.Rect(0, y, a.W, y+a.rowH()))
		}
	}
	a.drawList(lv)
	for _, rc := range rows {
		a.present(rc, a.uiMode(), false, "covers")
	}
	a.wantThumbs(lv)
}
