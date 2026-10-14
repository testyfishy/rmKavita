// Package mockkavita is a stand-in for Kavita's OPDS / OPDS-PSE endpoints. Its
// XML and URL shapes were copied from a real Kavita v0.9.1.4 instance (the
// fixtures under internal/opds/testdata/kavita-0.9.1.4), so tests that pass
// here exercise the same parsing as the real server. Page images are generated
// on demand (internal/synth), so no manga is needed.
package mockkavita

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rmkavita/internal/gfx"
	"rmkavita/internal/synth"
)

type chapter struct {
	id, vol, series, lib int
	title                string
	pages                int
	w, h                 int
	kind                 string // jpeg | jpegcolor | png | webp | spread | huge
	seed                 int64
}

type seriesT struct {
	id, lib  int
	name     string
	chapters []*chapter
	kind     string
}

type libT struct {
	id     int
	name   string
	series []*seriesT
}

type progressT struct {
	idx  int
	date time.Time
}

// Server is the mock catalogue.
type Server struct {
	key      string
	libs     []*libT
	seriesBy map[int]*seriesT
	chapBy   map[int]*chapter
	fonts    *gfx.Fonts
	webp     []byte
	delay    time.Duration
	pageSize int

	mu           sync.Mutex
	progress     map[int]progressT
	reqLog       []string
	failNext     int
	failCode     int
	failFeedPage int               // catalogue requests with this pageNumber answer 500 while it is non-zero
	users        map[string]string // user name -> password (sign-in)
	fails        map[string]int    // wrong attempts per user
	logins       int               // sign-in requests seen
	lockedUntil  map[string]time.Time
	reqs         []string // every request path (not only images), newest last
	cache        map[string][]byte
	order        []string
	hits         map[string]int
}

const glyphNone, glyphQuarter, glyphHalf, glyphMost, glyphFull = "⭘", "◔", "◑", "◕", "⬤"

func glyph(read, total int) string {
	if read <= 0 || total <= 0 {
		return glyphNone
	}
	f := float64(read) / float64(total)
	switch {
	case f >= 1:
		return glyphFull
	case f > 0.5:
		return glyphMost
	case f > 0.25:
		return glyphHalf
	}
	return glyphQuarter
}

func Build(nSeriesA int, pagesPer int, w, h int) *Server {
	s := &Server{key: "mockkey0123456789abcdef01234567", seriesBy: map[int]*seriesT{}, chapBy: map[int]*chapter{},
		progress: map[int]progressT{}, cache: map[string][]byte{}, hits: map[string]int{}, pageSize: 20,
		users: map[string]string{"reader": "correct horse"}, fails: map[string]int{}, lockedUntil: map[string]time.Time{}}
	nextSeries, nextChap, nextVol := 1, 1, 1
	add := func(lib *libT, name, kind string, nch, pages, w, h int) {
		sr := &seriesT{id: nextSeries, lib: lib.id, name: name, kind: kind}
		nextSeries++
		for c := 1; c <= nch; c++ {
			ch := &chapter{id: nextChap, vol: nextVol, series: sr.id, lib: lib.id,
				title: fmt.Sprintf("%s - Volume %d", name, c), pages: pages, w: w, h: h, kind: kind, seed: int64(nextChap) * 7919}
			nextChap++
			nextVol++
			sr.chapters = append(sr.chapters, ch)
			s.chapBy[ch.id] = ch
		}
		lib.series = append(lib.series, sr)
		s.seriesBy[sr.id] = sr
	}
	a := &libT{id: 1, name: "Manga A"}
	for i := 0; i < nSeriesA; i++ {
		add(a, fmt.Sprintf("Series %02d", i+1), "jpeg", 3+i%4, pagesPer, w, h)
	}
	bl := &libT{id: 2, name: "Manga B"}
	add(bl, "Color JPEG Scans", "jpegcolor", 2, 10, w, h)
	add(bl, "PNG Pages", "png", 2, 8, 1000, 1500)
	add(bl, "WebP Pages", "webp", 1, 5, 0, 0)
	add(bl, "Double Page Spreads", "spread", 1, 6, h, w)
	add(bl, "Huge Scans", "huge", 1, 6, 3000, 4500)
	add(bl, "Tiny Series With A Very Long Title That Must Not Overflow The Row Of The List View", "jpeg", 1, 5, 900, 1350)
	add(bl, "日本語のタイトル Japanese", "jpeg", 1, 5, 900, 1350)
	add(bl, "Text PDF", "pdf", 2, 6, 1020, 1320)
	add(bl, "Document Only", "doc", 2, 3, 0, 0)
	add(bl, "Black Pages", "black", 1, 5, 900, 1350)
	add(bl, "Wide Margins", "margins", 1, 5, 900, 1350)
	add(bl, "Tinted Scans", "tinted", 1, 5, 900, 1350)
	add(bl, "Mixed Pages", "mixed", 1, 6, 900, 1350)
	add(bl, "Grey Field", "grey", 1, 5, 900, 1350)
	add(bl, "Black Colour", "blackcolour", 1, 5, 900, 1350)
	add(bl, "Grey Colour", "greycolour", 1, 5, 900, 1350)
	empty := &libT{id: 3, name: "Empty Library"}
	s.libs = []*libT{a, bl, empty}
	return s
}

// ---- XML ----------------------------------------------------------------------

func esc(s string) string {
	var b bytes.Buffer
	_ = xmlEscape(&b, s)
	return b.String()
}

func xmlEscape(b *bytes.Buffer, s string) error {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return nil
}

const feedHead = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns="http://www.w3.org/2005/Atom">
`

func (s *Server) prefix() string { return "/api/opds/" + s.key }

type feedB struct {
	b bytes.Buffer
}

func (s *Server) feed(id, title, selfHref, selfKind string, total, perPage, startIdx int) *feedB {
	f := &feedB{}
	now := time.Now().Format("2006-01-02T15:04:05")
	f.b.WriteString(feedHead)
	fmt.Fprintf(&f.b, "  <updated>%s</updated>\n  <id>%s</id>\n  <title>%s</title>\n  <icon>%s/favicon</icon>\n", now, esc(id), esc(title), s.prefix())
	f.b.WriteString("  <author>\n    <name>Kavita</name>\n    <uri>https://www.rmkavita.com</uri>\n  </author>\n")
	if total >= 0 {
		fmt.Fprintf(&f.b, "  <totalResults xmlns=\"http://a9.com/-/spec/opensearch/1.1/\">%d</totalResults>\n", total)
		fmt.Fprintf(&f.b, "  <itemsPerPage xmlns=\"http://a9.com/-/spec/opensearch/1.1/\">%d</itemsPerPage>\n", perPage)
		fmt.Fprintf(&f.b, "  <startIndex xmlns=\"http://a9.com/-/spec/opensearch/1.1/\">%d</startIndex>\n", startIdx)
	}
	fmt.Fprintf(&f.b, "  <link rel=\"self\" type=\"application/atom+xml;profile=opds-catalog;kind=%s\" href=\"%s\" />\n", selfKind, esc(selfHref))
	fmt.Fprintf(&f.b, "  <link rel=\"start\" type=\"application/atom+xml;profile=opds-catalog;kind=navigation\" href=\"%s\" />\n", s.prefix())
	fmt.Fprintf(&f.b, "  <link rel=\"search\" type=\"application/opensearchdescription+xml\" href=\"%s/search\" />\n", s.prefix())
	return f
}

func (f *feedB) link(rel, typ, href string) {
	fmt.Fprintf(&f.b, "  <link rel=\"%s\" type=\"%s\" href=\"%s\" />\n", rel, typ, esc(href))
}

func (f *feedB) end() []byte {
	f.b.WriteString("</feed>")
	return f.b.Bytes()
}

const navType = "application/atom+xml;profile=opds-catalog;kind=navigation"

func (s *Server) navEntry(f *feedB, id, title, summary, href, coverHref string) {
	now := time.Now().Format("2006-01-02T15:04:05")
	f.b.WriteString("  <entry>\n")
	fmt.Fprintf(&f.b, "    <updated>%s</updated>\n    <id>%s</id>\n    <title>%s</title>\n", now, esc(id), esc(title))
	if summary != "" {
		fmt.Fprintf(&f.b, "    <summary>%s</summary>\n", esc(summary))
	}
	fmt.Fprintf(&f.b, "    <link rel=\"subsection\" type=\"%s\" href=\"%s\" />\n", navType, esc(href))
	if coverHref != "" {
		fmt.Fprintf(&f.b, "    <link rel=\"http://opds-spec.org/image\" type=\"image/jpeg\" href=\"%s\" />\n", esc(coverHref))
		fmt.Fprintf(&f.b, "    <link rel=\"http://opds-spec.org/image/thumbnail\" type=\"image/jpeg\" href=\"%s\" />\n", esc(coverHref))
	}
	f.b.WriteString("  </entry>\n")
}

func (s *Server) chapterEntry(f *feedB, c *chapter, titlePrefix string) {
	now := time.Now().Format("2006-01-02T15:04:05")
	s.mu.Lock()
	pr, hasProg := s.progress[c.id]
	s.mu.Unlock()
	read := 0
	if hasProg {
		read = pr.idx
	}
	title := glyph(read, c.pages) + " " + c.title
	if titlePrefix != "" {
		title = titlePrefix + glyph(read, c.pages) + " " + c.title
	}
	ext, mime := ".cbz", "application/x-cbz"
	switch c.kind {
	case "pdf":
		ext, mime = ".pdf", "application/pdf"
	case "doc":
		ext, mime = ".bin", "application/octet-stream"
	}
	fn := strings.ReplaceAll(c.title, " ", "%20") + ext
	f.b.WriteString("  <entry>\n")
	fmt.Fprintf(&f.b, "    <updated>%s</updated>\n    <id>%d</id>\n    <title>%s</title>\n", now, c.id, esc(title))
	fmt.Fprintf(&f.b, "    <summary>File Type: %s - 3.2 MB</summary>\n", strings.TrimPrefix(mime, "application/"))
	f.b.WriteString("    <extent xmlns=\"http://purl.org/dc/terms/\">3.2 MB</extent>\n")
	f.b.WriteString("    <format xmlns=\"http://purl.org/dc/terms/format\">Archive</format>\n")
	fmt.Fprintf(&f.b, "    <content type=\"text\">%s</content>\n", mime)
	cover := fmt.Sprintf("/api/image/chapter-cover?chapterId=%d&apiKey=%s", c.id, s.key)
	fmt.Fprintf(&f.b, "    <link rel=\"http://opds-spec.org/image\" type=\"image/jpeg\" href=\"%s\" />\n", esc(cover))
	fmt.Fprintf(&f.b, "    <link rel=\"http://opds-spec.org/image/thumbnail\" type=\"image/jpeg\" href=\"%s\" />\n", esc(cover))
	fmt.Fprintf(&f.b, "    <link rel=\"http://opds-spec.org/acquisition/open-access\" type=\"%s\" href=\"%s/series/%d/volume/%d/chapter/%d/download/%s\" title=\"%s\" p5:count=\"%d\" xmlns:p5=\"http://vaemendis.net/opds-pse/ns\" />\n",
		mime, s.prefix(), c.series, c.vol, c.id, fn, fn, c.pages)
	if c.kind == "doc" {
		f.b.WriteString("  </entry>\n")
		return
	}
	href := fmt.Sprintf("%s/image?libraryId=%d&seriesId=%d&volumeId=%d&chapterId=%d&pageNumber={pageNumber}", s.prefix(), c.lib, c.series, c.vol, c.id)
	last := ""
	if hasProg {
		last = fmt.Sprintf(" p5:lastRead=\"%d\" p5:lastReadDate=\"%s\"", pr.idx, pr.date.Format("2006-01-02T15:04:05"))
	}
	fmt.Fprintf(&f.b, "    <link rel=\"http://vaemendis.net/opds-pse/stream\" type=\"image/jpeg\" href=\"%s\" p5:count=\"%d\"%s xmlns:p5=\"http://vaemendis.net/opds-pse/ns\" />\n",
		esc(href), c.pages, last)
	f.b.WriteString("  </entry>\n")
}

func (s *Server) paged(f *feedB, base string, page, total int) {
	pages := (total + s.pageSize - 1) / s.pageSize
	if page > 1 {
		f.link("prev", navType, fmt.Sprintf("%s?pageNumber=%d", base, page-1))
	}
	if page+1 <= pages {
		f.link("next", navType, fmt.Sprintf("%s?pageNumber=%d", base, page+1))
	}
}

// ---- handlers ----------------------------------------------------------------

func (s *Server) logf(f string, a ...any) {
	line := fmt.Sprintf(f, a...)
	s.mu.Lock()
	s.reqLog = append(s.reqLog, line)
	if len(s.reqLog) > 5000 {
		s.reqLog = s.reqLog[len(s.reqLog)-2500:]
	}
	s.mu.Unlock()
}

func (s *Server) xml(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/xml")
	w.Write(b)
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	p := r.URL.Path
	s.mu.Lock()
	s.reqs = append(s.reqs, p)
	if len(s.reqs) > 5000 {
		s.reqs = s.reqs[len(s.reqs)-2500:]
	}
	ff := s.failFeedPage
	s.mu.Unlock()
	if ff != 0 && strings.HasPrefix(p, "/api/opds/") && !strings.HasSuffix(p, "/image") && r.URL.Query().Get("pageNumber") == strconv.Itoa(ff) {
		http.Error(w, "injected feed failure", http.StatusInternalServerError)
		return
	}
	switch {
	case p == "/api/health":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("Ok"))
		return
	case p == "/api/Account/login" && r.Method == http.MethodPost:
		s.login(w, r)
		return
	case strings.HasPrefix(p, "/_mock/"):
		s.debug(w, r)
		return
	case strings.HasPrefix(p, "/api/image/"):
		s.cover(w, r)
		return
	}
	pre := s.prefix()
	if !strings.HasPrefix(p, "/api/opds/") {
		http.NotFound(w, r)
		return
	}
	if p != pre && !strings.HasPrefix(p, pre+"/") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	rest := strings.TrimPrefix(p, pre)
	q := r.URL.Query()
	switch {
	case rest == "" || rest == "/":
		s.root(w)
	case rest == "/libraries":
		s.libraries(w)
	case strings.HasPrefix(rest, "/libraries/"):
		s.library(w, atoi(strings.TrimPrefix(rest, "/libraries/"), 0), atoi(q.Get("pageNumber"), 1))
	case strings.HasPrefix(rest, "/series/") && strings.Count(rest, "/") == 2:
		s.seriesDetail(w, atoi(strings.TrimPrefix(rest, "/series/"), 0))
	case rest == "/series":
		s.search(w, q.Get("query"))
	case rest == "/search":
		s.xml(w, []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<OpenSearchDescription xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>Search</ShortName>
  <Description>Search for Series, Collections, or Reading Lists</Description>
  <Url type="application/atom+xml;profile=opds-catalog;kind=acquisition" template="%s/series?query={searchTerms}" />
  <Tags />
  <InputEncoding>UTF-8</InputEncoding>
  <OutputEncoding>UTF-8</OutputEncoding>
  <Developer>rmkavita.com</Developer>
</OpenSearchDescription>`, pre)))
	case rest == "/image":
		s.image(w, r)
	case rest == "/favicon":
		w.Header().Set("Content-Type", "image/x-icon")
		w.Write([]byte{0, 0, 1, 0})
	case rest == "/on-deck", rest == "/recently-updated", rest == "/recently-added", rest == "/reading-list", rest == "/want-to-read", rest == "/collections":
		f := s.feed(strings.TrimPrefix(rest, "/"), strings.TrimPrefix(rest, "/"), pre+rest, "acquisition", -1, 0, 0)
		if rest == "/recently-added" || rest == "/on-deck" || rest == "/recently-updated" {
			for _, sr := range s.libs[0].series[:min(5, len(s.libs[0].series))] {
				s.navEntry(f, strconv.Itoa(sr.id), sr.name, fmtOf(sr.kind), fmt.Sprintf("%s/series/%d", pre, sr.id), fmt.Sprintf("/api/image/series-cover?seriesId=%d&apiKey=%s", sr.id, s.key))
			}
		}
		s.xml(w, f.end())
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) root(w http.ResponseWriter) {
	pre := s.prefix()
	f := s.feed("root", "Kavita", "/api/opds/", "navigation", -1, 0, 0)
	for _, e := range [][3]string{{"onDeck", "On Deck", "/on-deck"}, {"recentlyUpdated", "Recently Updated", "/recently-updated"},
		{"recentlyAdded", "Recently Added", "/recently-added"}, {"readingList", "Reading Lists", "/reading-list"},
		{"wantToRead", "Want to Read", "/want-to-read"}, {"allLibraries", "All Libraries", "/libraries"}, {"allCollections", "All Collections", "/collections"}} {
		now := time.Now().Format("2006-01-02T15:04:05")
		fmt.Fprintf(&f.b, "  <entry>\n    <updated>%s</updated>\n    <id>%s</id>\n    <title>%s</title>\n    <content type=\"text\">Browse %s</content>\n    <link rel=\"subsection\" type=\"%s\" href=\"%s%s\" />\n  </entry>\n",
			now, e[0], e[1], e[1], navType, pre, e[2])
	}
	s.xml(w, f.end())
}

func (s *Server) libraries(w http.ResponseWriter) {
	pre := s.prefix()
	f := s.feed("libraries", "All Libraries", pre+"/libraries", "acquisition", -1, 0, 0)
	for _, l := range s.libs {
		s.navEntry(f, strconv.Itoa(l.id), l.name, "", fmt.Sprintf("%s/libraries/%d", pre, l.id),
			fmt.Sprintf("/api/image/library-cover?libraryId=%d&apiKey=%s", l.id, s.key))
	}
	s.xml(w, f.end())
}

func (s *Server) library(w http.ResponseWriter, id, page int) {
	var l *libT
	for _, x := range s.libs {
		if x.id == id {
			l = x
		}
	}
	if l == nil {
		http.Error(w, "library", http.StatusBadRequest)
		return
	}
	pre := s.prefix()
	base := fmt.Sprintf("%s/libraries/%d", pre, id)
	if page < 1 {
		page = 1
	}
	lo := (page - 1) * s.pageSize
	hi := min(lo+s.pageSize, len(l.series))
	f := s.feed("library-"+l.name, l.name, fmt.Sprintf("%s?pageNumber=%d", base, page), "acquisition", len(l.series), s.pageSize, lo+1)
	s.paged(f, base, page, len(l.series))
	if lo < len(l.series) {
		for _, sr := range l.series[lo:hi] {
			s.navEntry(f, strconv.Itoa(sr.id), sr.name, fmtOf(sr.kind), fmt.Sprintf("%s/series/%d", pre, sr.id),
				fmt.Sprintf("/api/image/series-cover?seriesId=%d&apiKey=%s", sr.id, s.key))
		}
	}
	s.xml(w, f.end())
}

func (s *Server) seriesDetail(w http.ResponseWriter, id int) {
	sr := s.seriesBy[id]
	if sr == nil {
		http.Error(w, "series", http.StatusBadRequest)
		return
	}
	pre := s.prefix()
	f := s.feed(fmt.Sprintf("series-%d", sr.id), sr.name+" - Storyline", fmt.Sprintf("%s/series/%d", pre, sr.id), "acquisition", -1, 0, 0)
	f.link("http://opds-spec.org/image", "image/jpeg", fmt.Sprintf("/api/image/series-cover?seriesId=%d&apiKey=%s", sr.id, s.key))
	// "Continue Reading from:" copy of the chapter with the latest progress
	var cont *chapter
	var latest time.Time
	s.mu.Lock()
	for _, c := range sr.chapters {
		if p, ok := s.progress[c.id]; ok && p.date.After(latest) {
			latest, cont = p.date, c
		}
	}
	s.mu.Unlock()
	if cont != nil {
		s.chapterEntry(f, cont, "Continue Reading from: ")
	}
	for _, c := range sr.chapters {
		s.chapterEntry(f, c, "")
	}
	s.xml(w, f.end())
}

func (s *Server) search(w http.ResponseWriter, query string) {
	pre := s.prefix()
	f := s.feed("search", "Search "+query, pre+"/series?query="+url.QueryEscape(query), "acquisition", -1, 0, 0)
	q := strings.ToLower(query)
	n := 0
	for _, l := range s.libs {
		for _, sr := range l.series {
			if strings.Contains(strings.ToLower(sr.name), q) && n < 30 {
				n++
				s.navEntry(f, strconv.Itoa(sr.id), sr.name, fmtOf(sr.kind), fmt.Sprintf("%s/series/%d", pre, sr.id),
					fmt.Sprintf("/api/image/series-cover?seriesId=%d&apiKey=%s", sr.id, s.key))
			}
		}
	}
	s.xml(w, f.end())
}

func (s *Server) pageBytes(c *chapter, n int) ([]byte, string) {
	key := fmt.Sprintf("%d/%d", c.id, n)
	s.mu.Lock()
	if b, ok := s.cache[key]; ok {
		s.mu.Unlock()
		return b, mimeOf(b)
	}
	s.mu.Unlock()
	var b []byte
	label := synth.Label(fmt.Sprintf("%s", c.title), n)
	switch c.kind {
	case "webp":
		if n == c.pages-1 || len(s.webp) == 0 {
			b = s.webp
		} else {
			b = synth.JPEG(synth.Page(c.seed+int64(n), 1000, 1500, label, s.fonts), 85)
		}
	case "black":
		b = synth.JPEG(synth.BlackPage(c.seed+int64(n), c.w, c.h, label, s.fonts), 85)
	case "margins":
		b = synth.JPEG(synth.MarginPage(c.seed+int64(n), c.w, c.h, label, s.fonts), 85)
	case "png":
		b = synth.PNG(synth.Page(c.seed+int64(n), c.w, c.h, label, s.fonts))
	case "mixed": // colour on even pages, grey on odd ones
		if n%2 == 0 {
			b = synth.JPEGColor(synth.Page(c.seed+int64(n), c.w, c.h, label, s.fonts), 82)
		} else {
			b = synth.JPEG(synth.Page(c.seed+int64(n), c.w, c.h, label, s.fonts), 85)
		}
	case "grey":
		b = synth.JPEG(synth.GreyPage(c.seed+int64(n), c.w, c.h, label, s.fonts), 85)
	case "blackcolour": // a black page that really has colour in it
		g := synth.BlackPage(c.seed+int64(n), c.w, c.h, label, s.fonts)
		b = synth.JPEGColor(g, 85)
	case "greycolour":
		g := synth.GreyPage(c.seed+int64(n), c.w, c.h, label, s.fonts)
		b = synth.JPEGColor(g, 85)
	case "tinted":
		b = synth.JPEGFaint(synth.Page(c.seed+int64(n), c.w, c.h, label, s.fonts), 82)
	case "jpegcolor":
		b = synth.JPEGColor(synth.Page(c.seed+int64(n), c.w, c.h, label, s.fonts), 82)
	case "spread":
		w, h := c.w, c.h
		if n%2 == 1 {
			w, h = c.h, c.w
		}
		b = synth.JPEG(synth.Page(c.seed+int64(n), w, h, label, s.fonts), 85)
	default:
		b = synth.JPEG(synth.Page(c.seed+int64(n), c.w, c.h, label, s.fonts), 85)
	}
	s.mu.Lock()
	if len(s.order) > 60 {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.cache, old)
	}
	s.cache[key] = b
	s.order = append(s.order, key)
	s.mu.Unlock()
	return b, mimeOf(b)
}

func mimeOf(b []byte) string {
	switch {
	case len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8:
		return "image/jpeg"
	case len(b) > 8 && string(b[1:4]) == "PNG":
		return "image/png"
	case len(b) > 12 && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return "application/octet-stream"
}

func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c := s.chapBy[atoi(q.Get("chapterId"), 0)]
	n := atoi(q.Get("pageNumber"), -1)
	s.logf("image chapter=%d page=%d ua=%q", atoi(q.Get("chapterId"), 0), n, r.UserAgent())
	if c == nil || n < 0 {
		http.Error(w, "greater-0", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	fail := s.failNext > 0
	code := s.failCode
	if fail {
		s.failNext--
	}
	s.mu.Unlock()
	if fail {
		http.Error(w, "injected failure", code)
		return
	}
	idx := n
	if idx >= c.pages {
		idx = c.pages - 1 // like Kavita: a page past the end serves the last page
	}
	b, m := s.pageBytes(c, idx)
	s.mu.Lock()
	s.progress[c.id] = progressT{idx: min(n, c.pages), date: time.Now()}
	s.hits[fmt.Sprintf("c%d", c.id)]++
	s.mu.Unlock()
	sum := sha256.Sum256(b)
	w.Header().Set("Content-Type", m)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.Header().Set("Cache-Control", "max-age=86400, private")
	w.Write(b)
}

func (s *Server) cover(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("apiKey") != s.key {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if q.Get("libraryId") != "" { // Kavita has no cover for a library
		s.logf("cover library %s", q.Get("libraryId"))
		http.NotFound(w, r)
		return
	}
	id := atoi(q.Get("seriesId"), atoi(q.Get("chapterId"), 0))
	s.logf("cover %d", id)
	g := synth.Page(int64(id)*31+5, 240, 360, strconv.Itoa(id), s.fonts)
	w.Header().Set("Content-Type", "image/png")
	w.Write(synth.PNG(g))
}

func (s *Server) debug(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch strings.TrimPrefix(r.URL.Path, "/_mock/") {
	case "log":
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, strings.Join(s.reqLog, "\n"))
	case "progress":
		s.mu.Lock()
		defer s.mu.Unlock()
		out := map[string]int{}
		for id, p := range s.progress {
			out[strconv.Itoa(id)] = p.idx
		}
		keys := make([]string, 0, len(out))
		for k := range out {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		json.NewEncoder(w).Encode(out)
	case "failnext":
		s.mu.Lock()
		s.failNext = atoi(q.Get("n"), 1)
		s.failCode = atoi(q.Get("status"), 500)
		s.mu.Unlock()
		fmt.Fprintln(w, "ok")
	case "reset":
		s.mu.Lock()
		s.progress = map[int]progressT{}
		s.reqLog = nil
		s.failNext = 0
		s.mu.Unlock()
		fmt.Fprintln(w, "ok")
	case "delay":
		s.delay = time.Duration(atoi(q.Get("ms"), 0)) * time.Millisecond
		fmt.Fprintln(w, "ok")
	default:
		http.NotFound(w, r)
	}
}

// Key returns the API key the mock accepts.
func (s *Server) Key() string { return s.key }

// SetFonts sets the font set used to draw page labels.
func (s *Server) SetFonts(f *gfx.Fonts) { s.fonts = f }

// SetWebP sets the bytes served as the last page of the WebP series.
func (s *Server) SetWebP(b []byte) { s.webp = b }

// SetDelay adds latency to every request.
func (s *Server) SetDelay(d time.Duration) { s.delay = d }

// RequestLog returns the recorded image requests.
func (s *Server) RequestLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqLog...)
}

// Progress returns the last page index the server saw per chapter id.
func (s *Server) Progress() map[int]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]int{}
	for id, p := range s.progress {
		out[id] = p.idx
	}
	return out
}

// FailNext makes the next n image requests fail with status.
func (s *Server) FailNext(n, status int) {
	s.mu.Lock()
	s.failNext, s.failCode = n, status
	s.mu.Unlock()
}

// ResetLog clears the request log.
func (s *Server) ResetLog() {
	s.mu.Lock()
	s.reqLog = nil
	s.mu.Unlock()
}

// FailFeedPage makes every catalogue request whose pageNumber is n answer 500
// until it is called again with 0 (it does not affect images).
func (s *Server) FailFeedPage(n int) {
	s.mu.Lock()
	s.failFeedPage = n
	s.mu.Unlock()
}

// Requests returns every request path seen (image, catalogue, cover) in order,
// without query strings, so tests can prove what was and was not fetched.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

// fmtOf is the "Format:" summary Kavita puts on a series entry.
func fmtOf(kind string) string {
	switch kind {
	case "pdf":
		return "Format: Pdf"
	case "doc":
		return "Format: Doc"
	}
	return "Format: Archive"
}

// login mimics Kavita's POST /api/Account/login: 200 with the user DTO, or 401
// with a plain-text reason; five wrong attempts lock the user for ten minutes.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in)
	s.mu.Lock()
	s.logins++
	pw, known := s.users[in.Username]
	until := s.lockedUntil[in.Username]
	locked := known && time.Now().Before(until)
	ok := known && !locked && pw == in.Password
	if known && !locked && !ok {
		s.fails[in.Username]++
		if s.fails[in.Username] >= 5 {
			s.lockedUntil[in.Username] = time.Now().Add(10 * time.Minute)
		}
	}
	if ok {
		s.fails[in.Username] = 0
	}
	key := s.key
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	switch {
	case locked:
		http.Error(w, "You've been locked out from too many authorization attempts. Please wait 10 minutes.", http.StatusUnauthorized)
	case !ok:
		http.Error(w, "Your credentials are not correct", http.StatusUnauthorized)
	default:
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":2,"username":%q,"email":"","roles":["Login"],"token":"x","refreshToken":"y","apiKey":%q,"kavitaVersion":"0.9.1.4","identityProvider":0,"authKeys":[{"id":1,"key":%q,"name":"opds"},{"id":2,"key":%q,"name":"image-only"}]}`,
			in.Username, key, key, "0000000000"+"0000000000"+"00000000image")
	}
}

// Logins returns how many sign-in requests the server has seen.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// RevokeKey replaces the API key: every URL built with the old one now answers
// 401, as after a key rotation or a deleted user.
func (s *Server) RevokeKey() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = "rotatedkey" + "0123456789" + "abcdef012345"
}

// AddUser registers a sign-in.
func (s *Server) AddUser(name, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[name] = password
}
