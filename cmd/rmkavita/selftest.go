package main

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"rmkavita/internal/imgproc"
	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

func statusKB(key string) int {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, key+":") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				n, _ := strconv.Atoi(f[1])
				return n
			}
		}
	}
	return -1
}

func rssKB() int { return statusKB("VmRSS") }

// percentile returns the p-th percentile (0..100, nearest rank) of v.
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	rank := int(math.Ceil(p/100*float64(len(s)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}

// spread returns up to n page numbers spread evenly over 0..count-1, the first
// and the last included. (The self-test samples 0..count-2 instead: a request
// for the last page makes Kavita mark a chapter as read.)
func spread(count, n int) []int {
	if count <= 0 || n <= 0 {
		return nil
	}
	if count <= n {
		out := make([]int, count)
		for i := range out {
			out[i] = i
		}
		return out
	}
	if n == 1 {
		return []int{0}
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i * (count - 1) / (n - 1)
	}
	return out
}

// sampleSpan is how many pages the self-test may request: all but the last one
// (a single-page chapter has only that one).
func sampleSpan(count int) int {
	if count > 1 {
		return count - 1
	}
	return count
}

// a53Factor is the rough slowdown of the Paper Pro's Cortex-A53 against the
// machine the self-test usually runs on for the image path (about five times
// in the v1 measurements).
const a53Factor = 5

// pageStats collects the per-page timings (milliseconds) of one document kind.
type pageStats struct {
	fetch, decBW, fitBW, decCol, fitCol, total []float64
	bytes, srcMP                               []float64
}

func (s *pageStats) line(name string, v []float64) string {
	return fmt.Sprintf("   %-12s p50 %5.0f  p95 %5.0f  max %5.0f ms", name, percentile(v, 50), percentile(v, 95), percentile(v, 100))
}

// runSelftest walks root -> libraries -> series -> chapter and times up to
// `pages` evenly spaced pages of one comic and one PDF (when the server has
// them). It prints no URLs and no key. Exit code 0 only if a page was fetched,
// decoded and fitted (or the catalogue is empty, reported as such); 3 = not
// signed in yet.
func runSelftest(ctx context.Context, cfg store.Config, cerr error, warn string, pages int) int {
	say := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }
	if pages <= 0 {
		pages = 8
	}
	if cerr != nil {
		say("FAIL config: %v", cerr)
		return 2
	}
	if warn != "" {
		say("WARN %s", warn)
	}
	if cfg.APIKey == "" || cfg.Server == "" {
		say("not signed in: sign in on the device first, then run the self-test again")
		return 3
	}
	base, err := url.Parse(cfg.Server)
	if err != nil || base.Host == "" {
		say("FAIL config: the stored server address is not usable")
		return 2
	}
	cl := opds.NewClient(base, cfg.APIKey)
	say("server %s", base.Host)
	get := func(u string) (*opds.Feed, time.Duration, error) {
		t0 := time.Now()
		cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		f, err := cl.GetFeed(cctx, u)
		return f, time.Since(t0), err
	}
	root, dt, err := get(opds.RootURL(base, cfg.APIKey))
	if err != nil {
		say("FAIL root feed: %s (%s)", errShort(err), cl.Redact(err.Error()))
		return 1
	}
	say("OK root feed %q: %d entries, search=%v, %d ms", root.Title, len(root.Entries), root.SearchDescription() != "", dt.Milliseconds())
	var libsURL string
	for _, e := range root.Entries {
		if e.ID == "allLibraries" || strings.HasSuffix(e.NavHref(), "/libraries") {
			libsURL = e.NavHref()
		}
	}
	if libsURL == "" {
		say("WARN no libraries entry in root")
		return 0
	}
	libs, dt, err := get(libsURL)
	if err != nil {
		say("FAIL libraries: %s", cl.Redact(err.Error()))
		return 1
	}
	say("OK libraries: %d, %d ms", len(libs.Entries), dt.Milliseconds())
	if len(libs.Entries) == 0 {
		say("catalogue is empty: nothing to stream")
		return 0
	}
	type pick struct {
		st    *opds.Stream
		title string
	}
	picks := map[opds.Doc]*pick{}
	hiddenSeries, hiddenChapters := 0, 0
	for _, l := range libs.Entries {
		if len(picks) == 2 || l.NavHref() == "" {
			continue
		}
		f, dt, err := get(l.NavHref())
		if err != nil {
			say("   library %q: %s", l.Title, errShort(err))
			continue
		}
		say("OK library %q: %d series on first page, next=%v, total=%d, %d ms", l.Title, len(f.Entries), f.Next() != "", f.Total, dt.Milliseconds())
		for i, s := range f.Entries {
			if len(picks) == 2 || i >= 8 || s.NavHref() == "" {
				continue
			}
			if !s.ImageFormat() {
				hiddenSeries++
				continue
			}
			sf, dt, err := get(s.NavHref())
			if err != nil {
				continue
			}
			streamable := 0
			for _, e := range sf.Entries {
				st := e.Stream()
				if st == nil {
					hiddenChapters++
					continue
				}
				streamable++
				k := e.DocKind()
				if picks[k] == nil {
					picks[k] = &pick{st: st, title: e.Title}
				}
			}
			say("   series %q: %d entries, %d streamable, %d ms", s.Title, len(sf.Entries), streamable, dt.Milliseconds())
		}
	}
	say("not listed by the app: %d series, %d chapters without a page stream", hiddenSeries, hiddenChapters)
	if len(picks) == 0 {
		say("WARN no streamable chapter found")
		return 0
	}
	ok := 0
	var worstP95 float64
	for _, k := range []opds.Doc{opds.DocComic, opds.DocPDF} {
		p := picks[k]
		if p == nil {
			continue
		}
		name := "comic"
		kern := imgproc.CatmullRom
		if k == opds.DocPDF {
			name, kern = "pdf", imgproc.Lanczos3
		}
		st := p.st
		say("%s chapter: %d pages, lastRead=%d, measuring %d", name, st.Count, st.LastRead, len(spread(sampleSpan(st.Count), pages)))
		var s pageStats
		var fitW, fitH int
		for _, pg := range spread(sampleSpan(st.Count), pages) {
			cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			t0 := time.Now()
			data, err := cl.GetImage(cctx, st.PageURL(pg, 1620, 2160))
			cancel()
			if err != nil {
				say("FAIL page %d: %s", pg, cl.Redact(err.Error()))
				continue
			}
			tFetch := time.Since(t0)
			// black and white: luminance only
			t1 := time.Now()
			pl, err := imgproc.DecodePlanes(data, false)
			if err != nil {
				say("FAIL page %d decode: %v", pg, err)
				continue
			}
			tDec := time.Since(t1)
			b := pl.Bounds()
			dw, dh := imgproc.FitDims(b.Dx(), b.Dy(), 1620, 2160, imgproc.FitContain)
			t2 := time.Now()
			out := imgproc.FitPlanes(pl, dw, dh, kern, 1)
			tFit := time.Since(t2)
			fitW, fitH = out.W, out.H
			// colour
			t3 := time.Now()
			pc, err := imgproc.DecodePlanes(data, true)
			tDecC := time.Since(t3)
			var tFitC time.Duration
			colourNote := "gray"
			if err == nil {
				t4 := time.Now()
				oc := imgproc.FitPlanes(pc, dw, dh, kern, 1)
				tFitC = time.Since(t4)
				if !oc.IsGray() {
					colourNote = "colour"
				}
			}
			ok++
			ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
			s.fetch = append(s.fetch, ms(tFetch))
			s.decBW = append(s.decBW, ms(tDec))
			s.fitBW = append(s.fitBW, ms(tFit))
			s.decCol = append(s.decCol, ms(tDecC))
			s.fitCol = append(s.fitCol, ms(tFitC))
			s.total = append(s.total, ms(tDec+tFit))
			s.bytes = append(s.bytes, float64(len(data)))
			s.srcMP = append(s.srcMP, float64(b.Dx()*b.Dy())/1e6)
			say("OK %s page %d: %d bytes (%s), %dx%d -> %dx%d, fetch %d ms | b/w decode %d ms fit %d ms | colour decode %d ms fit %d ms (%dKB)",
				name, pg, len(data), colourNote, b.Dx(), b.Dy(), out.W, out.H,
				tFetch.Milliseconds(), tDec.Milliseconds(), tFit.Milliseconds(), tDecC.Milliseconds(), tFitC.Milliseconds(), out.Bytes()/1024)
		}
		if len(s.fetch) > 0 {
			say("%s pages measured: %d, source %.1f-%.1f MP, %.0f-%.0f KB, fitted %dx%d", name, len(s.fetch),
				percentile(s.srcMP, 0), percentile(s.srcMP, 100), percentile(s.bytes, 0)/1024, percentile(s.bytes, 100)/1024, fitW, fitH)
			say("%s", s.line("fetch", s.fetch))
			say("%s", s.line("b/w decode", s.decBW))
			say("%s", s.line("b/w fit", s.fitBW))
			say("%s", s.line("colour decode", s.decCol))
			say("%s", s.line("colour fit", s.fitCol))
			p95 := percentile(s.total, 95)
			colP95 := percentile(s.decCol, 95) + percentile(s.fitCol, 95)
			if colP95 > p95 {
				p95 = colP95
			}
			if p95 > worstP95 {
				worstP95 = p95
			}
			say("   decode+fit p95 %.0f ms here, projected %.0f ms on the tablet (x%d)", p95, p95*a53Factor, a53Factor)
		}
		if len(s.fetch) > 0 {
			// Kavita records progress on every page request: put the chapter back
			// where the server said it was before the test touched it (a chapter
			// that had none goes back to its first page; the last page is never
			// requested, so it is not marked as read).
			back := st.LastRead
			if back < 0 || back >= st.Count {
				back = 0
			}
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := cl.Touch(cctx, st.PageURL(back, 1620, 2160))
			cancel()
			say("reading progress put back to page %d: %v", back, err == nil)
		}
	}
	if worstP95*a53Factor > 500 {
		say("NOTE projected decode+fit p95 above 500 ms on the tablet: see NOTES.md (DCT-scaled decode design)")
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	say("memory: heap %d KB, sys %d KB, rss %d KB, peak rss %d KB", ms.HeapAlloc/1024, ms.Sys/1024, rssKB(), statusKB("VmHWM"))
	if ok == 0 {
		return 1
	}
	return 0
}

func errShort(err error) string {
	if e, ok := err.(*opds.Error); ok {
		return e.Short()
	}
	return "error"
}
