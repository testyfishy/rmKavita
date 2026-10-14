package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeKey = "0123456789abcdef" + "0123456789abcdef"

var reLong = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)

func TestParseConfig(t *testing.T) {
	c := ParseConfig("\xEF\xBB\xBF# comment\nserver = http://127.0.0.1:5001/\nusername = reader\napi_key = " + fakeKey + "\n" +
		"full_refresh_every=40\nrtl = no\nread_ahead: 4\n" +
		"keep_behind = 3\ncache_mb = 64\nprefetch = 9\nmax_cached = 1\npage_mode = fast\ncovers=off\ncrop_margins=1\ngamma=1.2\nhold_ms=1200\nsync_progress=false\nrotate_spreads=yes\n" +
		"bogus = 1\nfull_refresh_every_typo\n")
	if c.Server != "http://127.0.0.1:5001" || c.Username != "reader" || c.APIKey != fakeKey {
		t.Errorf("server=%q user=%q key set=%v", c.Server, c.Username, c.APIKey != "")
	}
	if !c.HasFullEvery || c.FullEvery != 40 || !c.HasRTL || c.RTL || c.ReadAhead != 4 || c.KeepBehind != 3 || c.CacheMB != 64 || c.PageMode != "fast" || c.Covers ||
		c.Crop != "trim" || c.Gamma != 1.2 || c.HoldMs != 1200 || c.SyncProgress || !c.RotateSpread {
		t.Errorf("parsed %+v", c)
	}
	if len(c.Unknown) != 2 {
		t.Errorf("unknown = %v", c.Unknown)
	}
	for _, u := range c.Unknown {
		if strings.Contains(u, fakeKey) {
			t.Errorf("a note leaks config content: %q", u)
		}
	}
}

// A v1 config named the catalogue root with the key in it: only the origin is
// used, and the key never reaches the Config.
func TestParseConfigV1RootKeyIsDropped(t *testing.T) {
	c := ParseConfig("root = http://127.0.0.1:5001/api/opds/" + fakeKey + "\nrtl = yes\nfull_refresh_every = 20\n")
	if c.Server != "http://127.0.0.1:5001" {
		t.Errorf("server = %q", c.Server)
	}
	if c.APIKey != "" {
		t.Error("the key in a v1 root line must not become the API key")
	}
}

func TestParseConfigDefaultsAndBadValues(t *testing.T) {
	c := ParseConfig("server = x y\nfull_refresh_every = lots\nread_ahead = 99\ncache_mb = 5\ngamma = 9\nhold_ms = 5\npage_mode = sparkly\n")
	d := DefaultConfig()
	if c.HasFullEvery || c.PageMode != d.PageMode || c.Gamma != d.Gamma {
		t.Errorf("bad values should keep defaults: %+v", c)
	}
	if c.ReadAhead != 5 || c.CacheMB != 32 || c.HoldMs != 400 {
		t.Errorf("out-of-range values should clamp: read_ahead=%d cache_mb=%d hold=%d", c.ReadAhead, c.CacheMB, c.HoldMs)
	}
	if d.ReadAhead != 3 || d.KeepBehind != 2 || d.CacheMB != 96 || d.MemGuardMB != 200 || d.MemFloorMB != 100 || d.PageMode != "auto" || d.Border != "match" || d.ZoneMiddle != 40 || d.Server != "" {
		t.Errorf("defaults changed: %+v", d)
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:5001":              "http://127.0.0.1:5001",
		"  http://host.local:5000/  ": "http://host.local:5000",
		"https://k.example.org/path":  "https://k.example.org",
		"http://h/api/opds/KEY":       "http://h",
	} {
		got, err := NormalizeServer(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", "ftp://x", "http://", "a b"} {
		if _, err := NormalizeServer(bad); err == nil {
			t.Errorf("NormalizeServer(%q) accepted", bad)
		}
	}
}

func TestLoadConfigMissingAndPermissionWarning(t *testing.T) {
	c, warn, err := LoadConfig(filepath.Join(t.TempDir(), "nope"))
	if err != nil || warn != "" || c.Exists || c.APIKey != "" {
		t.Errorf("missing file: %+v %q %v", c, warn, err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	p := filepath.Join(t.TempDir(), "c.conf")
	os.WriteFile(p, []byte("server = http://h\napi_key = "+fakeKey+"\n"), 0o644)
	_, warn, err = LoadConfig(p)
	if err != nil || warn == "" {
		t.Errorf("warn=%q err=%v", warn, err)
	}
	os.Chmod(p, 0o600)
	_, warn, _ = LoadConfig(p)
	if warn != "" {
		t.Errorf("unexpected warning %q", warn)
	}
}

func TestMigrateConfigText(t *testing.T) {
	in := "# my settings\nroot = http://127.0.0.1:5001/api/opds/" + fakeKey + "\n\nrtl = yes\nfull_refresh_every = 15\napi_key = " + fakeKey + "\nprefetch = 3\n"
	out, n := MigrateConfigText(in)
	if n != 2 {
		t.Errorf("removed = %d, want 2", n)
	}
	if strings.Contains(out, fakeKey) || strings.Contains(out, "api_key") || strings.Contains(out, "root") {
		t.Errorf("key or root survived:\n%s", out)
	}
	for _, want := range []string{"# my settings", "server = http://127.0.0.1:5001", "rtl = yes", "full_refresh_every = 15", "prefetch = 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q:\n%s", want, out)
		}
	}
	// idempotent
	out2, n2 := MigrateConfigText(out)
	if n2 != 0 || out2 != out {
		t.Errorf("second pass changed things: n=%d\n%s", n2, out2)
	}
	// a key pasted into a comment is neutralised too
	out3, n3 := MigrateConfigText("# was http://h/api/opds/" + fakeKey + "\nserver = http://h\n")
	if n3 != 1 || strings.Contains(out3, fakeKey) {
		t.Errorf("comment key: n=%d %q", n3, out3)
	}
	// an existing server line wins over a root line
	out4, _ := MigrateConfigText("server = http://new:1\nroot = http://old:2/api/opds/" + fakeKey + "\n")
	if strings.Count(out4, "server") != 1 || !strings.Contains(out4, "http://new:1") || strings.Contains(out4, "old:2") {
		t.Errorf("server precedence: %q", out4)
	}
}

func TestUpdateConfKeepsOtherLinesAndMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.conf")
	os.WriteFile(p, []byte("# keep me\nserver = http://old\nprefetch = 3\nroot = http://x/api/opds/"+fakeKey+"\n"), 0o644)
	if err := SaveSignIn(p, "http://new:5000", "reader", fakeKey); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"# keep me", "prefetch = 3", "server = http://new:5000", "username = reader", "api_key = " + fakeKey} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "root") || strings.Contains(s, "old") || strings.Count(s, "server") != 1 {
		t.Errorf("stale lines survived:\n%s", s)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %o", st.Mode().Perm())
		}
	}
	if err := ClearKey(p); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	s = string(b)
	if strings.Contains(s, "api_key") || strings.Contains(s, fakeKey) || !strings.Contains(s, "username = reader") || !strings.Contains(s, "server = http://new:5000") {
		t.Errorf("after ClearKey:\n%s", s)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("temp file left behind")
	}
}

func TestMigrateFiles(t *testing.T) {
	dir := t.TempDir()
	oc, os_ := filepath.Join(dir, "old.conf"), filepath.Join(dir, "old-state.json")
	nc, ns := filepath.Join(dir, "new.conf"), filepath.Join(dir, "new-state.json")
	os.WriteFile(oc, []byte("root = http://127.0.0.1:5001/api/opds/"+fakeKey+"\nrtl = no\n"), 0o600)
	os.WriteFile(os_, []byte(`{"v":1,"last":3,"chapters":{"3":{"c":3,"t":"x","p":1,"n":5,"at":1}}}`), 0o600)
	lines, err := Migrate(nc, ns, oc, os_)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, fakeKey) || !strings.Contains(joined, "migrate:") {
		t.Errorf("output: %q", joined)
	}
	b, _ := os.ReadFile(nc)
	if strings.Contains(string(b), fakeKey) || !strings.Contains(string(b), "server = http://127.0.0.1:5001") || !strings.Contains(string(b), "rtl = no") {
		t.Errorf("new conf:\n%s", b)
	}
	if _, err := os.Stat(ns); err != nil {
		t.Errorf("positions not copied: %v", err)
	}
	if _, err := os.Stat(oc); err != nil {
		t.Error("migrate must not delete the old conf (the installer does, after checking)")
	}
	// second run: nothing to do, nothing breaks
	lines, err = Migrate(nc, ns, oc, os_)
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "kept") {
		t.Errorf("second run: %v %v", lines, err)
	}
	// an up-to-date config keeps its sign-in when the installer runs again; v1-style
	// keys (a root address, a key pasted into a comment) are still removed
	os.WriteFile(nc, []byte("server = http://h\nusername = u\napi_key = "+fakeKey+"\n# old: http://h/api/opds/"+fakeKey+"\n"), 0o644)
	lines, err = Migrate(nc, ns, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(nc)
	if !strings.Contains(string(b), "api_key = "+fakeKey) || strings.Contains(string(b), "/api/opds/"+fakeKey) {
		t.Errorf("sign-in not kept or pasted key kept: %s", strings.ReplaceAll(string(b), fakeKey, "<k>"))
	}
	if strings.Contains(strings.Join(lines, "\n"), fakeKey) {
		t.Error("key in the migrate output")
	}
	// the same file built from an OLD config never keeps a key line
	os.WriteFile(oc, []byte("server = http://h\napi_key = "+fakeKey+"\n"), 0o600)
	os.Remove(nc)
	if _, err := Migrate(nc, ns, oc, os_); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(nc)
	if strings.Contains(string(b), fakeKey) {
		t.Errorf("a key was carried over from the old config: %s", b)
	}
}

func TestSettingsDefaultsPersistAndSeed(t *testing.T) {
	d := DefaultSettings()
	if d.Comic.Fit != FitBoth || !d.RTL || d.PDF.Fit != FitWidth || d.Refresh != RefreshDefault || d.Colour || d.DPI != 0 {
		t.Errorf("defaults: %+v", d)
	}
	if d.RefreshEvery() != 20 {
		t.Errorf("default refresh = %d", d.RefreshEvery())
	}
	p := filepath.Join(t.TempDir(), "s.json")
	s := d
	s.Comic.Fit = FitHeight
	s.Refresh = RefreshCustom2
	s.Custom = [3]int{5, 77, 200}
	s.RTL = false
	s.Colour = true
	s.DPI = 264
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	got, fromFile := LoadSettings(p, DefaultConfig())
	if !fromFile || got != s {
		t.Errorf("round trip: %+v (fromFile %v)", got, fromFile)
	}
	if got.RefreshEvery() != 77 {
		t.Errorf("custom 2 = %d", got.RefreshEvery())
	}
	s.Refresh = RefreshNever
	if s.RefreshEvery() != 0 {
		t.Error("never must be 0")
	}
	// garbage in the file: legal values are forced
	os.WriteFile(p, []byte(`{"comic":{"mode":"continuous","fit":"huge"},"pdf":{"mode":"zigzag","fit":"width"},"refresh":9,"custom":[0,500,-3],"dpi":5}`), 0o600)
	g, _ := LoadSettings(p, DefaultConfig())
	if g.Comic.Fit != FitBoth || g.PDF.Fit != FitWidth || g.Refresh != RefreshDefault || g.Custom != [3]int{10, 30, 50} || g.DPI != MinDPI {
		t.Errorf("sanitised: %+v", g)
	}
	// no file: v1 config values seed it once
	c := ParseConfig("rtl = no\nfull_refresh_every = 33\n")
	sd, from := LoadSettings(filepath.Join(t.TempDir(), "none.json"), c)
	if from || sd.RTL || sd.Refresh != RefreshCustom1 || sd.Custom[0] != 33 {
		t.Errorf("seed: %+v from=%v", sd, from)
	}
	c = ParseConfig("full_refresh_every = 0\n")
	sd, _ = LoadSettings(filepath.Join(t.TempDir(), "none.json"), c)
	if sd.Refresh != RefreshNever {
		t.Errorf("seed never: %+v", sd)
	}
	c = ParseConfig("full_refresh_every = 20\n")
	sd, _ = LoadSettings(filepath.Join(t.TempDir(), "none.json"), c)
	if sd.Refresh != RefreshDefault {
		t.Errorf("seed 20: %+v", sd)
	}
}

func TestPositionsRoundTripAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "state.json")
	p := OpenPositions(path)
	now := time.Unix(1_700_000_000, 0)
	p.Now = func() time.Time { return now }
	p.Set(Pos{Library: 1, Series: 2, Volume: 3, Chapter: 4, Title: "Vol 1", Page: 7, Count: 20})
	if !p.Dirty() {
		t.Fatal("not dirty")
	}
	// debounce: too soon after "now" with lastSave zero is fine, then a second change is held back
	if err := p.FlushIfDue(time.Minute); err != nil {
		t.Fatal(err)
	}
	if p.Dirty() {
		t.Fatal("first flush should write")
	}
	p.Set(Pos{Chapter: 4, Title: "Vol 1", Page: 8, Count: 20, Y: 250, Kind: "pdf"})
	_ = p.FlushIfDue(time.Minute)
	if !p.Dirty() {
		t.Error("second flush within the gap should be deferred")
	}
	now = now.Add(2 * time.Minute)
	if err := p.FlushIfDue(time.Minute); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "http") || strings.Contains(string(b), "apiKey") {
		t.Errorf("state file holds a URL or key: %s", b)
	}
	var f fileFormat
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	q := OpenPositions(path)
	got, ok := q.Get(4)
	if !ok || got.Page != 8 || got.Count != 20 || got.Title != "Vol 1" || got.Y != 250 || got.Kind != "pdf" {
		t.Errorf("reloaded %+v ok=%v", got, ok)
	}
	if last, ok := q.Last(); !ok || last.Chapter != 4 {
		t.Errorf("last = %+v", last)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("temp file left behind")
	}
}

func TestPositionsPerAccountAndV1Adoption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	// a version 1 file: one anonymous group
	os.WriteFile(path, []byte(`{"v":1,"last":7,"chapters":{"7":{"l":1,"s":1,"v":1,"c":7,"t":"x","p":3,"n":9,"at":1}}}`), 0o600)
	p := OpenPositions(path)
	alice := OwnerKey("http://127.0.0.1:5001", "Alice")
	bob := OwnerKey("https://127.0.0.1:5001/", "bob")
	if alice != "alice@127.0.0.1:5001" || bob != "bob@127.0.0.1:5001" {
		t.Fatalf("owner keys %q %q", alice, bob)
	}
	p.SetOwner(alice)
	if got, ok := p.Get(7); !ok || got.Page != 3 {
		t.Fatalf("the first account must adopt the v1 positions: %+v %v", got, ok)
	}
	p.Set(Pos{Chapter: 8, Page: 1, Count: 5, Title: "y"})
	p.SetOwner(bob)
	if _, ok := p.Get(7); ok {
		t.Error("bob sees alice's positions")
	}
	if _, ok := p.Last(); ok {
		t.Error("bob has a last chapter")
	}
	p.Set(Pos{Chapter: 7, Page: 0, Count: 9, Title: "own"})
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	q := OpenPositions(path)
	q.SetOwner(alice)
	if got, ok := q.Get(7); !ok || got.Page != 3 {
		t.Errorf("alice after reload: %+v", got)
	}
	if last, ok := q.Last(); !ok || last.Chapter != 8 {
		t.Errorf("alice last: %+v", last)
	}
	q.SetOwner(bob)
	if got, ok := q.Get(7); !ok || got.Title != "own" {
		t.Errorf("bob after reload: %+v", got)
	}
	// signed out: the anonymous group is empty again, nothing leaks
	q.SetOwner("")
	if _, ok := q.Get(7); ok {
		t.Error("signed-out group has positions")
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "http") {
		t.Errorf("state holds a URL: %s", b)
	}
}

func TestPositionsCorruptFileKeptAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	p := OpenPositions(path)
	if _, ok := p.Get(1); ok {
		t.Error("corrupt file yielded data")
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Errorf("corrupt file was not kept aside: %v", err)
	}
}

func TestPositionsCapAndOldPrefsIgnored(t *testing.T) {
	p := OpenPositions(filepath.Join(t.TempDir(), "s.json"))
	n := 0
	p.Now = func() time.Time { n++; return time.Unix(int64(1000+n), 0) }
	for i := 1; i <= maxEntries+25; i++ {
		p.Set(Pos{Chapter: i, Page: 1, Count: 10, Title: "t"})
	}
	if len(p.data.Chapters) != maxEntries {
		t.Errorf("entries = %d", len(p.data.Chapters))
	}
	if _, ok := p.Get(1); ok {
		t.Error("oldest entry survived the cap")
	}
	if _, ok := p.Get(maxEntries + 25); !ok {
		t.Error("newest entry evicted")
	}
	// An older state file may still carry a "prefs" object: it is ignored, and
	// the next save does not write it back (positions are the only state).
	path := filepath.Join(t.TempDir(), "old.json")
	os.WriteFile(path, []byte(`{"v":1,"last":7,"prefs":{"rtl":false},"chapters":{"7":{"l":1,"s":1,"v":1,"c":7,"t":"x","p":3,"n":9,"at":1}}}`), 0o600)
	q := OpenPositions(path)
	if got, ok := q.Get(7); !ok || got.Page != 3 {
		t.Fatalf("old file not read: %+v %v", got, ok)
	}
	q.Set(Pos{Chapter: 7, Page: 4, Count: 9, Title: "x"})
	if err := q.Flush(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "prefs") || strings.Contains(string(b), "rtl") {
		t.Errorf("state file still carries preferences: %s", b)
	}
}

func TestLoggerRotatesInsteadOfTruncating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.log")
	l := NewLogger(path, nil)
	l.Stderr = false
	l.max = 2000
	for i := 0; i < 100; i++ {
		l.Logf("line %03d %s", i, strings.Repeat("x", 40))
	}
	cur, _ := os.ReadFile(path)
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("no rotated file: %v", err)
	}
	if len(cur) == 0 || len(cur) > 2200 || len(old) == 0 || len(old) > 2200 {
		t.Errorf("sizes %d + %d", len(cur), len(old))
	}
	if !strings.Contains(string(cur), "line 099") {
		t.Errorf("the newest line is missing:\n%s", cur)
	}
	// the history is contiguous: the rotated file ends where the current one begins
	var lastOld, firstCur int
	fmt.Sscanf(string(old[strings.LastIndex(string(old), "line ")+0:]), "line %d", &lastOld)
	fmt.Sscanf(string(cur[strings.Index(string(cur), "line "):]), "line %d", &firstCur)
	if firstCur != lastOld+1 {
		t.Errorf("gap between the files: old ends at %d, current starts at %d", lastOld, firstCur)
	}
}

func TestLoggerRedacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.log")
	l := NewLogger(path, func(s string) string { return strings.ReplaceAll(s, "SECRET", "<key>") })
	l.Logf("GET /api/opds/%s/x", "SECRET")
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "SECRET") || !strings.Contains(string(b), "<key>") {
		t.Errorf("log = %q", b)
	}
}

// The sample config shipped in package\ must parse without a single complaint,
// contain no key and spell out exactly the built-in defaults, so it can never
// drift from them.
func TestSampleConfigMatchesDefaults(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "package", "rmkavita.conf.sample"))
	if err != nil {
		t.Skipf("package not staged: %v", err)
	}
	c := ParseConfig(string(b))
	if len(c.Unknown) != 0 {
		t.Errorf("sample config has unknown or bad lines: %v", c.Unknown)
	}
	if c.APIKey != "" || reLong.MatchString(string(b)) {
		t.Error("the sample must not contain a key or a key-shaped token")
	}
	c.Server, c.Username = "", ""
	d := DefaultConfig()
	if c.ReadAhead != d.ReadAhead || c.KeepBehind != d.KeepBehind || c.CacheMB != d.CacheMB || c.MemFloorMB != d.MemFloorMB ||
		c.ZoneMiddle != d.ZoneMiddle || c.BarsClean != d.BarsClean || c.HotTL != d.HotTL || c.HotTR != d.HotTR || c.HotBL != d.HotBL || c.HotBR != d.HotBR ||
		c.PageMode != d.PageMode || c.UIMode != d.UIMode || c.ClearMode != d.ClearMode || c.Covers != d.Covers ||
		c.Crop != d.Crop || c.RotateSpread != d.RotateSpread || c.Gamma != d.Gamma ||
		c.HoldMs != d.HoldMs || c.SyncProgress != d.SyncProgress ||
		c.ClearStyle != d.ClearStyle || c.FBFormat != d.FBFormat || c.Border != d.Border || c.Kernel != d.Kernel ||
		c.Warmup != d.Warmup || c.MemGuardMB != d.MemGuardMB || c.Render != d.Render || c.SpeedLevels != d.SpeedLevels || c.SpeedDither != d.SpeedDither || c.ClearIdleMS != d.ClearIdleMS {
		t.Errorf("sample differs from the defaults:\n sample   %+v\n defaults %+v", c, d)
	}
	if c.HasRTL || c.HasFullEvery {
		t.Error("the sample must not set rtl or full_refresh_every (those live in the settings file)")
	}
}

// Continuous mode was removed: a conf that still carries a key for it is read
// without being reported as unknown and changes nothing.
func TestRemovedContinuousKeysAreIgnoredSilently(t *testing.T) {
	c := ParseConfig("mode = continuous\nreading_mode = continuous\ncontinuous = yes\nscroll_overlap = 8\npage_mode = ui\n")
	if len(c.Unknown) != 0 {
		t.Errorf("reported as unknown: %v", c.Unknown)
	}
	d := DefaultConfig()
	d.PageMode = "ui"
	if c.Render != d.Render || c.Crop != d.Crop || c.PageMode != "ui" {
		t.Errorf("a removed key changed something: %+v", c)
	}
}
