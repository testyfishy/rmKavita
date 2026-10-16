package store

import (
	"encoding/json"
	"errors"
	"os"
)

// Fit modes. (There is no continuous reading mode: an e-ink panel is not built
// for scrolling; an old settings file that names one is read and the field is
// dropped at the next save.)
const (
	FitBoth   = "both"
	FitWidth  = "width"
	FitHeight = "height"
)

// Refresh choices: the active radio row of the "Full refresh" list.
const (
	RefreshDefault = 0 // every DefaultRefresh pages
	RefreshCustom1 = 1
	RefreshCustom2 = 2
	RefreshCustom3 = 3
	RefreshNever   = 4
)

// DefaultRefresh is the page-turn count between clearing refreshes.
const DefaultRefresh = 20

// Colour pages ghost more than black and white ones, so they have their own
// interval. The active row of the "Full refresh, colour" list:
const (
	ColourEvery5   = 0 // the default
	ColourEvery3   = 1
	ColourEvery10  = 2
	ColourCustom1  = 3
	ColourCustom2  = 4
	ColourCustom3  = 5
	ColourNever    = 6
	DefaultColourN = 5
)

// Limits of a custom refresh slot and of the DPI setting.
const (
	MinRefresh = 1
	MaxRefresh = 200
	MinDPI     = 120
	MaxDPI     = 400
)

// Prefs are the reading preferences of one document kind.
type Prefs struct {
	Fit string `json:"fit"` // both | width | height
}

// Settings are the non-secret preferences the Settings screen edits. They live
// in their own file (no key, no user name) and apply immediately.
type Settings struct {
	V       int    `json:"v"`
	Comic   Prefs  `json:"comic"` // manga and comics
	PDF     Prefs  `json:"pdf"`
	RTL     bool   `json:"rtl"`     // reading direction of comics: right to left
	Refresh int    `json:"refresh"` // active choice, see RefreshDefault ...
	Custom  [3]int `json:"custom"`  // the three custom slot values
	Colour  bool   `json:"colour"`  // colour rendering (off = luminance only)
	DPI     int    `json:"dpi"`     // 0 = automatic (the device's own)

	// Colour pages: their own clearing interval (see ColourEvery5 ...).
	ColourRefresh int    `json:"colour_refresh"`
	ColourCustom  [3]int `json:"colour_custom"`
	// Footer: the always-visible status strip under the page.
	Footer bool `json:"footer"`
	// Lists: comfortable rows with covers, or compact one-line rows; auto = compact
	// when the list has more than CompactOver entries.
	Lists string `json:"lists"` // auto | compact | comfortable
	// Alpha: the A-Z jump bar on the right edge of long lists; auto = on when
	// the list is alphabetical and has more than CompactOver entries.
	Alpha string `json:"alpha"` // auto | on | off
}

// Lists and Alpha values.
const (
	ListsAuto        = "auto"
	ListsCompact     = "compact"
	ListsComfortable = "comfortable"
	AlphaAuto        = "auto"
	AlphaOn          = "on"
	AlphaOff         = "off"
	// CompactOver is the number of entries above which a list is long: Auto
	// shows it compact and with the alphabet bar.
	CompactOver = 40
)

// DefaultSettings are the out-of-the-box choices per document kind.
func DefaultSettings() Settings {
	return Settings{
		V:       1,
		Comic:   Prefs{Fit: FitBoth},
		PDF:     Prefs{Fit: FitWidth},
		RTL:     true,
		Refresh: RefreshDefault,
		Custom:  [3]int{10, 30, 50},
		Colour:  false, // B/W by default

		ColourRefresh: ColourEvery5,
		ColourCustom:  [3]int{7, 15, 30},
		Footer:        true,
		Lists:         ListsAuto,
		Alpha:         AlphaAuto,
	}
}

func validPrefs(p Prefs, def Prefs) Prefs {
	if p.Fit != FitBoth && p.Fit != FitWidth && p.Fit != FitHeight {
		p.Fit = def.Fit
	}
	return p
}

// Sanitize forces every field into its legal range.
func (s *Settings) Sanitize() {
	d := DefaultSettings()
	s.V = 1
	s.Comic = validPrefs(s.Comic, d.Comic)
	s.PDF = validPrefs(s.PDF, d.PDF)
	if s.Refresh < RefreshDefault || s.Refresh > RefreshNever {
		s.Refresh = RefreshDefault
	}
	for i := range s.Custom {
		if s.Custom[i] < MinRefresh || s.Custom[i] > MaxRefresh {
			s.Custom[i] = d.Custom[i]
		}
	}
	if s.DPI != 0 {
		s.DPI = clampInt(s.DPI, MinDPI, MaxDPI)
	}
	switch s.Lists {
	case ListsAuto, ListsCompact, ListsComfortable:
	default:
		s.Lists = ListsAuto
	}
	switch s.Alpha {
	case AlphaAuto, AlphaOn, AlphaOff:
	default:
		s.Alpha = AlphaAuto
	}
	if s.ColourRefresh < ColourEvery5 || s.ColourRefresh > ColourNever {
		s.ColourRefresh = ColourEvery5
	}
	for i := range s.ColourCustom {
		if s.ColourCustom[i] < MinRefresh || s.ColourCustom[i] > MaxRefresh {
			s.ColourCustom[i] = d.ColourCustom[i]
		}
	}
}

// ColourEveryN is the number of colour page turns between clearing refreshes
// (0 = never).
func (s Settings) ColourEveryN() int {
	switch s.ColourRefresh {
	case ColourEvery3:
		return 3
	case ColourEvery10:
		return 10
	case ColourCustom1, ColourCustom2, ColourCustom3:
		return s.ColourCustom[s.ColourRefresh-ColourCustom1]
	case ColourNever:
		return 0
	}
	return DefaultColourN
}

// RefreshEvery is the number of page turns between clearing refreshes
// (0 = never).
func (s Settings) RefreshEvery() int {
	switch s.Refresh {
	case RefreshCustom1, RefreshCustom2, RefreshCustom3:
		return s.Custom[s.Refresh-1]
	case RefreshNever:
		return 0
	}
	return DefaultRefresh
}

// SeedFromConfig applies the values an older config carried (reading
// direction, refresh interval). It is used once, when no settings file exists.
func (s *Settings) SeedFromConfig(c Config) {
	if c.HasRTL {
		s.RTL = c.RTL
	}
	if c.HasFullEvery {
		switch {
		case c.FullEvery == 0:
			s.Refresh = RefreshNever
		case c.FullEvery != DefaultRefresh && c.FullEvery >= MinRefresh && c.FullEvery <= MaxRefresh:
			s.Custom[0] = c.FullEvery
			s.Refresh = RefreshCustom1
		}
	}
}

// LoadSettings reads the settings file. A missing or unreadable file yields
// the defaults seeded from the config; fromFile reports which happened.
func LoadSettings(path string, c Config) (s Settings, fromFile bool) {
	s = DefaultSettings()
	b, err := os.ReadFile(path)
	if err != nil {
		s.SeedFromConfig(c)
		return s, false
	}
	var f Settings
	f = DefaultSettings()
	if err := json.Unmarshal(b, &f); err != nil {
		s.SeedFromConfig(c)
		return s, false
	}
	f.Sanitize()
	return f, true
}

// SaveSettings writes the settings file atomically.
func SaveSettings(path string, s Settings) error {
	if path == "" {
		return errors.New("no settings path")
	}
	s.Sanitize()
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(b, '\n'), 0o600)
}
