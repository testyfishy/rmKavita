// Package opds reads OPDS 1.x Atom catalogues and the OPDS-PSE page-streaming
// extension, as served by Kavita (/api/opds/{apiKey}/...).
package opds

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const (
	// RelStream is the OPDS-PSE page stream relation.
	RelStream = "http://vaemendis.net/opds-pse/stream"
	// NSPSE is the PSE attribute namespace (count, lastRead). Matching is by
	// local name, never by prefix: .NET picks prefixes like "p5".
	NSPSE = "http://vaemendis.net/opds-pse/ns"
)

// Link is an atom:link plus the PSE attributes.
type Link struct {
	Rel, Type, Href, Title string
	Count                  int // pse:count, 0 when absent
	LastRead               int // pse:lastRead (1-based count of pages read), -1 when absent
}

// Entry is one atom:entry.
type Entry struct {
	ID, Title, Summary string
	Links              []Link
}

// Feed is one atom:feed page.
type Feed struct {
	ID, Title string
	Links     []Link
	Entries   []Entry
	Total     int // opensearch:totalResults, 0 when absent
	PerPage   int
	StartIdx  int
}

// Stream is a page-streaming link of a readable entry.
type Stream struct {
	Template string // href with {pageNumber}
	Count    int    // number of pages
	LastRead int    // pages read so far according to the server, -1 if unknown
}

type xAttrLink struct {
	Rel   string     `xml:"rel,attr"`
	Type  string     `xml:"type,attr"`
	Href  string     `xml:"href,attr"`
	Title string     `xml:"title,attr"`
	Other []xml.Attr `xml:",any,attr"`
}

type xEntry struct {
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Summary string      `xml:"summary"`
	Content string      `xml:"content"`
	Links   []xAttrLink `xml:"link"`
}

type xFeed struct {
	XMLName xml.Name    `xml:"feed"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Total   string      `xml:"totalResults"`
	PerPage string      `xml:"itemsPerPage"`
	Start   string      `xml:"startIndex"`
	Links   []xAttrLink `xml:"link"`
	Entries []xEntry    `xml:"entry"`
}

func convLink(x xAttrLink) Link {
	l := Link{Rel: strings.TrimSpace(x.Rel), Type: strings.TrimSpace(x.Type), Href: fixHref(x.Href), Title: x.Title, LastRead: -1}
	for _, a := range x.Other {
		switch a.Name.Local {
		case "count":
			l.Count, _ = strconv.Atoi(strings.TrimSpace(a.Value))
		case "lastRead":
			if v, err := strconv.Atoi(strings.TrimSpace(a.Value)); err == nil {
				l.LastRead = v
			}
		}
	}
	return l
}

// fixHref repairs the one Kavita quirk seen in the source: pagination hrefs
// can carry a literal "&amp;" after XML decoding.
func fixHref(h string) string {
	h = strings.TrimSpace(h)
	if strings.Contains(h, "&amp;") {
		h = strings.ReplaceAll(h, "&amp;", "&")
	}
	return h
}

// ParseFeed parses an Atom feed document.
func ParseFeed(data []byte) (*Feed, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty feed")
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) { return in, nil }
	var x xFeed
	if err := dec.Decode(&x); err != nil {
		return nil, fmt.Errorf("feed xml: %w", err)
	}
	if x.XMLName.Local != "feed" {
		return nil, errors.New("not an atom feed")
	}
	f := &Feed{ID: strings.TrimSpace(x.ID), Title: strings.TrimSpace(x.Title)}
	f.Total, _ = strconv.Atoi(strings.TrimSpace(x.Total))
	f.PerPage, _ = strconv.Atoi(strings.TrimSpace(x.PerPage))
	f.StartIdx, _ = strconv.Atoi(strings.TrimSpace(x.Start))
	for _, l := range x.Links {
		f.Links = append(f.Links, convLink(l))
	}
	for _, e := range x.Entries {
		en := Entry{ID: strings.TrimSpace(e.ID), Title: collapse(e.Title), Summary: collapse(e.Summary)}
		if en.Summary == "" {
			en.Summary = collapse(e.Content)
		}
		for _, l := range e.Links {
			en.Links = append(en.Links, convLink(l))
		}
		f.Entries = append(f.Entries, en)
	}
	return f, nil
}

func collapse(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

func (f *Feed) link(rel string) string {
	for _, l := range f.Links {
		if l.Rel == rel {
			return l.Href
		}
	}
	return ""
}

// Next returns the href of the next feed page or "".
func (f *Feed) Next() string { return f.link("next") }

// Prev returns the previous page href or "".
func (f *Feed) Prev() string {
	if h := f.link("prev"); h != "" {
		return h
	}
	return f.link("previous")
}

// SearchDescription returns the href of the OpenSearch description or "".
func (f *Feed) SearchDescription() string {
	for _, l := range f.Links {
		if l.Rel == "search" && strings.Contains(l.Type, "opensearchdescription") {
			return l.Href
		}
	}
	return f.link("search")
}

// Stream returns the entry's PSE stream or nil.
func (e *Entry) Stream() *Stream {
	for _, l := range e.Links {
		if l.Rel == RelStream || strings.HasSuffix(l.Rel, "/opds-pse/stream") {
			if l.Href == "" {
				continue
			}
			return &Stream{Template: l.Href, Count: l.Count, LastRead: l.LastRead}
		}
	}
	return nil
}

// NavHref returns the catalogue link an entry leads to (a folder), or "".
func (e *Entry) NavHref() string {
	for _, l := range e.Links {
		if l.Rel == "subsection" {
			return l.Href
		}
	}
	for _, l := range e.Links {
		if strings.Contains(l.Type, "application/atom+xml") {
			switch l.Rel {
			case "self", "start", "search", "next", "prev", "previous", "up":
				continue
			}
			return l.Href
		}
	}
	return ""
}

// ThumbHref returns the cover thumbnail link or "".
func (e *Entry) ThumbHref() string {
	for _, l := range e.Links {
		if l.Rel == "http://opds-spec.org/image/thumbnail" {
			return l.Href
		}
	}
	for _, l := range e.Links {
		if l.Rel == "http://opds-spec.org/image" {
			return l.Href
		}
	}
	return ""
}

// PageURL expands the template for a zero-based page index. maxW/maxH fill the
// optional {maxWidth}/{maxHeight} variables of newer PSE versions.
func (s *Stream) PageURL(page, maxW, maxH int) string {
	u := s.Template
	for _, p := range []string{"{pageNumber}", "%7BpageNumber%7D", "%7bpageNumber%7d"} {
		u = strings.ReplaceAll(u, p, strconv.Itoa(page))
	}
	for _, p := range []string{"{maxWidth}", "%7BmaxWidth%7D", "%7bmaxWidth%7d"} {
		u = strings.ReplaceAll(u, p, strconv.Itoa(maxW))
	}
	for _, p := range []string{"{maxHeight}", "%7BmaxHeight%7D", "%7bmaxHeight%7d"} {
		u = strings.ReplaceAll(u, p, strconv.Itoa(maxH))
	}
	return u
}

// Progress is Kavita's reading indicator, which it prepends to titles.
type Progress int

const (
	ProgressUnknown Progress = iota
	ProgressNone
	ProgressQuarter
	ProgressHalf
	ProgressMost
	ProgressFull
)

// SplitProgress removes Kavita's progress glyph from a title. The glyph leads
// the title ("⭘ Vol. 1") or follows the localized "Continue from" prefix, so
// every occurrence is stripped and the first one decides the state.
func SplitProgress(title string) (string, Progress) {
	p := ProgressUnknown
	var b strings.Builder
	for _, r := range title {
		var g Progress
		switch r {
		case '⭘':
			g = ProgressNone
		case '◔':
			g = ProgressQuarter
		case '◑':
			g = ProgressHalf
		case '◕':
			g = ProgressMost
		case '⬤':
			g = ProgressFull
		default:
			b.WriteRune(r)
			continue
		}
		if p == ProgressUnknown {
			p = g
		}
	}
	if p == ProgressUnknown {
		return title, p
	}
	return strings.Join(strings.Fields(b.String()), " "), p
}

// IDs are Kavita's identifiers embedded in a stream template.
type IDs struct{ Library, Series, Volume, Chapter int }

// ParseIDs reads libraryId/seriesId/volumeId/chapterId from a stream href.
func ParseIDs(href string) (IDs, bool) {
	i := strings.IndexByte(href, '?')
	if i < 0 {
		return IDs{}, false
	}
	q, err := url.ParseQuery(href[i+1:])
	if err != nil {
		return IDs{}, false
	}
	var ids IDs
	var ok bool
	get := func(k string) int {
		v, e := strconv.Atoi(q.Get(k))
		if e != nil {
			return 0
		}
		return v
	}
	ids.Library, ids.Series, ids.Volume, ids.Chapter = get("libraryId"), get("seriesId"), get("volumeId"), get("chapterId")
	ok = ids.Chapter != 0
	return ids, ok
}

// StreamTemplate rebuilds a stream template under rootPath (".../api/opds/KEY").
func StreamTemplate(rootPath string, ids IDs) string {
	return fmt.Sprintf("%s/image?libraryId=%d&seriesId=%d&volumeId=%d&chapterId=%d&pageNumber={pageNumber}",
		strings.TrimRight(rootPath, "/"), ids.Library, ids.Series, ids.Volume, ids.Chapter)
}

// SearchURL expands an OpenSearch description's template for a query. The
// description is the XML document Kavita serves at .../search.
func SearchURL(desc []byte, query string) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(desc))
	dec.Strict = false
	best := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Url" {
			continue
		}
		var typ, tmpl string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "type":
				typ = a.Value
			case "template":
				tmpl = a.Value
			}
		}
		if tmpl == "" {
			continue
		}
		if strings.Contains(typ, "atom+xml") && (best == "" || strings.Contains(typ, "acquisition")) {
			best = tmpl
		}
	}
	if best == "" {
		return "", errors.New("no search template")
	}
	q := url.QueryEscape(query)
	for _, p := range []string{"{searchTerms}", "%7BsearchTerms%7D", "%7bsearchTerms%7d"} {
		best = strings.ReplaceAll(best, p, q)
	}
	best = fixHref(best)
	return best, nil
}

// Doc is the kind of document a streamable entry is.
type Doc int

const (
	DocComic Doc = iota // manga, comics: image archives and image folders
	DocPDF              // a PDF, streamed as page images by Kavita
)

// DocKind classifies an entry from its acquisition link types: a PDF is a
// "pdf" file; everything else that streams is treated as a comic.
func (e *Entry) DocKind() Doc {
	for _, l := range e.Links {
		if strings.HasPrefix(l.Rel, "http://opds-spec.org/acquisition") && strings.Contains(strings.ToLower(l.Type), "pdf") {
			return DocPDF
		}
	}
	return DocComic
}

// FormatHint is the lower-case format word of a series entry's summary
// ("Format: Archive" -> "archive"), "" when the summary has none.
func (e *Entry) FormatHint() string {
	s := strings.TrimSpace(e.Summary)
	if i := strings.Index(strings.ToLower(s), "format:"); i >= 0 {
		f := strings.Fields(s[i+len("format:"):])
		if len(f) > 0 {
			return strings.ToLower(strings.Trim(f[0], ".,;"))
		}
	}
	return ""
}

// ImageFormat reports whether a series entry holds image pages (or does not
// say): only archives, image folders and PDFs are listed.
func (e *Entry) ImageFormat() bool {
	switch e.FormatHint() {
	case "", "archive", "image", "pdf":
		return true
	}
	return false
}
