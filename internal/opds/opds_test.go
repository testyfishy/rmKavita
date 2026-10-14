package opds

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtures = "testdata/kavita-0.9.1.4"

func load(t *testing.T, name string) *Feed {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseFeed(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f
}

func TestRootFeedFromRealKavita(t *testing.T) {
	f := load(t, "root.xml")
	if len(f.Entries) != 7 {
		t.Fatalf("entries = %d, want 7", len(f.Entries))
	}
	if f.SearchDescription() != "/api/opds/TESTKEY/search" {
		t.Errorf("search = %q", f.SearchDescription())
	}
	want := []string{"On Deck", "Recently Updated", "Recently Added", "Reading Lists", "Want to Read", "All Libraries", "All Collections"}
	for i, w := range want {
		if f.Entries[i].Title != w {
			t.Errorf("entry %d = %q, want %q", i, f.Entries[i].Title, w)
		}
		if f.Entries[i].NavHref() == "" {
			t.Errorf("entry %q has no navigation link", w)
		}
	}
}

func TestLibraryAndPagination(t *testing.T) {
	f := load(t, "library1.xml")
	if f.Total != 2 || f.PerPage != 20 || f.StartIdx != 1 {
		t.Errorf("opensearch = %d/%d/%d", f.Total, f.PerPage, f.StartIdx)
	}
	if len(f.Entries) != 2 || f.Entries[0].Title != "Alpha Blade" {
		t.Fatalf("entries = %+v", f.Entries)
	}
	if f.Next() != "" {
		t.Errorf("unexpected next link %q", f.Next())
	}
	e := f.Entries[0]
	if e.NavHref() != "/api/opds/TESTKEY/series/1" {
		t.Errorf("nav = %q", e.NavHref())
	}
	if !strings.HasPrefix(e.ThumbHref(), "/api/image/series-cover?seriesId=1&apiKey=TESTKEY") {
		t.Errorf("thumb = %q (the &amp; must be decoded)", e.ThumbHref())
	}
	if e.Stream() != nil {
		t.Error("a series entry must not have a stream")
	}
}

func TestPaginationLinkWithLiteralAmp(t *testing.T) {
	xml := `<feed xmlns="http://www.w3.org/2005/Atom"><title>x</title>
	<link rel="next" href="/api/opds/K/libraries/1?a=1&amp;amp;pageNumber=2" type="x"/></feed>`
	f, err := ParseFeed([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Next(); got != "/api/opds/K/libraries/1?a=1&pageNumber=2" {
		t.Errorf("next = %q", got)
	}
}

func TestSeriesDetailStream(t *testing.T) {
	f := load(t, "series1.xml")
	if len(f.Entries) != 3 {
		t.Fatalf("entries = %d", len(f.Entries))
	}
	e := f.Entries[0]
	st := e.Stream()
	if st == nil {
		t.Fatal("no stream link")
	}
	if st.Count != 12 {
		t.Errorf("count = %d, want 12", st.Count)
	}
	if st.LastRead != -1 {
		t.Errorf("lastRead = %d, want -1 when absent", st.LastRead)
	}
	if got := st.PageURL(7, 1620, 2160); got != "/api/opds/TESTKEY/image?libraryId=1&seriesId=1&volumeId=1&chapterId=1&pageNumber=7" {
		t.Errorf("page url = %q", got)
	}
	// the acquisition link is not mistaken for the stream
	for _, l := range e.Links {
		if strings.Contains(l.Rel, "acquisition") && l.Count != 12 {
			t.Errorf("acquisition p5:count = %d", l.Count)
		}
	}
	title, prog := SplitProgress(e.Title)
	if title != "Alpha Blade - Volume 1" || prog != ProgressNone {
		t.Errorf("title = %q prog = %v", title, prog)
	}
	ids, ok := ParseIDs(st.Template)
	if !ok || ids != (IDs{Library: 1, Series: 1, Volume: 1, Chapter: 1}) {
		t.Errorf("ids = %+v ok=%v", ids, ok)
	}
}

func TestProgressAndContinueEntry(t *testing.T) {
	f := load(t, "series1-progress.xml")
	if len(f.Entries) != 4 {
		t.Fatalf("entries = %d, want continue + 3", len(f.Entries))
	}
	title, prog := SplitProgress(f.Entries[0].Title)
	if title != "Continue Reading from: Alpha Blade - Volume 1" || prog != ProgressHalf {
		t.Errorf("continue entry = %q %v", title, prog)
	}
	// The "Continue Reading" copy carries no lastRead (Kavita builds it from a DTO
	// without progress); volume 1 has 5 (zero-based index of the last page seen),
	// volume 2 has 3, volume 3 none.
	want := []int{-1, 5, 3, -1}
	for i, w := range want {
		st := f.Entries[i].Stream()
		if st == nil {
			t.Fatalf("entry %d has no stream", i)
		}
		if st.LastRead != w {
			t.Errorf("entry %d lastRead = %d, want %d", i, st.LastRead, w)
		}
	}
}

func TestSearchURL(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(fixtures, "search.xml"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := SearchURL(b, "blade & co")
	if err != nil {
		t.Fatal(err)
	}
	if u != "/api/opds/TESTKEY/series?query=blade+%26+co" {
		t.Errorf("search url = %q", u)
	}
	f := load(t, "search-results.xml")
	if len(f.Entries) == 0 {
		t.Error("search result feed has no entries")
	}
}

func TestSplitProgressVariants(t *testing.T) {
	cases := []struct {
		in    string
		out   string
		state Progress
	}{
		{"⭘ Vol 1", "Vol 1", ProgressNone},
		{"◔ Vol 1", "Vol 1", ProgressQuarter},
		{"◑ Vol 1", "Vol 1", ProgressHalf},
		{"◕ Vol 1", "Vol 1", ProgressMost},
		{"⬤ Vol 1", "Vol 1", ProgressFull},
		{"Vol 1", "Vol 1", ProgressUnknown},
		{"Continue from ◑ Vol 2", "Continue from Vol 2", ProgressHalf},
	}
	for _, c := range cases {
		got, p := SplitProgress(c.in)
		if got != c.out || p != c.state {
			t.Errorf("SplitProgress(%q) = %q, %v", c.in, got, p)
		}
	}
}

func TestStreamTemplatesAndNamespaces(t *testing.T) {
	// Another server could use any prefix and encode the braces.
	xml := `<feed xmlns="http://www.w3.org/2005/Atom" xmlns:pse="http://vaemendis.net/opds-pse/ns"><title>t</title>
	<entry><id>1</id><title>A</title>
	<link rel="http://vaemendis.net/opds-pse/stream" type="image/jpeg" href="/p?c=1&amp;n=%7BpageNumber%7D&amp;w=%7BmaxWidth%7D" pse:count="9" pse:lastRead="4"/>
	</entry></feed>`
	f, err := ParseFeed([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	st := f.Entries[0].Stream()
	if st == nil || st.Count != 9 || st.LastRead != 4 {
		t.Fatalf("stream = %+v", st)
	}
	if got := st.PageURL(3, 1620, 2160); got != "/p?c=1&n=3&w=1620" {
		t.Errorf("url = %q", got)
	}
}

// splitRoot is a test helper: origin and last path segment of a v1-style root.
func splitRoot(t testing.TB, root string) (*url.URL, string) {
	t.Helper()
	u, err := url.Parse(root)
	if err != nil || u.Host == "" {
		t.Fatalf("bad root %q", root)
	}
	p := strings.Trim(u.Path, "/")
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, p[strings.LastIndexByte(p, '/')+1:]
}

func TestRedaction(t *testing.T) {
	key := "0123456789abcdef" + "0123456789abcdef"
	r := Redactor{Key: key}
	in := "Get http://127.0.0.1:5001/api/opds/" + key + "/series/1?apiKey=" + key + "&x=1 failed; key " + key
	out := r.String(in)
	if strings.Contains(out, key) {
		t.Errorf("key survived redaction: %s", out)
	}
	// also redacts a key we were never told about, by shape
	if got := (Redactor{}).String("/api/opds/someotherkey/image?apiKey=abc"); strings.Contains(got, "someotherkey") || strings.Contains(got, "abc") {
		t.Errorf("shape redaction failed: %s", got)
	}
}

// ---- client -------------------------------------------------------------------

func newClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	base, key := splitRoot(t, srv.URL+"/api/opds/SECRETKEY123")
	return NewClient(base, key), srv
}

func TestClientPinsOriginAndUsesUA(t *testing.T) {
	var gotUA, gotPath string
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotPath = r.UserAgent(), r.URL.String()
		w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"><title>ok</title></feed>`))
	}))
	// the feed names its public hostname; the request must still go to the configured server
	f, err := c.GetFeed(context.Background(), "http://kavita.example.org:5000/api/opds/SECRETKEY123/libraries")
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "ok" || gotPath != "/api/opds/SECRETKEY123/libraries" {
		t.Errorf("title=%q path=%q", f.Title, gotPath)
	}
	if !strings.HasPrefix(gotUA, "rmKavita/") {
		t.Errorf("ua = %q", gotUA)
	}
}

func TestClientErrorKinds(t *testing.T) {
	status := int32(200)
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := int(atomic.LoadInt32(&status))
		w.WriteHeader(s)
		w.Write([]byte("body mentioning /api/opds/SECRETKEY123/x and SECRETKEY123"))
	}))
	cases := []struct {
		code int
		kind Kind
	}{{401, KindAuth}, {403, KindAuth}, {404, KindNotFound}, {500, KindServer}, {400, KindServer}}
	for _, tc := range cases {
		atomic.StoreInt32(&status, int32(tc.code))
		_, _, err := c.Get(context.Background(), "/x", 0)
		e, ok := err.(*Error)
		if !ok || e.Kind != tc.kind {
			t.Errorf("status %d: got %#v", tc.code, err)
			continue
		}
		if strings.Contains(e.Error(), "SECRETKEY123") {
			t.Errorf("status %d: key in error text: %s", tc.code, e.Error())
		}
		if e.Short() == "" {
			t.Error("empty short message")
		}
	}
}

func TestClientDoorDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens any more
	base, key := splitRoot(t, "http://"+addr+"/api/opds/K")
	c := NewClient(base, key)
	_, _, gerr := c.Get(context.Background(), "/x", 0)
	e, ok := gerr.(*Error)
	if !ok || e.Kind != KindDoor {
		t.Fatalf("got %#v, want KindDoor", gerr)
	}
}

func TestClientRetriesOnceOnDroppedConnection(t *testing.T) {
	var n int32
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			hj := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close() // what a dead tailscale nc looks like
			return
		}
		w.Write([]byte("fine"))
	}))
	b, _, err := c.Get(context.Background(), "/x", 0)
	if err != nil || string(b) != "fine" {
		t.Fatalf("b=%q err=%v", b, err)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Errorf("attempts = %d", n)
	}
}

func TestClientBodyLimit(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 5000))
	}))
	_, _, err := c.Get(context.Background(), "/x", 1000)
	if e, ok := err.(*Error); !ok || e.Kind != KindData {
		t.Fatalf("got %#v", err)
	}
}

func TestTouchDropsBody(t *testing.T) {
	var hit int32
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hit, 1)
		w.Header().Set("Content-Length", "50000000")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		// never finishes the body
		time.Sleep(200 * time.Millisecond)
	}))
	start := time.Now()
	if err := c.Touch(context.Background(), "/image?pageNumber=3"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Errorf("touch waited for the body: %v", time.Since(start))
	}
	if atomic.LoadInt32(&hit) != 1 {
		t.Error("server not hit")
	}
}

func TestParseFeedRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "   ", "<html><body>no</body></html>", "not xml"} {
		if _, err := ParseFeed([]byte(in)); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
}

// Real Kavita 0.9.1.4 answers for a Book library with two PDFs: each PDF is a
// page stream (JPEG pages) as well as a download; the series says "Format: Pdf".
func TestBooksLibraryFixturesFromRealKavita(t *testing.T) {
	dir := "testdata/kavita-0.9.1.4-books"
	lib, err := os.ReadFile(filepath.Join(dir, "library-books.xml"))
	if err != nil {
		t.Skipf("fixtures missing: %v", err)
	}
	f, err := ParseFeed(lib)
	if err != nil || len(f.Entries) != 2 {
		t.Fatalf("library: %v entries=%d", err, len(f.Entries))
	}
	for _, e := range f.Entries {
		if e.FormatHint() != "pdf" || !e.ImageFormat() || e.NavHref() == "" {
			t.Errorf("series %q: hint=%q", e.Title, e.FormatHint())
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "series-*.xml"))
	if len(matches) != 2 {
		t.Fatalf("series fixtures: %v", matches)
	}
	for _, m := range matches {
		b, _ := os.ReadFile(m)
		sf, err := ParseFeed(b)
		if err != nil || len(sf.Entries) != 1 {
			t.Fatalf("%s: %v", m, err)
		}
		e := sf.Entries[0]
		st := e.Stream()
		if st == nil || st.Count < 3 {
			t.Fatalf("%s: no page stream (%+v)", m, st)
		}
		if e.DocKind() != DocPDF {
			t.Errorf("%s: kind %v, want PDF", m, e.DocKind())
		}
		ids, ok := ParseIDs(st.Template)
		if !ok || ids.Library != 3 {
			t.Errorf("%s: ids %+v", m, ids)
		}
	}
}
