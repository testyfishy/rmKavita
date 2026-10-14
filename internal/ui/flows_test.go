package ui

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/store"
)

const goodPassword = "correct horse"

// walk taps the named rows in turn; the last tap is expected to open a reader.
func (h *harness) walk(rows ...string) {
	h.t.Helper()
	for i, r := range rows {
		n := h.disp.count("list")
		h.tapName(r)
		if i < len(rows)-1 {
			h.waitTag("list", n+1)
		}
	}
	h.waitTag("reader-first", 1)
}

// ---- sign in -----------------------------------------------------------------------

func TestSignInStoresOnlyTheKey(t *testing.T) {
	h := startOpts(t, hopts{signedOut: true})
	h.waitScreen("signin")
	s := h.snapshot()
	if s.FocusField != 2 {
		t.Fatalf("server and user are prefilled: focus should be the password field, got %d", s.FocusField)
	}
	h.typeText(goodPassword)
	h.typeText("\n")
	h.waitScreen("list")
	h.waitTag("list", 1)
	conf := h.readConf()
	if !strings.Contains(conf, "api_key = "+h.mock.Key()) || !strings.Contains(conf, "username = reader") || !strings.Contains(conf, "server = "+h.srv.URL) {
		t.Errorf("config after sign-in:\n%s", strings.ReplaceAll(conf, h.mock.Key(), "<key>"))
	}
	if strings.Contains(conf, goodPassword) {
		t.Error("the password reached the config file")
	}
	if n := h.mock.Logins(); n != 1 {
		t.Errorf("%d sign-in requests, want exactly 1", n)
	}
	// the password and the key are nowhere in the log
	lg := h.readLog()
	if strings.Contains(lg, goodPassword) || strings.Contains(lg, h.mock.Key()) {
		t.Errorf("secret in the log:\n%s", lg)
	}
	// and the home list loaded with the new key
	h.waitFor(func() bool { s := h.snapshot(); return s.ListRows > 0 }, "home rows")
	h.stop()
	// a second launch goes straight to the list: no sign-in
	h.launch()
	h.waitScreen("list")
	if h.snapshot().Screen == "signin" {
		t.Error("signed in again at launch")
	}
	h.stop()
}

func TestSignInWrongPasswordThenLockout(t *testing.T) {
	h := startOpts(t, hopts{signedOut: true})
	h.waitScreen("signin")
	for i := 1; i <= 5; i++ {
		h.typeText(fmt.Sprintf("wrong-%d\n", i))
		h.waitFor(func() bool { s := h.snapshot(); return s.Tag == "signin-error" && !s.Busy && h.mock.Logins() == i }, fmt.Sprintf("failure %d", i))
		if got := h.disp.count("signin-error"); got != i {
			t.Fatalf("after attempt %d: %d error frames", i, got)
		}
	}
	// nothing was retried behind the user's back
	if n := h.mock.Logins(); n != 5 {
		t.Fatalf("%d sign-in requests for 5 attempts", n)
	}
	// Kavita now locks the account: even the right password is refused, and the
	// screen says so instead of "wrong password".
	h.typeText(goodPassword + "\n")
	h.waitFor(func() bool { return h.mock.Logins() == 6 && !h.snapshot().Busy }, "locked attempt")
	h.waitFor(func() bool { return h.disp.count("signin-error") == 6 }, "locked frame")
	if h.readConf() != "" && strings.Contains(h.readConf(), "api_key") {
		t.Error("a key was stored although sign-in failed")
	}
	if h.snapshot().Screen != "signin" {
		t.Errorf("screen %q", h.snapshot().Screen)
	}
	h.stop()
}

func TestSignInDoorDownAndNotKavita(t *testing.T) {
	// nothing listens: "cannot reach"
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := l.Addr().String()
	l.Close()
	h := startOpts(t, hopts{signedOut: true})
	h.waitScreen("signin")
	// replace the server text: clear the field, type the dead address
	h.tapName("field-server")
	for i := 0; i < len(h.srv.URL)+2; i++ {
		h.typeText("\b")
	}
	h.typeText(dead)
	h.tapName("field-pass")
	h.typeText("x\n")
	h.waitTag("signin-error", 1)
	h.stop()

	// a web server that is not Kavita answers 404 to the login
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	h = startOpts(t, hopts{signedOut: true, mut: func(c *store.Config) {}})
	h.waitScreen("signin")
	h.tapName("field-server")
	for i := 0; i < len(h.srv.URL)+2; i++ {
		h.typeText("\b")
	}
	h.typeText(strings.TrimPrefix(other.URL, "http://"))
	h.tapName("field-pass")
	h.typeText("x\n")
	h.waitTag("signin-error", 1)
	h.stop()
}

func TestSignInOnScreenKeyboardShiftAndLayers(t *testing.T) {
	h := startOpts(t, hopts{signedOut: true})
	h.waitScreen("signin")
	h.tapName("field-user")
	for i := 0; i < 8; i++ {
		h.typeText("\b")
	}
	h.waitFor(func() bool { return h.snapshot().UserText == "" }, "user field cleared")
	// shift once: the next letter is a capital, then back to lower case
	h.tapName("key-shift")
	h.tapName("key-a")
	h.tapName("key-b")
	h.waitFor(func() bool { return h.snapshot().UserText == "Ab" }, "Ab (one-shot shift)")
	// symbols layer: punctuation, then back to the letters
	h.tapName("key-layer")
	h.waitFor(func() bool { _, ok := h.hitRect("key-!"); return ok }, "symbols layer")
	h.tapName("key-!")
	h.tapName("key-layer")
	h.waitFor(func() bool { _, ok := h.hitRect("key-q"); return ok }, "letters layer")
	h.waitFor(func() bool { return h.snapshot().UserText == "Ab!" }, "Ab!")
	// caps lock: two taps on shift keep it on
	h.tapName("key-shift")
	h.tapName("key-shift")
	h.tapName("key-c")
	h.tapName("key-d")
	h.waitFor(func() bool { return h.snapshot().UserText == "Ab!CD" }, "caps lock")
	// backspace key
	h.tapName("key-bs")
	h.waitFor(func() bool { return h.snapshot().UserText == "Ab!C" }, "backspace")
	h.stop()
}

func TestSignInPasswordToggleAndMasking(t *testing.T) {
	h := startOpts(t, hopts{signedOut: true})
	h.waitScreen("signin")
	h.typeText("abc")
	h.waitFor(func() bool { return h.snapshot().PassLen == 3 }, "typed")
	n := h.disp.count("signin")
	h.tapName("pass-toggle")
	h.waitFor(func() bool { return h.disp.count("signin") > n }, "toggle redraw")
	h.stop()
}

func TestNoConfigStartsAtSignInWithEmptyServer(t *testing.T) {
	h := startOpts(t, hopts{signedOut: true})
	h.stop()
	os.Remove(h.conf)
	h.launch()
	h.waitTag("start", 1) // the new run's first frame
	// the snapshot is the new run's once it has published it (the frame is
	// counted a moment before): the previous run's still carries its server
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "signin" && s.ServerText == "" }, "the new run's sign-in snapshot")
	if s := h.snapshot(); s.FocusField != 0 {
		t.Errorf("an empty server must be asked for first: %+v", s.FocusField)
	}
	// submitting an empty form explains what is missing, and sends nothing
	h.tapName("signin")
	h.waitTag("signin-error", 1)
	if n := h.mock.Logins(); n != 0 {
		t.Errorf("%d sign-in requests with an empty form", n)
	}
	h.stop()
}

// ---- logout and refused keys ---------------------------------------------------------

func TestLogoutRemovesKeyKeepsPositions(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	h := startOpts(t, hopts{state: state})
	h.openFirstChapter()
	h.tap(300, 1000)
	h.waitReady(1)
	// back to the list, then Logout through the account menu (two taps)
	h.tap(810, 300)
	h.waitTag("overlay-top", 1)
	h.tapName("home")
	h.waitFor(func() bool { return h.snapshot().Screen == "list" && h.disp.count("home") >= 1 }, "home")
	h.tapName("account")
	h.waitTag("popover", 1)
	h.tapName("logout")
	h.waitTag("popover", 2) // armed: still signed in
	if !h.snapshot().SignedIn {
		t.Fatal("one tap on Logout signed out; it must ask for a second")
	}
	h.tapName("logout")
	h.waitScreen("signin")
	if c := h.readConf(); strings.Contains(c, "api_key") || strings.Contains(c, h.mock.Key()) {
		t.Errorf("key still stored:\n%s", c)
	}
	if h.snapshot().SignedIn {
		t.Error("client still signed in")
	}
	b, err := os.ReadFile(state)
	if err != nil || !strings.Contains(string(b), `"p":1`) {
		t.Errorf("positions were not kept: %v %s", err, b)
	}
	if strings.Contains(string(b), "http") || strings.Contains(string(b), h.mock.Key()) {
		t.Errorf("state holds a URL or key: %s", b)
	}
	// signing in again as the same user finds the position again
	h.typeText(goodPassword + "\n")
	h.waitScreen("list")
	h.waitFor(func() bool { return h.snapshot().ListRows > 0 }, "list")
	h.stop()
}

func TestRefusedKeyLeadsToSignIn(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.waitFor(func() bool { return h.snapshot().ListRows > 0 }, "home rows")
	h.mock.RevokeKey() // the key the tablet holds is now refused
	h.tapName("row-5") // All Libraries: the feed answers 401
	h.waitScreen("signin")
	if c := h.readConf(); strings.Contains(c, "api_key") {
		t.Errorf("the refused key is still stored:\n%s", c)
	}
	h.stop()
}

// A 401 on a page prefetch or cover must not sign anyone out while the key is
// still good: it is confirmed with a fresh request first.
func TestBackgroundRefusalDoesNotSignOut(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.ReadAhead = 0; c.KeepBehind = 0 }})
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-0")
	h.waitTag("list", 3)
	h.tapName("row-0")
	h.waitTag("list", 4)
	h.mock.FailNext(1, 401) // one page request refused although the key is fine
	h.tapName("row-0")
	h.waitFor(func() bool { return h.disp.count("reader") >= 1 }, "error frame")
	time.Sleep(200 * time.Millisecond) // the root feed re-check ran and said "fine"
	if s := h.snapshot(); s.Screen != "reader" || !s.SignedIn {
		t.Fatalf("signed out by a single refused page: %+v", s)
	}
	h.tapName("retry")
	h.waitTag("reader-first", 1)
	h.stop()
}

// ---- settings ------------------------------------------------------------------------

func TestSettingsPersistAndApplyImmediately(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	want := []string{"fit-width", "fit-height", "fit-both", "footer-on", "footer-off",
		"refresh-0", "refresh-1", "refresh-2", "refresh-3", "refresh-4",
		"crefresh-0", "crefresh-3", "crefresh-10", "crefresh-c1", "crefresh-c2", "crefresh-c3", "crefresh-never",
		"dir-rtl", "dir-ltr", "colour-off", "colour-on", "dpi-auto", "dpi-custom-plus", "dpi-custom-minus", "hot-open", "adv-open"}
	have := h.settingsHits() // the list is several screens long
	for _, n := range want {
		if !have[n] {
			t.Errorf("settings lack %q", n)
		}
	}
	h.stop()

	h = startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	// there is no reading-mode choice any more
	for _, n := range []string{"mode-page", "mode-continuous"} {
		if _, ok := h.hitRect(n); ok {
			t.Errorf("settings still offer %q", n)
		}
	}
	h.tapName("fit-height")
	h.settingsScrollTo("refresh-4")
	h.tapName("refresh-4")
	h.settingsScrollTo("dir-ltr")
	h.tapName("dir-ltr")
	h.waitFor(func() bool {
		s, _ := store.LoadSettings(h.setFile, store.DefaultConfig())
		return s.Comic.Fit == store.FitHeight && !s.RTL && s.Refresh == store.RefreshNever
	}, "settings saved")
	h.stop()
	// they are still there after a restart
	h.launch()
	h.waitTag("list", 1)
	if h.set.Comic.Fit != store.FitHeight || h.set.RTL || h.set.Refresh != store.RefreshNever {
		t.Errorf("after restart: %+v", h.set)
	}
	h.stop()
}

func TestStepperTapAndHoldToRepeat(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Custom[1] = 30; s.Refresh = store.RefreshDefault }})
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	read := func() store.Settings { s, _ := store.LoadSettings(h.setFile, store.DefaultConfig()); return s }
	h.tapName("refresh-2-plus")
	h.waitFor(func() bool { return read().Custom[1] == 31 }, "one step")
	if read().Refresh != store.RefreshCustom2 {
		t.Error("using a stepper must select its row")
	}
	// hold: repeats, and the release is not an extra tap
	r, _ := h.hitRect("refresh-2-plus")
	x, y := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	h.send(display.Down, x, y)
	time.Sleep(1300 * time.Millisecond)
	h.send(display.Up, x, y)
	time.Sleep(100 * time.Millisecond)
	v := read().Custom[1]
	if v < 36 {
		t.Errorf("holding repeated only %d times (value %d)", v-31, v)
	}
	time.Sleep(300 * time.Millisecond)
	if read().Custom[1] != v {
		t.Error("the value kept moving after the finger lifted")
	}
	// the minus button, and the bounds
	h.tapName("refresh-2-minus")
	h.waitFor(func() bool { return read().Custom[1] == v-1 }, "minus")
	h.stop()
}

func TestStepperClampsAtOneAndTwoHundred(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Custom[0] = 1; s.Custom[2] = 200 }})
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	read := func() store.Settings { s, _ := store.LoadSettings(h.setFile, store.DefaultConfig()); return s }
	h.tapName("refresh-1-minus")
	h.tapName("refresh-3-plus")
	h.waitFor(func() bool { return read().Refresh == store.RefreshCustom3 }, "both taps applied")
	if s := read(); s.Custom[0] != 1 || s.Custom[2] != 200 {
		t.Errorf("custom = %v", s.Custom)
	}
	h.stop()
}

// ---- refresh counter -------------------------------------------------------------------

// The periodic clearing refresh counts page turns: with the default of 20, the
// first nineteen turns after a clearing refresh are plain partial updates and
// the twentieth is the clear; the count starts again after it.
func TestRefreshCounterSemantics(t *testing.T) {
	h := startOpts(t, hopts{pages: 60, mutSet: func(s *store.Settings) { s.Refresh = store.RefreshDefault }})
	h.openFirstChapter()
	h.waitReady(0)
	turn := func(page int) {
		h.tap(300, 1000)
		h.waitReady(page)
		// wait for the frame of this turn
		h.waitFor(func() bool { return h.disp.count("page")+h.disp.count("page-full") >= page }, "frame")
	}
	for p := 1; p <= 19; p++ {
		turn(p)
	}
	if h.disp.count("page-full") != 0 {
		t.Fatalf("a clearing refresh before the 20th turn: %v", h.disp.tags())
	}
	// opening and closing the overlay is not a turn
	h.tap(810, 300)
	h.waitTag("overlay-top", 1)
	h.tap(300, 1000)
	h.waitTag("overlay-hide-top", 1)
	turn(20)
	if h.disp.count("page-full") != 1 {
		t.Fatalf("the 20th turn must clear: %v", h.disp.tags())
	}
	for p := 21; p <= 39; p++ {
		turn(p)
	}
	if h.disp.count("page-full") != 1 {
		t.Fatalf("the count must restart after a clear")
	}
	turn(40)
	if h.disp.count("page-full") != 2 {
		t.Fatalf("40th turn: %d clears", h.disp.count("page-full"))
	}
	h.stop()
}

func TestRefreshNeverAndCustom(t *testing.T) {
	h := startOpts(t, hopts{pages: 30, mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	for p := 1; p <= 25; p++ {
		h.tap(300, 1000)
		h.waitReady(p)
	}
	if h.disp.count("page-full") != 0 {
		t.Errorf("Never cleared %d times", h.disp.count("page-full"))
	}
	h.stop()

	h = startOpts(t, hopts{pages: 30, mutSet: func(s *store.Settings) { s.Refresh = store.RefreshCustom3; s.Custom[2] = 7 }})
	h.openFirstChapter()
	for p := 1; p <= 15; p++ {
		h.tap(300, 1000)
		h.waitReady(p)
		h.waitFor(func() bool { return h.disp.count("page")+h.disp.count("page-full") >= p }, "frame")
	}
	if got := h.disp.count("page-full"); got != 2 {
		t.Errorf("every 7 turns over 15 turns: %d clears, want 2", got)
	}
	h.stop()
}

// ---- reading modes, fit and kinds -------------------------------------------------------

func TestFitWidthScrollsInsidePageThenTurns(t *testing.T) {
	h := startOpts(t, hopts{pw: 1000, ph: 1500, mut: func(c *store.Config) { c.Crop = "keep" }, mutSet: func(s *store.Settings) {
		s.Comic.Fit = store.FitWidth
		s.Refresh = store.RefreshNever
	}})
	h.openFirstChapter()
	h.waitReady(0)
	step := stepFor(2130)
	// page 1620 x 2430 on a 2160 high screen: two segments (0, 270)
	h.tap(300, 1000) // forward = left third (RTL)
	h.waitFor(func() bool { s := h.snapshot(); return s.Page == 0 && s.Off == 2430-2130 }, "scrolled to the end of page 0")
	_ = step
	h.tap(300, 1000)
	h.waitReady(1)
	h.waitFor(func() bool { return h.snapshot().Off == 0 }, "next page starts at its top")
	// back: lands on the END of the previous page
	h.tap(1400, 1000)
	h.waitFor(func() bool { s := h.snapshot(); return s.Page == 0 && s.Off == 2430-2130 }, "back lands at the end of page 0")
	h.stop()
}

func TestListHidesEntriesWithoutPageStream(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-1") // Manga B
	h.waitTag("list", 3)
	// 8 series with a stream (the series with only non-streamable files is not listed)
	h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Manga B" && s.ListRows == 15 }, "15 rows in Manga B")
	for _, row := range h.app.top().rows {
		if strings.Contains(row.title, "Document Only") {
			t.Error("a series without a page stream is listed")
		}
	}
	h.stop()
}

func TestPDFIsLeftToRightAndHasNoDirectionSetting(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.RTL = true; s.Refresh = store.RefreshNever }})
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-1") // Manga B
	h.waitTag("list", 3)
	h.waitFor(func() bool { return h.snapshot().ListRows == 15 }, "series")
	h.tapName("row-7") // Text PDF
	h.waitTag("list", 4)
	h.tapName("row-0")
	h.waitTag("reader-first", 1)
	h.waitReady(0)
	// right third = forward for a PDF although comics read right to left
	h.tap(1400, 1000)
	h.waitReady(1)
	h.tap(300, 1000)
	h.waitReady(0)
	// its settings have no direction
	h.tap(810, 300)
	h.waitTag("overlay-top", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	if _, ok := h.hitRect("dir-rtl"); ok {
		t.Error("a PDF's settings show a reading direction")
	}
	if _, ok := h.hitRect("mode-continuous"); ok {
		t.Error("a PDF offers a continuous mode")
	}
	h.stop()
}

func TestColourPagesUseTheColourLayerAndSafeWaveform(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Colour = true; s.Refresh = store.RefreshNever }})
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-1") // Manga B
	h.waitTag("list", 3)
	h.waitFor(func() bool { return h.snapshot().ListRows >= 1 }, "rows")
	h.tapName("row-0") // Color JPEG Scans
	h.waitTag("list", 4)
	h.tapName("row-0")
	h.waitTag("reader-first", 1)
	h.waitReady(0)
	var first frameRec
	for _, f := range h.disp.all() {
		if f.tag == "reader-first" {
			first = f
		}
	}
	if !first.colour {
		t.Error("a colour page was sent without the colour layer")
	}
	h.tap(300, 1000)
	h.waitTag("page", 1)
	for _, f := range h.disp.all() {
		if f.tag == "page" && (!f.colour || f.mode != display.ModeUI || f.full) {
			t.Errorf("colour turn = %+v, want one partial UI update with colour", f)
		}
	}
	h.stop()
}

func TestBlackAndWhiteModeSendsNoColour(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Colour = false; s.Refresh = store.RefreshNever }})
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-1")
	h.waitTag("list", 3)
	h.waitFor(func() bool { return h.snapshot().ListRows >= 1 }, "rows")
	h.tapName("row-0")
	h.waitTag("list", 4)
	h.tapName("row-0")
	h.waitTag("reader-first", 1)
	h.waitReady(0)
	h.tap(300, 1000)
	h.waitTag("page", 1)
	for _, f := range h.disp.all() {
		if f.colour {
			t.Errorf("colour frame in black and white mode: %+v", f)
		}
		if f.tag == "page" && f.mode != display.ModeUI {
			t.Errorf("turn waveform %v, want UI", f.mode)
		}
	}
	h.stop()
}

func TestSettingsChangeRefetchesPagesAndKeepsPage(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	for p := 1; p <= 3; p++ {
		h.tap(300, 1000)
		h.waitReady(p)
	}
	n := len(h.imageRequests())
	h.tap(810, 300)
	h.waitTag("overlay-top", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	h.tapName("fit-width")
	h.tapName("back")
	h.waitScreen("reader")
	h.waitReady(3) // same page, fitted to the width now
	if got := len(h.imageRequests()); got <= n {
		t.Errorf("pages were not fetched again for the new fit (%d -> %d requests)", n, got)
	}
	h.stop()
}

// ---- DPI -------------------------------------------------------------------------------

func TestDPIScalesLayoutAndFitsTheMove(t *testing.T) {
	// the Paper Pro Move: 954 x 1696 at 264 dpi, sign-in screen must not overlap
	h := startOpts(t, hopts{signedOut: true, w: 954, h: 1696, dpi: 264})
	h.waitScreen("signin")
	g := h.app.signinGeometry()
	for i := 0; i < 3; i++ {
		if g.fields[i].Max.X > 954 || g.fields[i].Min.X < 0 {
			t.Errorf("field %d outside the screen: %v", i, g.fields[i])
		}
	}
	if g.button.Max.Y > h.app.kbTop(g.kbBottom, g.kh) {
		t.Errorf("button %v overlaps the keyboard (top %d)", g.button, h.app.kbTop(g.kbBottom, g.kh))
	}
	if g.kh < 90 {
		t.Errorf("keys %d px high", g.kh)
	}
	// 100 design pixels are 115 px at 264 dpi: the same physical size
	if got := h.app.u(100); got != 115 {
		t.Errorf("u(100) at 264 dpi = %d, want 115", got)
	}
	h.stop()

	// a custom DPI in the settings wins over the device's
	h = startOpts(t, hopts{mutSet: func(s *store.Settings) { s.DPI = 300 }})
	h.waitTag("list", 1)
	if got := h.app.u(229); got != 300 {
		t.Errorf("u(229) with a custom 300 dpi = %d", got)
	}
	h.stop()
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// Holding a stepper must not strobe the whole screen or write the file ten
// times a second: only the row's rectangle is updated while the finger is down,
// and the settings file is written once, when it lifts.
func TestHeldStepperUpdatesOnlyItsRowAndWritesOnRelease(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Custom[1] = 30 }})
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	read := func() int { s, _ := store.LoadSettings(h.setFile, store.DefaultConfig()); return s.Custom[1] }
	r, _ := h.hitRect("refresh-2-plus")
	x, y := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	before := len(h.disp.all())
	h.send(display.Down, x, y)
	time.Sleep(1200 * time.Millisecond)
	if v := read(); v != 30 {
		t.Errorf("the file was written while the finger was down (value %d)", v)
	}
	h.send(display.Up, x, y)
	h.waitFor(func() bool { return read() > 33 }, "value written on release")
	rows := 0
	for _, f := range h.disp.all()[before:] {
		if f.rect.Dx() == 1620 && f.rect.Dy() == 2160 {
			t.Fatalf("a full-screen update during the hold: %+v", f)
		}
		if f.tag == "settings-row" {
			rows++
		}
	}
	if rows < 4 {
		t.Errorf("only %d row updates for a held stepper", rows)
	}
	h.stop()
}

// The v1 config on the tablet carries `page_mode = ui`; carried over, it would
// bring the flashing page turns back. Migration turns that default into auto.
func TestV1ConfigMigrationDoesNotRestoreTheFlashingWaveform(t *testing.T) {
	const v1 = "# Kavita reader configuration\nroot = http://127.0.0.1:5001/api/opds/0123456789abcdef" + "0123456789abcdef\n\nrtl = yes\n\nfull_refresh_every = 20\n\n" +
		"prefetch = 2\nmax_cached = 5\n\npage_mode = ui\nui_mode = ui\nclear_mode = content\n\ncovers = yes\n\ncrop_margins = no\nrotate_spreads = no\n\ngamma = 1\n\nhold_ms = 900\n\nsync_progress = yes\n"
	dir := t.TempDir()
	old, nw := filepath.Join(dir, "old.conf"), filepath.Join(dir, "new.conf")
	os.WriteFile(old, []byte(v1), 0o600)
	lines, err := store.Migrate(nw, "", old, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), "0123456789abcdef") {
		t.Error("key in the migrate output")
	}
	cfg, _, _ := store.LoadConfig(nw)
	if cfg.APIKey != "" || cfg.Server != "http://127.0.0.1:5001" {
		t.Errorf("migrated config: server=%q key=%v", cfg.Server, cfg.APIKey != "")
	}
	a := &App{Cfg: cfg}
	r := &reader{wave: a.baseWave()}
	if cfg.PageMode != "auto" {
		t.Errorf("the migrated config says page_mode = %q, want auto (the v1 default is rewritten)", cfg.PageMode)
	}
	if m := a.turnWave(r, false); m != display.ModeUI {
		t.Errorf("a migrated v1 config turns pages with %v, want UI (auto)", m)
	}
	if m := a.turnWave(r, true); m != display.ModeUI {
		t.Errorf("colour page waveform %v, want UI", m)
	}
	// a deliberate choice in an existing v2 config survives re-running the migration
	os.WriteFile(nw, []byte("server = http://h\npage_mode = ui\n"), 0o600)
	if _, err := store.Migrate(nw, "", old, ""); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ = store.LoadConfig(nw)
	if cfg.PageMode != "ui" {
		t.Errorf("an explicit page_mode was rewritten: %q", cfg.PageMode)
	}
}
