// Package store holds the app's small pieces of local state: the root-only
// config file (server, user name, API key and a few hand-edited tunables), the
// non-secret settings file, the reading positions and the log.
package store

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config is read from the config file (mode 600). It carries the only secret
// the app keeps: the API key Kavita returned at sign-in. The password is never
// stored. Nothing here is ever logged or drawn.
type Config struct {
	Exists   bool   // the file was present
	Server   string // origin of the Kavita server, "" = not set yet
	Username string
	APIKey   string // secret; "" = signed out

	// Values from older configs that now only seed the settings file once.
	HasRTL       bool
	RTL          bool
	HasFullEvery bool
	FullEvery    int

	ReadAhead    int    // pages fetched ahead of the current one (0-5)
	KeepBehind   int    // pages kept behind it for going back (0-4)
	CacheMB      int    // byte budget of the fitted pages held in RAM (32-192)
	MemFloorMB   int    // below this much MemAvailable no new page is decoded (0 = off)
	PageMode     string // waveform for page turns: auto | ui | fast | content | animate
	UIMode       string // waveform for list screens
	ClearMode    string // waveform of the periodic full clear
	Covers       bool   // fetch small cover thumbnails for list rows
	ClearStyle   string // ui_full | content_full: how a clearing refresh is sent (ui_full: one full-refresh request, no mode message)
	FBFormat     string // auto | rgba | rgb565: framebuffer pixel format
	Border       string // white | match: colour of the margin around a page
	Crop         string // keep | trim: auto-crop of white/black page margins
	Kernel       string // auto | catmull | lanczos | smooth: page resampling filter
	Warmup       bool   // fetch the first page of the next chapter shortly before the end
	MemGuardMB   int    // skip prefetch when MemAvailable is below this (0 = off)
	ZoneMiddle   int    // width of the middle tap column in percent (20-60); the side columns turn pages
	BarsClean    string // auto | always | never: how bars and menus are repainted over the page (see viewport.go)
	Render       string // quality | balanced | speed: how a page turn is rendered (see baseWave in ui/reader.go)
	SpeedLevels  int    // 2 | 4 | 16: grey levels of a page in speed mode
	SpeedDither  string // ordered | diffuse | none: how a speed-mode page is dithered
	ClearIdleMS  int    // 0..5000: a due clearing refresh waits for this long without input (0 = inside the turn)
	HotTL        string // what holding each screen corner does (see HotActions)
	HotTR        string
	HotBL        string
	HotBR        string
	RotateSpread bool    // rotate landscape double-page spreads to use the tall screen
	Gamma        float64 // output curve; 1 = off
	HoldMs       int     // corner hold duration
	SyncProgress bool    // re-request the page you are on after a pause so Kavita's progress matches it
	Unknown      []string
}

// DefaultConfig returns the defaults.
func DefaultConfig() Config {
	return Config{
		ReadAhead:    3,
		KeepBehind:   2,
		CacheMB:      96,
		MemFloorMB:   100,
		PageMode:     "auto",
		UIMode:       "ui",
		ClearMode:    "content",
		ClearStyle:   "ui_full",
		ClearIdleMS:  600,
		FBFormat:     "auto",
		Border:       "match",
		Crop:         "trim",
		Kernel:       "auto",
		Warmup:       true,
		MemGuardMB:   200,
		ZoneMiddle:   40,
		BarsClean:    "never",
		Render:       "balanced",
		SpeedLevels:  2,
		SpeedDither:  "diffuse",
		HotTL:        HotNone,
		HotTR:        HotClear,
		HotBL:        HotQuit,
		HotBR:        HotNone,
		Covers:       true,
		RotateSpread: false,
		Gamma:        1,
		HoldMs:       900,
		SyncProgress: true,
	}
}

// Hot-corner actions, as the words of hot_tl / hot_tr / hot_bl / hot_br. The
// screen at Settings -> Hot corners offers exactly these.
const (
	HotNone        = "none"
	HotQuit        = "quit"
	HotClear       = "clear"
	HotHome        = "home"
	HotSettings    = "settings"
	HotControls    = "controls"
	HotFirst       = "first"
	HotLast        = "last"
	HotNextChapter = "next_chapter"
	HotPrevChapter = "prev_chapter"
	HotColour      = "colour"
	HotDirection   = "direction"
	HotFit         = "fit"
	HotFooter      = "footer"
)

// HotActions lists them in the order the chooser shows them.
var HotActions = []string{HotNone, HotQuit, HotClear, HotHome, HotSettings, HotControls, HotFirst, HotLast,
	HotNextChapter, HotPrevChapter, HotColour, HotDirection, HotFit, HotFooter}

// ValidHotAction reports whether s is one of HotActions.
func ValidHotAction(s string) bool {
	for _, a := range HotActions {
		if a == s {
			return true
		}
	}
	return false
}

func parseBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func validMode(s string) bool {
	switch s {
	case "ui", "fast", "content", "animate":
		return true
	}
	return false
}

// NormalizeServer turns what was typed into an origin (scheme://host[:port]).
// A missing scheme means http. Any path, query or key is dropped.
func NormalizeServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("Enter the server address")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", errors.New("That is not a server address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("The address must start with http or https")
	}
	if strings.ContainsAny(u.Host, " \t") {
		return "", errors.New("That is not a server address")
	}
	return u.Scheme + "://" + u.Host, nil
}

// legacyRoot extracts the origin of a v1 `root = http://host:port/api/opds/<key>`
// value and reports whether it carried a key.
func legacyRoot(v string) (origin string, hadKey bool, ok bool) {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false, false
	}
	return u.Scheme + "://" + u.Host, strings.Trim(u.Path, "/") != "", true
}

// ParseConfig reads key = value lines. Bad values fall back to the default and
// are named (key only, never the value) in Unknown.
func ParseConfig(data string) Config {
	c := DefaultConfig()
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	haveServer := false
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), string([]byte{0xEF, 0xBB, 0xBF})))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		i := strings.IndexAny(line, "=:")
		if i < 0 {
			c.Unknown = append(c.Unknown, "malformed line")
			continue
		}
		k := strings.ToLower(strings.TrimSpace(line[:i]))
		v := strings.TrimSpace(line[i+1:])
		bad := func() { c.Unknown = append(c.Unknown, "bad value for "+k) }
		switch k {
		case "server":
			if o, _, ok := legacyRoot(v); ok {
				c.Server, haveServer = o, true
			} else {
				bad()
			}
		case "root", "opds_url", "url":
			// A v1 catalogue address: only its origin is used, the key in it never is.
			if o, _, ok := legacyRoot(v); ok {
				if !haveServer {
					c.Server = o
				}
			} else {
				bad()
			}
		case "username", "user":
			c.Username = v
		case "api_key", "apikey", "key":
			c.APIKey = v
		case "full_refresh_every", "full_every":
			if n, err := strconv.Atoi(v); err == nil {
				c.FullEvery, c.HasFullEvery = clampInt(n, 0, 1000), true
			} else {
				bad()
			}
		case "rtl":
			if b, ok := parseBool(v); ok {
				c.RTL, c.HasRTL = b, true
			} else {
				bad()
			}
		case "read_ahead":
			if n, err := strconv.Atoi(v); err == nil {
				c.ReadAhead = clampInt(n, 0, 5)
			} else {
				bad()
			}
		case "keep_behind":
			if n, err := strconv.Atoi(v); err == nil {
				c.KeepBehind = clampInt(n, 0, 4)
			} else {
				bad()
			}
		case "cache_mb":
			if n, err := strconv.Atoi(v); err == nil {
				c.CacheMB = clampInt(n, 32, 192)
			} else {
				bad()
			}
		case "mem_floor_mb":
			if n, err := strconv.Atoi(v); err == nil {
				c.MemFloorMB = clampInt(n, 0, 512)
			} else {
				bad()
			}
		case "prefetch", "max_cached":
			// the 0.2 and early 0.3 keys: replaced by read_ahead, keep_behind and cache_mb; accepted and ignored
		case "mode", "reading_mode", "continuous", "scroll_overlap":
			// continuous reading mode was removed in 0.3.0: a conf that still has a key for it
			// is read without a complaint
		case "page_mode":
			lv := strings.ToLower(v)
			if lv == "auto" || validMode(lv) {
				c.PageMode = lv
			} else {
				bad()
			}
		case "ui_mode":
			if validMode(strings.ToLower(v)) {
				c.UIMode = strings.ToLower(v)
			} else {
				bad()
			}
		case "clear_mode":
			if validMode(strings.ToLower(v)) {
				c.ClearMode = strings.ToLower(v)
			} else {
				bad()
			}
		case "covers":
			if b, ok := parseBool(v); ok {
				c.Covers = b
			} else {
				bad()
			}
		case "crop_margins": // the version 2 key
			if b, ok := parseBool(v); ok {
				c.Crop = "keep"
				if b {
					c.Crop = "trim"
				}
			} else {
				bad()
			}
		case "crop":
			if lv := strings.ToLower(v); lv == "keep" || lv == "trim" {
				c.Crop = lv
			} else {
				bad()
			}
		case "border":
			if lv := strings.ToLower(v); lv == "white" || lv == "match" {
				c.Border = lv
			} else {
				bad()
			}
		case "clear_style":
			if lv := strings.ToLower(v); lv == "content_full" || lv == "ui_full" {
				c.ClearStyle = lv
			} else {
				bad()
			}
		case "fb_format":
			switch lv := strings.ToLower(v); lv {
			case "auto", "rgba", "rgb565":
				c.FBFormat = lv
			case "rgba8888":
				c.FBFormat = "rgba"
			case "565":
				c.FBFormat = "rgb565"
			default:
				bad()
			}
		case "kernel":
			switch lv := strings.ToLower(v); lv {
			case "auto", "catmull", "lanczos", "smooth":
				c.Kernel = lv
			default:
				bad()
			}
		case "warmup", "next_chapter_warmup":
			if b, ok := parseBool(v); ok {
				c.Warmup = b
			} else {
				bad()
			}
		case "mem_guard_mb":
			if n, err := strconv.Atoi(v); err == nil {
				c.MemGuardMB = clampInt(n, 0, 512)
			} else {
				bad()
			}
		case "zone_middle_pct":
			if n, err := strconv.Atoi(v); err == nil {
				c.ZoneMiddle = clampInt(n, 20, 60)
			} else {
				bad()
			}
		case "render":
			if lv := strings.ToLower(v); lv == "quality" || lv == "balanced" || lv == "speed" {
				c.Render = lv
			} else {
				bad()
			}
		case "speed_levels":
			if n, err := strconv.Atoi(v); err == nil && (n == 2 || n == 4 || n == 16) {
				c.SpeedLevels = n
			} else {
				bad()
			}
		case "speed_dither":
			if lv := strings.ToLower(v); lv == "ordered" || lv == "diffuse" || lv == "none" {
				c.SpeedDither = lv
			} else {
				bad()
			}
		case "clear_idle_ms":
			if n, err := strconv.Atoi(v); err == nil {
				c.ClearIdleMS = clampInt(n, 0, 5000)
			} else {
				bad()
			}
		case "bars_clean":
			if lv := strings.ToLower(v); lv == "auto" || lv == "always" || lv == "never" {
				c.BarsClean = lv
			} else {
				bad()
			}
		case "chrome_clean": // the earlier key: yes = auto, no = never
			if b, ok := parseBool(v); ok {
				c.BarsClean = "auto"
				if !b {
					c.BarsClean = "never"
				}
			} else {
				bad()
			}
		case "hot_tl", "hot_tr", "hot_bl", "hot_br":
			lv := strings.ToLower(v)
			if !ValidHotAction(lv) {
				bad()
				break
			}
			switch k {
			case "hot_tl":
				c.HotTL = lv
			case "hot_tr":
				c.HotTR = lv
			case "hot_bl":
				c.HotBL = lv
			default:
				c.HotBR = lv
			}
		case "rotate_spreads":
			if b, ok := parseBool(v); ok {
				c.RotateSpread = b
			} else {
				bad()
			}
		case "gamma":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0.4 && f <= 2.5 {
				c.Gamma = f
			} else {
				bad()
			}
		case "hold_ms":
			if n, err := strconv.Atoi(v); err == nil {
				c.HoldMs = clampInt(n, 400, 3000)
			} else {
				bad()
			}
		case "sync_progress":
			if b, ok := parseBool(v); ok {
				c.SyncProgress = b
			} else {
				bad()
			}
		default:
			c.Unknown = append(c.Unknown, "unknown key "+k)
		}
	}
	return c
}

// LoadConfig reads and parses the file at path. A missing file is not an error
// (the app then starts at the Sign-in screen); a world- or group-readable file
// that holds a key is reported but still used.
func LoadConfig(path string) (Config, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultConfig(), "", nil
		}
		return DefaultConfig(), "", fmt.Errorf("cannot read config: %w", err)
	}
	c := ParseConfig(string(b))
	c.Exists = true
	warn := ""
	if c.APIKey != "" {
		if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
			warn = fmt.Sprintf("config file mode is %o, expected 600", st.Mode().Perm())
		}
	}
	return c, warn, nil
}

// ---- writing -------------------------------------------------------------------

// WriteFileAtomic writes data next to path and renames it into place.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, mode)
	// A reader holding the destination open makes the rename fail on Windows (the
	// test host); retry briefly. On the device the first attempt succeeds.
	var rerr error
	for i := 0; i < 8; i++ {
		if rerr = os.Rename(tmp, path); rerr == nil {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	os.Remove(tmp)
	return rerr
}

func lineKey(line string) string {
	t := strings.TrimSpace(strings.TrimPrefix(line, string([]byte{0xEF, 0xBB, 0xBF})))
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
		return ""
	}
	i := strings.IndexAny(t, "=:")
	if i < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(t[:i]))
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

var reOpdsKeyText = regexp.MustCompile(`(?i)(/api/opds/)[A-Za-z0-9_-]{6,}`)

// MigrateConfigText removes every API key from a (possibly v1) config text:
// `root = http://host:port/api/opds/<key>` becomes `server = http://host:port`,
// api_key lines are deleted, every other line is kept. It reports how many
// key-bearing lines were removed or rewritten.
func MigrateConfigText(text string) (string, int) { return migrateConfig(text, false) }

// migrateConfig is MigrateConfigText; keepKey leaves a stored `api_key` line in
// place (the sign-in of an up-to-date config), while v1 root lines and keys
// pasted anywhere else are always removed.
func migrateConfig(text string, keepKey bool) (string, int) {
	lines := splitLines(text)
	haveServer := false
	for _, l := range lines {
		if lineKey(l) == "server" {
			haveServer = true
		}
	}
	var out []string
	removed := 0
	emittedServer := false
	for _, l := range lines {
		switch k := lineKey(l); k {
		case "api_key", "apikey", "key":
			if keepKey {
				out = append(out, l)
				continue
			}
			removed++
			continue
		case "root", "opds_url", "url":
			i := strings.IndexAny(l, "=:")
			v := strings.TrimSpace(l[i+1:])
			o, hadKey, ok := legacyRoot(v)
			if hadKey {
				removed++
			}
			if !ok || haveServer || emittedServer {
				continue // nothing usable, or the file already names its server
			}
			emittedServer = true
			out = append(out, "server = "+o)
			continue
		}
		if reOpdsKeyText.MatchString(l) { // a key pasted into a comment
			l = reOpdsKeyText.ReplaceAllString(l, "${1}<key>")
			removed++
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return "", removed
	}
	return strings.Join(out, "\n") + "\n", removed
}

// UpdateConf rewrites the config file in place, keeping every line it was not
// asked to change (comments and hand-edited tunables survive): the keys in set
// are replaced (first occurrence; later duplicates dropped) or appended, the
// keys in del are removed, and any v1 root line is dropped once a server is
// set. The file ends up mode 600, written atomically.
func UpdateConf(path string, set map[string]string, del []string) error {
	var lines []string
	if b, err := os.ReadFile(path); err == nil {
		lines = splitLines(string(b))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delSet := map[string]bool{}
	for _, d := range del {
		delSet[d] = true
	}
	_, settingServer := set["server"]
	done := map[string]bool{}
	var out []string
	for _, l := range lines {
		k := lineKey(l)
		if k == "apikey" || k == "key" {
			k = "api_key"
		}
		switch {
		case k == "":
			out = append(out, l)
		case delSet[k]:
			continue
		case k == "root" || k == "opds_url" || k == "url":
			if settingServer || delSet["server"] {
				continue
			}
			out = append(out, l)
		default:
			if v, ok := set[k]; ok {
				if done[k] {
					continue
				}
				done[k] = true
				out = append(out, k+" = "+v)
				continue
			}
			out = append(out, l)
		}
	}
	var extra []string
	for k := range set {
		if !done[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, k+" = "+set[k])
	}
	data := ""
	if len(out) > 0 {
		data = strings.Join(out, "\n") + "\n"
	}
	return WriteFileAtomic(path, []byte(data), 0o600)
}

// SaveSignIn stores what is needed to stream: server, user name and key.
func SaveSignIn(path, server, username, key string) error {
	return UpdateConf(path, map[string]string{"server": server, "username": username, "api_key": key}, nil)
}

// ClearKey removes the stored key (server and user name stay as prefill).
func ClearKey(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return UpdateConf(path, nil, []string{"api_key"})
}

// Migrate is the `-migrate` command: it brings an older install's files to
// the current layout without deleting anything and without ever printing a
// key. It returns human-readable lines (all starting with "migrate:").
func Migrate(conf, state, oldConf, oldState string) ([]string, error) {
	var out []string
	switch {
	case fileExists(conf):
		b, err := os.ReadFile(conf)
		if err != nil {
			return out, err
		}
		// An up-to-date config keeps its sign-in (reinstalling must not sign the
		// owner out); only v1-style keys (a root address, a pasted URL) go.
		text, n := migrateConfig(string(b), true)
		if n > 0 || st(conf)&0o077 != 0 {
			if err := WriteFileAtomic(conf, []byte(text), 0o600); err != nil {
				return out, err
			}
		}
		out = append(out, fmt.Sprintf("migrate: conf kept, removed %d key line(s)", n))
	case oldConf != "" && fileExists(oldConf):
		b, err := os.ReadFile(oldConf)
		if err != nil {
			return out, err
		}
		text, n := MigrateConfigText(string(b))
		// Version 1 wrote `page_mode = ui` as its default; carried over it would pin
		// the waveform that flashes on every turn. The default is now "auto".
		pageModeFixed := strings.Contains(text, "\npage_mode = ui\n")
		if pageModeFixed {
			text = strings.Replace(text, "\npage_mode = ui\n", "\npage_mode = auto\n", 1)
		}
		if err := WriteFileAtomic(conf, []byte(text), 0o600); err != nil {
			return out, err
		}
		out = append(out, fmt.Sprintf("migrate: conf created from the old one, removed %d key line(s)", n))
		if pageModeFixed {
			out = append(out, "migrate: page_mode ui -> auto (the old default flashed on every page turn)")
		}
	default:
		out = append(out, "migrate: no config to convert")
	}
	switch {
	case state == "":
	case fileExists(state):
		out = append(out, "migrate: positions already in place")
	case oldState != "" && fileExists(oldState):
		b, err := os.ReadFile(oldState)
		if err != nil {
			return out, err
		}
		if err := WriteFileAtomic(state, b, 0o600); err != nil {
			return out, err
		}
		out = append(out, "migrate: positions copied")
	default:
		out = append(out, "migrate: no positions to copy")
	}
	return out, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func st(p string) os.FileMode {
	s, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return s.Mode().Perm()
}
