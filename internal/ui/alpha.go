package ui

import (
	"context"
	"fmt"
	"image"
	"strings"
	"time"
	"unicode"

	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

// Long lists: a compact one-line row, and an A-Z jump bar on the right edge. The
// index behind the bar is the list itself (rows already carry the titles); the
// background fill only makes sure that every page of a long OPDS feed is loaded.
//
// E-ink rules: a jump or a scroll is ONE update of the list area (rows, bar and
// footer together); the bar follows a dragging finger with at most one small
// update per 300 ms and only when the letter under it changes; nothing animates.

const (
	// alphaCap is the most rows a list is filled to in the background.
	alphaCap = 5000
	// listCacheMax is how many filled lists the session keeps; listCacheTTL is
	// how long one is trusted without a look at the server.
	listCacheMax = 3
	listCacheTTL = 15 * time.Minute
	// hintEvery is the least time between two bar updates while a finger drags.
	hintEvery = 300 * time.Millisecond
	// progressEvery is the least time between two "Loading list k/N" repaints.
	progressEvery = 2 * time.Second
	// alphaLetters is the number of targets: # and A-Z.
	alphaLetters = 27
)

// ---- letters ------------------------------------------------------------------

var foldTable = map[rune]byte{}

func init() {
	for _, g := range []struct {
		l byte
		s string
	}{
		{'A', "ÀÁÂÃÄÅÆàáâãäåæĀāĂăĄąǍǎ"},
		{'B', "ƀƁƃ"},
		{'C', "ÇçĆćĈĉĊċČč"},
		{'D', "ĎďĐđÐð"},
		{'E', "ÈÉÊËèéêëĒēĔĕĖėĘęĚě"},
		{'G', "ĜĝĞğĠġĢģ"},
		{'H', "ĤĥĦħ"},
		{'I', "ÌÍÎÏìíîïĨĩĪīĬĭĮįİı"},
		{'J', "Ĵĵ"},
		{'K', "Ķķ"},
		{'L', "ĹĺĻļĽľĿŀŁł"},
		{'N', "ÑñŃńŅņŇň"},
		{'O', "ÒÓÔÕÖØòóôõöøŌōŎŏŐőŒœ"},
		{'R', "ŔŕŖŗŘř"},
		{'S', "ŚśŜŝŞşŠšß"},
		{'T', "ŢţŤťŦŧÞþ"},
		{'U', "ÙÚÛÜùúûüŨũŪūŬŭŮůŰűŲų"},
		{'W', "Ŵŵ"},
		{'Y', "ÝýÿŶŷŸ"},
		{'Z', "ŹźŻżŽž"},
	} {
		for _, r := range g.s {
			foldTable[r] = g.l
		}
	}
}

// letterOf is the index of the first alphanumeric character of s in the bar: 0
// is '#' (digits, symbols, CJK, anything that is not a Latin letter), 1..26 are
// A..Z. Accents are folded and leading punctuation is skipped. With
// skipArticles a leading "The", "A" or "An" word is skipped too.
func letterOf(s string, skipArticles bool) int {
	rs := []rune(s)
	i := 0
	skipPunct := func() {
		for i < len(rs) && !unicode.IsLetter(rs[i]) && !unicode.IsDigit(rs[i]) {
			i++
		}
	}
	skipPunct()
	if skipArticles {
		for _, art := range []string{"the", "an", "a"} {
			n := len(art)
			if i+n < len(rs) && unicode.IsSpace(rs[i+n]) && strings.EqualFold(string(rs[i:i+n]), art) {
				j := i + n
				for j < len(rs) && unicode.IsSpace(rs[j]) {
					j++
				}
				// only when something alphanumeric follows
				k := j
				for k < len(rs) && !unicode.IsLetter(rs[k]) && !unicode.IsDigit(rs[k]) {
					k++
				}
				if k < len(rs) {
					i = k
				}
				break
			}
		}
	}
	if i >= len(rs) {
		return 0
	}
	r := rs[i]
	switch {
	case r >= 'a' && r <= 'z':
		return int(r-'a') + 1
	case r >= 'A' && r <= 'Z':
		return int(r-'A') + 1
	}
	if l, ok := foldTable[r]; ok {
		return int(l-'A') + 1
	}
	return 0
}

// letterName is the bar's label of letter index i.
func letterName(i int) string {
	if i <= 0 {
		return "#"
	}
	return string(rune('A' + i - 1))
}

// alphaIdx is the letter index of a list: the key of every row, the first row of
// each letter, and how alphabetical the order is.
type alphaIdx struct {
	n       int
	keys    []uint8
	first   [alphaLetters]int
	cnt     int     // letters with at least one row
	ordered float64 // share of adjacent rows that do not go backwards
	art     bool    // the keys skip leading articles (the server sorts that way)
}

// buildAlphaIdx keys the rows both ways (title as shown; leading article
// skipped, which is how Kavita's sort names read) and keeps the way that makes
// the list the more alphabetical.
func buildAlphaIdx(rows []row) *alphaIdx {
	raw := make([]uint8, len(rows))
	art := make([]uint8, len(rows))
	for i := range rows {
		raw[i] = uint8(letterOf(rows[i].title, false))
		art[i] = uint8(letterOf(rows[i].title, true))
	}
	ord := func(k []uint8) float64 {
		if len(k) < 2 {
			return 1
		}
		ok := 0
		for i := 1; i < len(k); i++ {
			if k[i] >= k[i-1] {
				ok++
			}
		}
		return float64(ok) / float64(len(k)-1)
	}
	x := &alphaIdx{n: len(rows), keys: raw}
	x.ordered = ord(raw)
	if o := ord(art); o > x.ordered+0.02 {
		x.keys, x.ordered, x.art = art, o, true
	}
	for i := range x.first {
		x.first[i] = -1
	}
	for i, k := range x.keys {
		if x.first[k] < 0 {
			x.first[k] = i
			x.cnt++
		}
	}
	return x
}

// index returns the (cached) letter index of lv.
func (lv *listView) index() *alphaIdx {
	if lv.alpha == nil || lv.alpha.n != len(lv.rows) {
		lv.alpha = buildAlphaIdx(lv.rows)
	}
	return lv.alpha
}

// ---- which view -----------------------------------------------------------------

// listLen is the number of entries the list has or will have.
func listLen(lv *listView) int {
	n := len(lv.rows)
	if t := lv.total - lv.hidden; t > n {
		n = t
	}
	return n
}

// plainList: the home menu, the list of libraries and search results keep the
// old view.
func plainList(lv *listView) bool { return lv == nil || lv.home || lv.libs || lv.search }

// compactList reports whether lv is shown as one-line rows.
func (a *App) compactList(lv *listView) bool {
	if plainList(lv) {
		return false
	}
	switch a.Set.Lists {
	case store.ListsCompact:
		return true
	case store.ListsComfortable:
		return false
	}
	return listLen(lv) > store.CompactOver
}

// alphaShown reports whether lv has the A-Z bar.
func (a *App) alphaShown(lv *listView) bool {
	if plainList(lv) || len(lv.rows) < 2 {
		return false
	}
	switch a.Set.Alpha {
	case store.AlphaOff:
		return false
	case store.AlphaOn:
		return true
	}
	if listLen(lv) <= store.CompactOver {
		return false
	}
	x := lv.index()
	if x.ordered < 0.9 {
		return false
	}
	// still loading: the letters that are not here yet are dim, not a reason
	// to hide the bar; a finished list needs a few letters to be worth one
	return x.cnt >= 3 || lv.next != ""
}

// rowHeightFor is the height of a row of lv.
func (a *App) rowHeightFor(lv *listView) int {
	if a.compactList(lv) {
		return a.u(64)
	}
	return a.rowH()
}

// rowsFor is the number of rows a page of lv holds.
func (a *App) rowsFor(lv *listView) int {
	n := (a.listBottom() - a.listTop()) / a.rowHeightFor(lv)
	if n < 1 {
		n = 1
	}
	return n
}

// ---- the bar -----------------------------------------------------------------------

// alphaView is the bar as last drawn: where it is and what each letter does.
type alphaView struct {
	on      bool
	lv      *listView
	rect    image.Rectangle // the strip, with its rule
	y0      int
	cell    int
	active  [alphaLetters]bool
	cur     int // letter of the first visible row
	hint    int // letter drawn as hinted while a finger drags, -1 none
	hintAt  time.Time
	lastPrg time.Time // the last "Loading list" repaint
}

func (a *App) alphaBarW() int { return a.u(64) }

// alphaSpan is the vertical run of the 27 targets: the whole list area. It
// reaches into the two right-hand corner zones by a few millimetres; a press that
// starts on the bar never arms a corner hold (see onTouch), and the corner icons
// of the header and the footer's page chevron are outside it.
func (a *App) alphaSpan() (y0, y1 int) {
	return a.listTop() + a.u(6), a.listBottom() - a.u(6)
}

// layoutAlpha computes the bar of lv into a.alpha (not drawn).
func (a *App) layoutAlpha(lv *listView) {
	a.alpha.on = false
	a.alpha.hint = -1
	if a.screen() != scrList || !a.alphaShown(lv) {
		return
	}
	y0, y1 := a.alphaSpan()
	cell := (y1 - y0) / alphaLetters
	if cell < a.u(24) {
		return
	}
	x := lv.index()
	v := &a.alpha
	v.on, v.lv, v.y0, v.cell = true, lv, y0, cell
	v.rect = image.Rect(a.W-a.alphaBarW(), a.listTop(), a.W, a.listBottom())
	for i := range v.active {
		v.active[i] = x.first[i] >= 0
	}
	v.cur = -1
	if lv.scroll >= 0 && lv.scroll < len(x.keys) {
		v.cur = int(x.keys[lv.scroll])
	}
}

// cellRect is the rectangle of letter i.
func (a *App) alphaCell(i int) image.Rectangle {
	v := &a.alpha
	y := v.y0 + i*v.cell
	return image.Rect(v.rect.Min.X+a.u(3), y, a.W, y+v.cell)
}

// drawAlpha paints the bar: solid white strip, a rule on its left, dim letters
// with no rows, the current letter inverted.
func (a *App) drawAlpha() {
	v := &a.alpha
	if !v.on {
		return
	}
	c := a.cv
	c.Fill(v.rect, paperGray)
	c.Fill(image.Rect(v.rect.Min.X, v.rect.Min.Y, v.rect.Min.X+a.u(2), v.rect.Max.Y), inkRule)
	for i := 0; i < alphaLetters; i++ {
		a.drawAlphaCell(i)
	}
}

func (a *App) drawAlphaCell(i int) {
	v := &a.alpha
	c := a.cv
	cell := a.alphaCell(i)
	ink := uint8(inkBlack)
	if !v.active[i] {
		ink = inkRule
	}
	bg := uint8(paperGray)
	switch {
	case i == v.cur && v.active[i]:
		bg, ink = inkBlack, paperGray
		c.Fill(cell.Inset(a.u(2)), bg)
	case i == v.hint && v.active[i]:
		c.Frame(cell.Inset(a.u(2)), a.u(4), inkBlack)
	}
	s := a.st(30, v.active[i], ink)
	s.Size = max(s.Size, 14)
	t := letterName(i)
	w := a.Fonts.Width(t, s)
	a.Fonts.Draw(c, cell.Min.X+(cell.Dx()-w)/2, a.textBaseline(cell.Min.Y, cell.Dy(), s.Size), t, s)
}

// alphaLetterAt is the letter under y (clamped to the strip), or -1 when the bar
// is not up.
func (a *App) alphaLetterAt(y int) int {
	v := &a.alpha
	if !v.on {
		return -1
	}
	i := (y - v.y0) / v.cell
	if y < v.y0 {
		i = 0
	}
	if i < 0 {
		i = 0
	}
	if i >= alphaLetters {
		i = alphaLetters - 1
	}
	return i
}

// alphaHit reports whether a press at (x, y) belongs to the bar.
func (a *App) alphaHit(x, y int) bool {
	v := &a.alpha
	if !v.on || a.screen() != scrList || a.pop.open || a.top() != v.lv {
		return false
	}
	return x >= v.rect.Min.X && x < a.W && y >= v.y0 && y < v.y0+alphaLetters*v.cell
}

// alphaMove follows a dragging finger: the letter under it is outlined, with
// one small update per change, no faster than hintEvery. The list is not touched.
func (a *App) alphaMove(y int) {
	v := &a.alpha
	i := a.alphaLetterAt(y)
	if i < 0 || i == v.hint || !v.active[i] {
		return
	}
	if time.Since(v.hintAt) < hintEvery {
		return
	}
	old := v.hint
	v.hint, v.hintAt = i, time.Now()
	r := a.alphaCell(i)
	a.drawAlphaCell(i)
	if old >= 0 {
		a.drawAlphaCell(old)
		r = r.Union(a.alphaCell(old))
	}
	a.present(r, a.uiMode(), false, "alpha-hint")
}

// alphaClearHint takes the outline back (a release on a dim letter, a press
// that was let go of). It costs one update only when an outline was drawn.
func (a *App) alphaClearHint() {
	v := &a.alpha
	if !v.on || v.hint < 0 {
		return
	}
	old := v.hint
	v.hint = -1
	if a.screen() != scrList || a.top() != v.lv {
		return
	}
	a.drawAlphaCell(old)
	a.present(a.alphaCell(old), a.uiMode(), false, "alpha-hint")
}

// alphaRelease is the end of a press that began on the bar: jump to the first
// row of the letter under the finger.
func (a *App) alphaRelease(y int) {
	v := &a.alpha
	lv := v.lv
	i := a.alphaLetterAt(y)
	if i < 0 || lv == nil || a.top() != lv || !v.active[i] {
		a.alphaClearHint()
		return
	}
	if !a.alphaJump(lv, i) {
		a.alphaClearHint()
	}
}

// alphaJump scrolls lv so that the first row of letter i is on top and repaints
// the list area once (rows, bar and footer in one update). It returns false
// when there was nothing to do.
func (a *App) alphaJump(lv *listView, i int) bool {
	if lv == nil || a.top() != lv || i < 0 || i >= alphaLetters {
		return false
	}
	to := lv.index().first[i]
	if to < 0 || to >= len(lv.rows) || to == lv.scroll {
		return false
	}
	lv.scroll = to
	a.renderBody("jump")
	return true
}

// ---- background fill -----------------------------------------------------------------

// shouldFill reports whether the remaining pages of lv should be fetched in the
// background now.
func (a *App) shouldFill(lv *listView) bool {
	if plainList(lv) || lv.next == "" || len(lv.rows) >= alphaCap || a.Set.Alpha == store.AlphaOff {
		return false
	}
	if a.Set.Alpha == store.AlphaOn {
		return true
	}
	// auto: a long list that is still alphabetical as the pages arrive
	if listLen(lv) <= store.CompactOver {
		return false
	}
	return lv.index().ordered >= 0.9
}

// fillLabel is the footer's "Loading list 3/9".
func (lv *listView) fillLabel() string {
	pp := lv.perPage
	if pp <= 0 {
		pp = 20
	}
	total := lv.total
	if total <= 0 {
		return "Loading list"
	}
	pages := (total + pp - 1) / pp
	if pages < lv.pages {
		pages = lv.pages
	}
	return fmt.Sprintf("Loading list %d/%d", lv.pages, pages)
}

// afterPage runs after a catalogue page was added to lv: it counts pages, asks
// for the next one when the list is being filled, and keeps the session cache.
func (a *App) afterPage(lv *listView, appended bool, f *opds.Feed) {
	if !appended {
		lv.pages = 1
		lv.perPage = f.PerPage
		if n := len(f.Entries); n > lv.perPage {
			lv.perPage = n
		}
	} else {
		lv.pages++
	}
	lv.alpha = nil
	was := lv.filling
	switch {
	case lv.next == "":
		lv.filling = false
		if was || lv.pages > 1 || len(lv.rows) > store.CompactOver {
			a.cacheList(lv)
		}
	case a.shouldFill(lv):
		lv.filling = true
		if !lv.loading {
			a.loadList(lv, true)
		}
	default:
		lv.filling = false
	}
}

// fillProgress repaints the footer (and the bar, whose letters may have come
// alive) while a list is filling or when it just finished: one update, at most
// every progressEvery, never while a finger is down.
func (a *App) fillProgress(lv *listView, final bool) {
	if !a.listVisible() || a.top() != lv || a.pop.open || a.touch.active {
		return
	}
	v := &a.alpha
	if !final && time.Since(v.lastPrg) < progressEvery {
		return
	}
	v.lastPrg = time.Now()
	a.drawList(lv)
	a.present(image.Rect(0, a.listBottom(), a.W, a.H), a.uiMode(), false, "list-progress")
	if v.on {
		a.present(v.rect, a.uiMode(), false, "list-progress")
	}
}

// ---- session cache ---------------------------------------------------------------------

type listCacheEntry struct {
	rows   []row
	total  int
	hidden int
	pages  int
	per    int
	at     time.Time
}

// cacheList keeps a completely loaded long list for the session.
func (a *App) cacheList(lv *listView) {
	if lv.url == "" || lv.next != "" || lv.search {
		return
	}
	if a.listCache == nil {
		a.listCache = map[string]*listCacheEntry{}
	}
	a.listCache[lv.url] = &listCacheEntry{rows: append([]row(nil), lv.rows...), total: lv.total, hidden: lv.hidden,
		pages: lv.pages, per: lv.perPage, at: time.Now()}
	for len(a.listCache) > listCacheMax {
		oldest, key := time.Now(), ""
		for k, e := range a.listCache {
			if e.at.Before(oldest) {
				oldest, key = e.at, k
			}
		}
		if key == "" {
			break
		}
		delete(a.listCache, key)
	}
}

// cachedList returns a copy of the cached rows of url if they are fresh enough
// to show at once (the caller then verifies them against page 1).
func (a *App) cachedList(url string) *listCacheEntry {
	e := a.listCache[url]
	if e == nil || time.Since(e.at) > listCacheTTL {
		return nil
	}
	return e
}

// sameFirstPage reports whether the entries of a freshly fetched first page
// are the ones the cached list starts with (titles and progress).
func sameFirstPage(cached []row, fresh []row) bool {
	if len(fresh) > len(cached) {
		return false
	}
	for i := range fresh {
		if fresh[i].title != cached[i].title || fresh[i].prog != cached[i].prog || fresh[i].href != cached[i].href {
			return false
		}
	}
	return true
}

// verifyList fetches page 1 of a list that was shown from the session cache.
func (a *App) verifyList(lv *listView) {
	sctx := a.sctx
	target := lv.url
	a.async(func() any {
		ctx, cancel := context.WithTimeout(sctx, 45*time.Second)
		defer cancel()
		f, err := a.Cl.GetFeed(ctx, target)
		return feedMsg{lv: lv, feed: f, err: errOf(err), reqURL: target, verify: true}
	})
}

// onVerify compares page 1 with the cached start of the list. Nothing differs:
// nothing is repainted. Something does (a series was added, read, removed): the
// cache entry is dropped and the list is loaded again from page 1.
func (a *App) onVerify(m feedMsg) {
	lv := m.lv
	lv.verify = false
	if m.err != nil {
		a.logf("list check failed (keeping the cached list): %s", m.err.Error())
		return
	}
	rows, hidden := a.rowsFromFeed(m.feed, m.reqURL)
	if m.feed.Total == lv.total && hidden <= lv.hidden && sameFirstPage(lv.rows, rows) {
		return
	}
	a.debugf("list changed on the server: loading it again")
	delete(a.listCache, lv.url)
	lv.rows, lv.hidden, lv.alpha, lv.scroll = nil, 0, nil, 0
	lv.next, lv.pages, lv.filling, lv.nextFailed = "", 0, false, false
	a.onFeed(feedMsg{lv: lv, feed: m.feed, reqURL: m.reqURL})
}
