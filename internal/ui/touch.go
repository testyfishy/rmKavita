package ui

import (
	"fmt"
	"image"
	"time"

	"rmkavita/internal/display"
)

type hit struct {
	r      image.Rectangle
	fn     func()
	fnxy   func(x, y int)
	name   string // for tests and for the repeat lookup
	repeat bool   // holding the finger down repeats fn (steppers, backspace)
}

type corner int

const (
	cornerNone corner = iota
	cornerTL
	cornerTR
	cornerBL
	cornerBR
)

type touchState struct {
	alpha     bool // the press began on the A-Z bar: it is a letter pick, never a swipe
	active    bool
	id        int
	x0, y0    int
	x, y      int
	ax, ay    int       // the last position that counted as movement (jitter below stillPx does not)
	t0        time.Time // when the press began
	lastMove  time.Time // the last significant movement
	moved     bool
	edge      bool // began within palmEdgePx of the left or right edge: a palm candidate
	holdFired bool
	repName   string // name of the repeat button under the finger ("" = none)
	repN      int    // repeats fired so far
	repFired  bool
}

// openContact is a finger the panel reported down and not yet up. The table is
// bookkeeping for the log and for palm handling: a contact that never gets its
// release (a thumb resting on the bezel, a lost message) must not matter to the
// next touch.
type openContact struct {
	since time.Time
	x, y  int
	edge  bool // began at the screen edge
	palm  bool // classed as a resting palm or thumb: ignored until its release
}

// touchStats count what the tracker did with the input it received. They go
// into the log (every telemetry line and the exit totals), so that if the app
// ever stops answering, "no events arrived" and "events arrived and were
// ignored" can be told apart.
type touchStats struct {
	down, up, move int // events the UI goroutine consumed
	started        int // gestures begun by a Down
	completed      int // gestures that ended with their own Up
	cancelled      int // presses abandoned without acting on them
	superseded     int // of those: ended by a newer Down
	palm           int // contacts classed as a resting palm / thumb
	ignoredDown    int // presses not taken as a gesture (second finger while one is swiping, edge contact during a fresh press)
	ignoredUp      int // releases that matched no press
	ignoredMove    int // moves that matched no press
}

// Palm and stale-contact handling.
const (
	palmAfter      = 1200 * time.Millisecond // stationary this long with no release: a resting palm or thumb
	palmEdgePx     = 60                      // a contact starting this close to the left/right edge is a palm candidate
	stillPx        = 12                      // movement below this is jitter, not a finger moving
	activeMoveFor  = 250 * time.Millisecond  // a finger that moved this recently is "actively moving"
	openContactTTL = 10 * time.Second        // an unreleased contact is forgotten after this
	maxOpen        = 8
	// maxRepeatPress bounds a held repeat button (steppers, backspace) whose
	// release was lost: it keeps repeating for at most this long.
	maxRepeatPress = 15 * time.Second
)

// pressed reports whether the tracker believes a finger is down.
func (a *App) pressed() bool { return a.touch.active }

func (a *App) palmDelay() time.Duration {
	if a.palmAfter > 0 {
		return a.palmAfter
	}
	return palmAfter
}

// armTouchTimer (re)starts the classification timer of the active press.
func (a *App) armTouchTimer(d time.Duration) {
	a.touchGen++
	if !a.touchT.Stop() {
		select {
		case <-a.touchT.C:
		default:
		}
	}
	a.touchT.Reset(d)
}

func (a *App) stopTouchTimer() {
	a.touchGen++
	if !a.touchT.Stop() {
		select {
		case <-a.touchT.C:
		default:
		}
	}
}

// expireContacts forgets contacts that have been down for longer than
// openContactTTL (their release will not matter any more) and keeps the table
// small.
func (a *App) expireContacts(now time.Time) {
	for id, c := range a.open {
		if now.Sub(c.since) > openContactTTL {
			delete(a.open, id)
		}
	}
	for len(a.open) > maxOpen {
		oldest, oid := now, -1
		for id, c := range a.open {
			if c.since.Before(oldest) {
				oldest, oid = c.since, id
			}
		}
		if oid < 0 {
			break
		}
		delete(a.open, oid)
	}
}

// resetPress clears the gesture state without logging.
func (a *App) resetPress() {
	a.stopHold()
	a.stopRepeat()
	a.stopTouchTimer()
	onBar := a.touch.alpha
	a.touch = touchState{}
	if onBar {
		a.alphaClearHint() // the outline of a pick that was let go of
	}
}

// cancelPress abandons the press in progress without acting on it: no tap, no
// hold, no repeat. One log line says why, so a stuck press is visible.
func (a *App) cancelPress(why string) {
	t := &a.touch
	if !t.active {
		return
	}
	a.logf("input: press id=%d at %d,%d force-cancelled after %d ms (%s); open contacts %d; consumed d=%d u=%d m=%d",
		t.id, t.x0, t.y0, time.Since(t.t0).Milliseconds(), why, len(a.open), a.ts.down, a.ts.up, a.ts.move)
	a.ts.cancelled++
	a.resetPress()
}

// onTouchStale runs when the active press has been stationary for the
// classification time without a release.
func (a *App) onTouchStale(gen int) {
	t := &a.touch
	if gen != a.touchGen || !t.active {
		return
	}
	age := time.Since(t.t0)
	switch {
	case t.holdFired:
		// a hold that did its job and whose release has not come: just let go of it
		a.resetPress()
		return
	case t.repName != "" && t.repFired && age < maxRepeatPress:
		a.armTouchTimer(a.palmDelay()) // a stepper that is repeating is a held finger, not a lost one
		return
	}
	// Stationary for palmAfter with no release: a thumb or palm resting on the
	// glass (or a release that never came). It must not block, arm a hold or
	// count towards anything until its release arrives.
	if c := a.open[t.id]; c != nil {
		c.palm = true
	}
	a.ts.palm++
	a.ts.cancelled++
	a.logf("input: contact id=%d at %d,%d classed as a resting palm/thumb (stationary %d ms, edge=%v); ignored until its release; open contacts %d",
		t.id, t.x0, t.y0, age.Milliseconds(), t.edge, len(a.open))
	a.resetPress()
}

// inputSuffix is the part of every telemetry line that carries the input
// counters: panel events received d/u/m and shed, gestures started /
// completed / cancelled, whether a press is believed to be down, the contacts
// that are down without a release, and how many were classed as palms.
func (a *App) inputSuffix() string {
	var c display.InputCounters
	if ic, ok := a.Disp.(display.InputCounted); ok {
		c = ic.InputCounters()
	}
	p := 0
	if a.pressed() {
		p = 1
	}
	a.expireContacts(time.Now())
	return fmt.Sprintf(" in=%d/%d/%d/%d g=%d/%d/%d pr=%d open=%d palm=%d", c.Down, c.Up, c.Move, c.Dropped,
		a.ts.started, a.ts.completed, a.ts.cancelled, p, len(a.open), a.ts.palm)
}

// inputTotals is the exit line: the same counters, spelled out.
func (a *App) inputTotals() string {
	var c display.InputCounters
	if ic, ok := a.Disp.(display.InputCounted); ok {
		c = ic.InputCounters()
	}
	a.expireContacts(time.Now())
	return fmt.Sprintf("input totals: received down=%d up=%d move=%d coalesced=%d dropped=%d | consumed down=%d up=%d move=%d | gestures started=%d completed=%d cancelled=%d (superseded=%d palm=%d) | ignored down=%d up=%d move=%d | pressed=%v open=%d",
		c.Down, c.Up, c.Move, c.Coalesced, c.Dropped, a.ts.down, a.ts.up, a.ts.move,
		a.ts.started, a.ts.completed, a.ts.cancelled, a.ts.superseded, a.ts.palm, a.ts.ignoredDown, a.ts.ignoredUp, a.ts.ignoredMove, a.pressed(), len(a.open))
}

func cornerName(c corner) string {
	switch c {
	case cornerTL:
		return "top-left"
	case cornerTR:
		return "top-right"
	case cornerBL:
		return "bottom-left"
	case cornerBR:
		return "bottom-right"
	}
	return ""
}

func (a *App) cornerAt(x, y int) corner {
	z := a.u(260)
	left, right := x < z, x >= a.W-z
	top, bot := y < z, y >= a.H-z
	switch {
	case left && top:
		return cornerTL
	case right && top:
		return cornerTR
	case left && bot:
		return cornerBL
	case right && bot:
		return cornerBR
	}
	return cornerNone
}

func (a *App) addHit(r image.Rectangle, fn func()) { a.hits = append(a.hits, hit{r: r, fn: fn}) }

func (a *App) addNamedHit(name string, r image.Rectangle, fn func()) {
	a.hits = append(a.hits, hit{r: r, fn: fn, name: name})
}

func (a *App) addNamedRepeatHit(name string, r image.Rectangle, fn func()) {
	a.hits = append(a.hits, hit{r: r, fn: fn, name: name, repeat: true})
}

func (a *App) addNamedHitXY(name string, r image.Rectangle, fn func(x, y int)) {
	a.hits = append(a.hits, hit{r: r, fnxy: fn, name: name})
}

func (a *App) stopHold() {
	a.holdGen++
	if !a.holdT.Stop() {
		select {
		case <-a.holdT.C:
		default:
		}
	}
}

func (a *App) stopRepeat() {
	if !a.repT.Stop() {
		select {
		case <-a.repT.C:
		default:
		}
	}
}

func dist2(x0, y0, x1, y1 int) int {
	dx, dy := x1-x0, y1-y0
	return dx*dx + dy*dy
}

// repeatHitAt finds the repeat button under a point.
func (a *App) repeatHitAt(x, y int) *hit {
	for i := len(a.hits) - 1; i >= 0; i-- {
		h := &a.hits[i]
		if image.Pt(x, y).In(h.r) {
			if h.repeat {
				return h
			}
			return nil
		}
	}
	return nil
}

func (a *App) onEvent(ev display.Event) {
	if ev.Kind == display.Key {
		a.typeText(ev.Text)
		return
	}
	a.onTouch(ev)
}

// onTouch is the gesture tracker. It is built so that no sequence of events
// can leave it unable to answer the next touch:
//   - a Down starts a fresh gesture whatever state the tracker is in (a lost
//     release, a resting thumb, a different finger id): the newest press wins.
//     Only two things keep a new Down from becoming the gesture: the first
//     finger is actively moving (a swipe), or the new contact begins at the
//     screen edge while a fresh press is in progress (a palm landing);
//   - a contact that is stationary for palmAfter with no release is classed as
//     a resting palm or thumb and let go of: it blocks nothing, arms no hold;
//   - a release or move that matches no press is ignored without side effects;
//   - the panel's read loop never waits for this goroutine.
func (a *App) onTouch(ev display.Event) {
	t := &a.touch
	now := time.Now()
	switch ev.Kind {
	case display.Down:
		a.ts.down++
		a.clrT.Stop() // a deferred clearing refresh waits for the reader to pause
		a.expireContacts(now)
		// a contact at the left or right edge is a palm candidate, except in a
		// corner zone: there a deliberate hold is the whole point
		edge := (ev.X < palmEdgePx || ev.X >= a.W-palmEdgePx) && a.cornerAt(ev.X, ev.Y) == cornerNone
		oc := &openContact{since: now, x: ev.X, y: ev.Y, edge: edge}
		a.open[ev.ID] = oc
		if t.active {
			switch {
			case t.moved && now.Sub(t.lastMove) < activeMoveFor:
				a.ts.ignoredDown++ // the first finger is swiping: a second contact is not a new gesture
				oc.palm = true
				return
			case edge && !t.edge && now.Sub(t.t0) < a.palmDelay():
				a.ts.ignoredDown++ // a palm landing at the edge must not steal a fresh press
				oc.palm = true
				return
			}
			a.ts.superseded++
			a.cancelPress(fmt.Sprintf("superseded by a newer Down id=%d", ev.ID))
		}
		a.stopHold()
		a.stopRepeat()
		*t = touchState{active: true, id: ev.ID, x0: ev.X, y0: ev.Y, x: ev.X, y: ev.Y, ax: ev.X, ay: ev.Y, t0: now, lastMove: now, edge: edge}
		t.alpha = a.alphaHit(ev.X, ev.Y)
		a.ts.started++
		delay := a.palmDelay()
		if c := a.cornerAt(ev.X, ev.Y); c != cornerNone && !edge && !t.alpha && a.holdWanted(c) {
			hold := time.Duration(a.Cfg.HoldMs) * time.Millisecond
			a.holdT.Reset(hold)
			if hold+400*time.Millisecond > delay {
				delay = hold + 400*time.Millisecond // a deliberate hold is stationary by design
			}
		}
		if h := a.repeatHitAt(ev.X, ev.Y); h != nil {
			t.repName = h.name
			a.repT.Reset(450 * time.Millisecond)
		}
		a.armTouchTimer(delay)
	case display.Move:
		a.ts.move++
		if !t.active || ev.ID != t.id {
			a.ts.ignoredMove++
			return
		}
		t.x, t.y = ev.X, ev.Y
		if t.alpha {
			a.alphaMove(ev.Y)
		}
		if dist2(t.ax, t.ay, ev.X, ev.Y) >= stillPx*stillPx {
			t.ax, t.ay, t.lastMove = ev.X, ev.Y, now
			if t.repName == "" && !t.holdFired {
				a.armTouchTimer(a.palmDelay()) // the finger is alive: classification starts over
			}
		}
		slop := a.u(40)
		if !t.moved && dist2(t.x0, t.y0, ev.X, ev.Y) > slop*slop {
			t.moved = true
			a.stopHold()
			a.stopRepeat()
		}
	case display.Up:
		a.ts.up++
		delete(a.open, ev.ID)
		if !t.active || ev.ID != t.id {
			a.ts.ignoredUp++
			return
		}
		a.ts.completed++
		a.inAt = ev.At
		if a.inAt.IsZero() {
			a.inAt = now
		}
		a.stopHold()
		a.stopRepeat()
		a.stopTouchTimer()
		t.x, t.y = ev.X, ev.Y
		fired := t.holdFired || t.repFired
		repeated := t.repFired
		x0, y0, x, y := t.x0, t.y0, t.x, t.y
		onBar := t.alpha
		*t = touchState{}
		if repeated {
			a.flushSettings() // a held stepper is written once, when the finger lifts
		}
		if fired {
			return
		}
		if onBar {
			a.alphaRelease(y) // a letter pick: the letter under the finger when it lifts
			return
		}
		a.gesture(x0, y0, x, y)
	}
}

// onHold runs when a corner has been held long enough: it does what the
// corner is set to (Settings -> Hot corners; defaults: top-right clears the
// screen, bottom-left quits).
func (a *App) onHold(gen int) {
	t := &a.touch
	if gen != a.holdGen || !t.active || t.holdFired || t.moved {
		return
	}
	c := a.cornerAt(t.x0, t.y0)
	act := a.hotAction(c)
	if act == hotNone {
		return
	}
	t.holdFired = true
	a.runHot(act, "hold "+cornerName(c)+" corner")
}

// onRepeat fires the repeat button under a held finger and re-arms itself,
// faster after a while.
func (a *App) onRepeat() {
	t := &a.touch
	if !t.active || t.moved || t.repName == "" {
		return
	}
	h := a.repeatHitAt(t.x0, t.y0)
	if h == nil || h.name != t.repName {
		return
	}
	t.repFired = true
	t.repN++
	fn := h.fn
	fn()
	if a.quitting || !t.active {
		return
	}
	d := 170 * time.Millisecond
	if t.repN > 10 {
		d = 90 * time.Millisecond
	}
	a.repT.Reset(d)
}

// topBand is the strip at the top of the screen from which a downward swipe
// reveals the chrome while reading. It starts below AppLoad's own top-bar drag
// (press at y < 100, release at y 100-400 shows its bar) and ends at 260 design
// pixels.
func (a *App) topBand() int { return a.u(260) }

// revealMinY is the first y of the reveal-swipe band: AppLoad reacts to a press
// above y = 100, so the band starts at 120 device pixels at the least.
func (a *App) revealMinY() int { return max(120, a.u(120)) }

// gesture classifies a finished touch into tap / swipe.
func (a *App) gesture(x0, y0, x, y int) {
	dx, dy := x-x0, y-y0
	adx, ady := abs(dx), abs(dy)
	fromTop := y0 >= a.revealMinY() && y0 <= a.topBand()
	if fromTop && dy > 0 && ady >= a.u(100) && ady*10 > adx*14 {
		a.onSwipe(0, 1, true)
		return
	}
	if a.screen() == scrReader && y0 < a.revealMinY() && dy > 0 && ady >= a.u(100) && ady*10 > adx*14 {
		return // AppLoad's top-bar drag: not ours
	}
	slop := a.u(80)
	if dist2(x0, y0, x, y) <= slop*slop {
		a.onTap(x0, y0)
		return
	}
	switch {
	case ady >= a.u(160) && ady*10 > adx*14:
		a.onSwipe(0, sign(dy), false)
	case adx >= a.u(200) && adx*10 > ady*14:
		a.onSwipe(sign(dx), 0, false)
	default:
		a.onTap(x0, y0)
	}
}

func (a *App) onTap(x, y int) {
	for i := len(a.hits) - 1; i >= 0; i-- {
		if image.Pt(x, y).In(a.hits[i].r) {
			if a.hits[i].fnxy != nil {
				a.hits[i].fnxy(x, y)
			} else if a.hits[i].fn != nil {
				a.hits[i].fn()
			}
			return
		}
	}
	if a.screen() == scrReader {
		a.readerTapZone(x, y)
	}
}

func (a *App) onSwipe(sx, sy int, fromTop bool) {
	if a.pop.open {
		return
	}
	switch a.screen() {
	case scrSignin, scrSearch:
		return
	case scrSettings:
		if sy != 0 {
			a.settingsScroll(sy)
		}
	case scrReader:
		a.readerSwipe(sx, sy, fromTop)
	default:
		if sy < 0 {
			a.listPage(1)
		} else if sy > 0 {
			a.listPage(-1)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}
