package ui

import "image"

// HitInfo is a named tap target on the current screen.
type HitInfo struct {
	Name string
	Rect image.Rectangle
}

// Snapshot is a read-only picture of the UI state, taken on the UI goroutine.
// It exists for tests and diagnostics; nothing in the app reads it.
type Snapshot struct {
	Tag         string
	Seq         int // number of updates sent so far
	Screen      string
	Hits        []HitInfo
	Page, Off   int
	Count       int
	Slots       []int
	ReadyPages  []int
	Ready       int
	Peak        int
	HeldBytes   int
	Overlay     bool
	Popover     bool
	Colour      bool // a colour page is on screen
	ListTitle   string
	ListRows    int
	ListErr     bool
	ListLoading bool
	ListScroll  int
	SignedIn    bool
	Session     int
	DPI         int
	FocusField  int
	Busy        bool
	ServerText  string
	UserText    string
	PassLen     int
}

var screenNames = map[screenKind]string{scrList: "list", scrReader: "reader", scrSearch: "search", scrSettings: "settings", scrSignin: "signin"}

// Snapshot returns the current state. Call it only from the UI goroutine
// (the OnShow hook and the display's Idle callback run there).
func (a *App) Snapshot() Snapshot {
	s := Snapshot{Seq: a.showSeq, Screen: screenNames[a.screen()], SignedIn: a.Cl.Signed(), Session: a.sess, DPI: a.dpi}
	for _, h := range a.hits {
		if h.name != "" {
			s.Hits = append(s.Hits, HitInfo{h.name, h.r})
		}
	}
	if r := a.rd; r != nil {
		s.Page, s.Off, s.Count, s.Peak = r.page, r.off, r.count, r.peakHeld
		s.Overlay, s.Popover = r.overlay, a.pop.open
		s.HeldBytes = r.heldBytes()
		for n, sl := range r.slots {
			s.Slots = append(s.Slots, n)
			if sl.state == 2 {
				s.Ready++
				s.ReadyPages = append(s.ReadyPages, n)
			}
		}
		s.Colour = a.layerActive
	}
	if len(a.stack) > 0 {
		lv := a.top()
		s.ListTitle, s.ListRows, s.ListErr, s.ListLoading, s.ListScroll = lv.title, len(lv.rows), lv.err != nil, lv.loading, lv.scroll
	}
	if a.si != nil {
		s.FocusField, s.Busy = a.si.focus, a.si.busy
		s.ServerText, s.UserText, s.PassLen = string(a.si.server), string(a.si.user), len(a.si.pass)
	}
	return s
}
