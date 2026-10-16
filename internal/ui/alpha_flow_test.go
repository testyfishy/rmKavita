package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/store"
)

// openBig walks home -> All Libraries -> Big Library (the 4th library).
func (h *harness) openBig() {
	h.t.Helper()
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "All Libraries" && s.ListRows >= 4 }, "libraries")
	h.tapName("row-3")
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Big Library" && s.ListRows > 0 }, "big library")
}

// listState is the list on top, read from the UI goroutine.
type listState struct {
	rows, scroll, perPage, rowH int
	compact, bar, filling, next bool
	cur                         string
	first                       map[string]int
}

func (h *harness) listState() (ls listState) {
	h.onUI(func() {
		a := h.app
		lv := a.top()
		ls.rows, ls.scroll = len(lv.rows), lv.scroll
		ls.perPage, ls.rowH = a.rowsFor(lv), a.rowHeightFor(lv)
		ls.compact, ls.bar, ls.filling, ls.next = a.compactList(lv), a.alpha.on, lv.filling, lv.next != ""
		if a.alpha.on && a.alpha.cur >= 0 {
			ls.cur = letterName(a.alpha.cur)
		}
		ls.first = map[string]int{}
		x := lv.index()
		for i := range x.first {
			if x.first[i] >= 0 {
				ls.first[letterName(i)] = x.first[i]
			}
		}
	})
	return
}

func (h *harness) waitBigLoaded(n int) {
	h.t.Helper()
	h.waitFor(func() bool { s := h.listState(); return s.rows >= n && !s.next && !s.filling }, fmt.Sprintf("%d rows loaded", n))
}

func (h *harness) listTopY() int {
	var y int
	h.onUI(func() { y = h.app.listTop() })
	return y
}

func TestBigLibraryIsCompactFilledAndJumpable(t *testing.T) {
	h := startOpts(t, hopts{big: 900})
	h.openBig()
	h.waitBigLoaded(900)
	s := h.listState()
	if !s.compact || !s.bar {
		t.Fatalf("900 series: compact=%v bar=%v", s.compact, s.bar)
	}
	if s.perPage < 25 {
		t.Errorf("only %d rows a page (comfortable has 7)", s.perPage)
	}
	t.Logf("compact rows: %d a page, row height %d px, %d letters have entries", s.perPage, s.rowH, len(s.first))
	hits := map[string]bool{}
	for _, hh := range h.snapshot().Hits {
		hits[hh.Name] = true
	}
	for i := 0; i < s.perPage; i++ {
		if !hits[fmt.Sprintf("row-%d", i)] {
			t.Errorf("row-%d has no touch target", i)
		}
	}
	if !hits["alpha-A"] || !hits["alpha-M"] {
		t.Error("the bar's letters have no touch targets")
	}
	// jump to M: the first entry that starts with M is on top, in ONE update of
	// the list area, and the bar says M
	before := h.disp.count("jump")
	n0 := len(h.disp.all())
	h.tapName("alpha-M")
	h.waitFor(func() bool { return h.disp.count("jump") == before+1 }, "the jump update")
	time.Sleep(60 * time.Millisecond)
	s = h.listState()
	if want := s.first["M"]; s.scroll != want {
		t.Errorf("scroll %d, want the first M at %d", s.scroll, want)
	}
	if s.cur != "M" {
		t.Errorf("the bar marks %q, want M", s.cur)
	}
	frames := h.disp.all()[n0:]
	if len(frames) != 1 || frames[0].tag != "jump" {
		t.Errorf("a jump sent %d updates: %+v", len(frames), frames)
	} else if r := frames[0].rect; r.Min.Y > h.listTopY() || r.Max.X != 1620 || r.Max.Y != 2160 {
		t.Errorf("the jump update is %v, want the list area", r)
	}
	// open a series, Back: the place and the letter are restored without a request
	reqs := len(h.mock.Requests())
	h.tapName("row-2")
	h.waitFor(func() bool {
		s := h.snapshot()
		return s.ListTitle != "Big Library" && s.ListTitle != "" && s.ListRows > 0
	}, "series open")
	h.tapName("back")
	h.waitFor(func() bool { return h.snapshot().ListTitle == "Big Library" }, "back in the library")
	time.Sleep(60 * time.Millisecond)
	s2 := h.listState()
	if s2.scroll != s.scroll || s2.cur != "M" {
		t.Errorf("after Back: scroll %d letter %q, want %d M", s2.scroll, s2.cur, s.scroll)
	}
	feeds := 0
	for _, p := range h.mock.Requests()[reqs:] {
		if strings.Contains(p, "/libraries/") {
			feeds++
		}
	}
	if feeds != 0 {
		t.Errorf("Back asked the server for the library again (%d requests)", feeds)
	}
	h.stop()
}

// A drag down the bar: the finger outlines letters with small updates (at most
// one per 300 ms), never touches the list, and the pick happens on release.
func TestDragAlongTheBarOutlinesLettersAndJumpsOnRelease(t *testing.T) {
	h := startOpts(t, hopts{big: 900})
	h.openBig()
	h.waitBigLoaded(900)
	var y0, cell, x int
	h.onUI(func() { y0, cell = h.app.alpha.y0, h.app.alpha.cell; x = h.app.alpha.rect.Min.X + 20 })
	yAt := func(letter string) int {
		i := 0
		if letter != "#" {
			i = int(letter[0]-'A') + 1
		}
		return y0 + i*cell + cell/2
	}
	n0 := len(h.disp.all())
	h.send(display.Down, x, yAt("A"))
	for _, l := range []string{"B", "C", "D", "F", "H", "K", "N", "Q", "S", "T"} {
		h.send(display.Move, x-5, yAt(l))
	}
	h.send(display.Up, x-5, yAt("T"))
	h.waitFor(func() bool { return h.disp.count("jump") >= 1 }, "the jump")
	time.Sleep(60 * time.Millisecond)
	var tags []string
	hints, jumps, others := 0, 0, 0
	for _, f := range h.disp.all()[n0:] {
		tags = append(tags, f.tag)
		switch f.tag {
		case "alpha-hint":
			hints++
		case "jump":
			jumps++
		default:
			others++
		}
	}
	if jumps != 1 || others != 0 || hints > 2 {
		t.Errorf("a drag over ten letters sent %v (jumps=%d hints=%d other=%d)", tags, jumps, hints, others)
	}
	s := h.listState()
	if s.cur != "T" || s.scroll != s.first["T"] {
		t.Errorf("released on T: letter %q scroll %d (first T %d)", s.cur, s.scroll, s.first["T"])
	}
	h.stop()
}

// A press that lasts on the top letter is a letter pick, not the hot corner's
// function (top-right clears the screen by default).
func TestHoldOnTheBarIsNotAHotCorner(t *testing.T) {
	h := startOpts(t, hopts{big: 900, mut: func(c *store.Config) { c.HoldMs = 300 }})
	h.openBig()
	h.waitBigLoaded(900)
	var x, y int
	h.onUI(func() { x, y = h.app.alpha.rect.Min.X+20, h.app.alpha.y0+h.app.alpha.cell/2 })
	h.send(display.Down, x, y)
	time.Sleep(700 * time.Millisecond)
	h.send(display.Up, x, y)
	time.Sleep(80 * time.Millisecond)
	if n := h.disp.count("clear"); n != 0 {
		t.Errorf("the hold on the bar cleared the screen (%d)", n)
	}
	if lg := h.readLog(); strings.Contains(lg, "hot corner") {
		t.Errorf("a hot corner ran:\n%s", tail(lg, 400))
	}
	h.stop()
}

// Short lists keep the old rows, with or without a big library next to them.
func TestShortListsKeepTheOldView(t *testing.T) {
	h := startOpts(t, hopts{big: 900})
	h.waitTag("list", 1)
	s := h.listState()
	if s.compact || s.bar {
		t.Errorf("home: compact=%v bar=%v", s.compact, s.bar)
	}
	h.tapName("row-5")
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "All Libraries" && s.ListRows >= 4 }, "libraries")
	h.tapName("row-0") // Manga A: 25 series
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Manga A" && s.ListRows > 0 }, "Manga A")
	time.Sleep(100 * time.Millisecond)
	s = h.listState()
	if s.compact || s.bar || s.perPage > 12 {
		t.Errorf("25 series: compact=%v bar=%v rows a page=%d", s.compact, s.bar, s.perPage)
	}
	h.stop()
}

// Settings: Comfortable on the big list, bar Off, back to Auto.
func TestListSettingsSwitchTheView(t *testing.T) {
	h := startOpts(t, hopts{big: 900})
	h.openBig()
	h.waitBigLoaded(900)
	set := func(row string) {
		h.tapName("settings")
		h.waitScreen("settings")
		h.settingsScrollTo(row)
		h.tapName(row)
		h.tapName("back")
		h.waitFor(func() bool { return h.snapshot().Screen == "list" }, "back in the list")
		time.Sleep(60 * time.Millisecond)
	}
	set("lists-comfortable")
	s := h.listState()
	if s.compact || s.perPage > 12 || !s.bar {
		t.Errorf("comfortable: compact=%v rows=%d bar=%v", s.compact, s.perPage, s.bar)
	}
	set("alpha-off")
	if s = h.listState(); s.bar {
		t.Error("the bar is still there with Alphabet bar Off")
	}
	set("lists-auto")
	set("alpha-auto")
	s = h.listState()
	if !s.compact || !s.bar {
		t.Errorf("auto again: compact=%v bar=%v", s.compact, s.bar)
	}
	h.waitFor(func() bool {
		st, _ := store.LoadSettings(h.setFile, store.DefaultConfig())
		return st.Lists == store.ListsAuto && st.Alpha == store.AlphaAuto
	}, "saved")
	h.stop()
}

// The fill: "Loading list k/N" in the footer while the pages arrive, repaints no
// faster than every 2 s, and a final one.
func TestFillShowsProgressAndIsThrottled(t *testing.T) {
	h := startOpts(t, hopts{big: 900, delay: 45 * time.Millisecond})
	start := time.Now()
	h.openBig()
	var label string
	h.waitFor(func() bool {
		h.onUI(func() {
			lv := h.app.top()
			if lv.filling && lv.loading {
				label = lv.fillLabel()
			}
		})
		return label != ""
	}, "a loading label")
	if !regexp.MustCompile(`^Loading list \d+/45$`).MatchString(label) {
		t.Errorf("label %q", label)
	}
	h.waitBigLoaded(900)
	took := time.Since(start)
	prog := h.disp.count("list-progress")
	if limit := int(took/progressEvery) + 2; prog > 2*limit { // two rectangles per repaint
		t.Errorf("%d progress updates in %v", prog, took)
	}
	t.Logf("45 pages in %v with 45 ms per request; %d progress updates", took, prog)
	h.stop()
}

// Auto fills only a list that stays alphabetical; On fills anything.
func TestAutoDoesNotFillAnUnorderedListButOnDoes(t *testing.T) {
	h := startOpts(t, hopts{big: 300, bigRaw: true})
	h.openBig()
	h.waitFor(func() bool { s := h.listState(); return s.rows >= 20 }, "first page")
	time.Sleep(400 * time.Millisecond)
	s := h.listState()
	time.Sleep(400 * time.Millisecond)
	s2 := h.listState()
	// not filled in the background: it only grows as far as the lazy read-ahead
	// of the next page needs, and it is stable
	if s.rows >= 300 || s2.rows != s.rows || s.filling || s.bar {
		t.Errorf("unordered list in auto: rows=%d then %d filling=%v bar=%v", s.rows, s2.rows, s.filling, s.bar)
	}
	h.stop()

	h = startOpts(t, hopts{big: 300, bigRaw: true, mutSet: func(st *store.Settings) { st.Alpha = store.AlphaOn }})
	h.openBig()
	h.waitBigLoaded(300)
	s = h.listState()
	if !s.bar || s.rows != 300 {
		t.Errorf("alpha on: rows=%d bar=%v", s.rows, s.bar)
	}
	// the jump goes to the first entry of the letter wherever it is
	h.tapName("alpha-P")
	h.waitFor(func() bool { return h.listState().scroll == s.first["P"] }, "jump to the first P")
	h.stop()
}

// A list that was loaded in full is shown at once the next time, with one
// request for page 1 to see whether it changed, and no repaint when it did not.
func TestSessionCacheReusesTheIndexWithAStalenessCheck(t *testing.T) {
	h := startOpts(t, hopts{big: 900})
	h.openBig()
	h.waitBigLoaded(900)
	h.tapName("home")
	time.Sleep(150 * time.Millisecond)
	reqs := len(h.mock.Requests())
	h.tapName("row-5")
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "All Libraries" && s.ListRows >= 4 }, "libraries")
	h.tapName("row-3")
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Big Library" && s.ListRows == 900 }, "the big library at once")
	n := len(h.disp.all())
	time.Sleep(300 * time.Millisecond)
	feeds := 0
	for _, p := range h.mock.Requests()[reqs:] {
		if strings.Contains(p, "/libraries/4") {
			feeds++
		}
	}
	if feeds != 1 {
		t.Errorf("%d requests for the big library, want exactly the one that checks page 1", feeds)
	}
	if extra := len(h.disp.all()) - n; extra != 0 {
		t.Errorf("the staleness check repainted the screen %d times though nothing changed", extra)
	}
	h.stop()
}

// The server's list changed: the cached copy is dropped and loaded again.
func TestSessionCacheIsDroppedWhenTheServerChanged(t *testing.T) {
	h := startOpts(t, hopts{big: 100})
	h.openBig()
	h.waitBigLoaded(100)
	h.onUI(func() {
		for _, e := range h.app.listCache {
			e.total += 5 // as if the server had five series more than when it was cached
		}
	})
	h.tapName("home")
	time.Sleep(150 * time.Millisecond)
	h.tapName("row-5")
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "All Libraries" && s.ListRows >= 4 }, "libraries")
	h.tapName("row-3")
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Big Library" && s.ListRows > 0 }, "list")
	h.waitBigLoaded(100)
	h.waitFor(func() bool {
		var cached int
		h.onUI(func() {
			for _, e := range h.app.listCache {
				cached = e.total
			}
		})
		return cached == 100
	}, "the cache entry replaced after a reload")
	h.stop()
}

// How long the whole index takes: 1000 series are 50 catalogue pages, fetched
// one after the other. Logged for the notes; the 150 ms case (a slow link)
// runs with RMK_BENCH=1.
func TestIndexLoadTime(t *testing.T) {
	cases := []time.Duration{0}
	if os.Getenv("RMK_BENCH") != "" {
		cases = append(cases, 150*time.Millisecond)
	}
	for _, d := range cases {
		h := startOpts(t, hopts{big: 1000, delay: d})
		start := time.Now()
		h.openBig()
		h.waitBigLoaded(1000)
		t.Logf("1000 series, %v per request: the whole index in %v (usable after the first page)", d, time.Since(start).Round(time.Millisecond))
		h.stop()
	}
}
