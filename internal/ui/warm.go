package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rmkavita/internal/imgproc"
	"rmkavita/internal/opds"
)

// Next-chapter warm-up: three pages before the end of a chapter the first page
// of the next one (and, when the reader was opened from the home "Continue"
// row, the series feed that names it) is fetched, so crossing the boundary
// shows a page at once instead of after a fetch + decode. It happens once per
// chapter, is cancelled when the reader leaves, and is skipped when the next
// chapter would resume somewhere other than page one.

// warmAhead is how many pages before the last one the warm-up starts.
const warmAhead = 3

type warmPage struct {
	chapter int
	spec    pageSpec // what the page was prepared for (gen is zero)
	img     *imgproc.Fitted
	state   int // 1 loading, 2 ready, 3 failed
	cancel  context.CancelFunc
}

type warmPageMsg struct {
	r       *reader
	w       *warmPage
	img     *imgproc.Fitted
	err     *opds.Error
	chapter int
}

type warmFeedMsg struct {
	r   *reader
	lv  *listView
	err *opds.Error
}

// dropWarm cancels a warm-up in flight and forgets its page.
func (r *reader) dropWarm() {
	if r.warm != nil && r.warm.cancel != nil {
		r.warm.cancel()
	}
	r.warm = nil
}

// maybeWarm starts the warm-up when the reader is within warmAhead pages of
// the end. It runs at most once per reader (r.warm is the marker).
func (a *App) maybeWarm(r *reader) {
	if !a.Cfg.Warmup || r != a.rd || r.warm != nil || r.unknown || r.count < warmAhead+1 || r.page < r.count-warmAhead {
		return
	}
	if a.lowMem() {
		return
	}
	r.warm = &warmPage{} // started: the marker that keeps it to once
	idx, found := chapterAfter(r.from, r.ids.Chapter, 0)
	if found {
		if idx > 0 {
			a.warmRow(r, r.from, idx)
		}
		return // the last chapter of its series: nothing to warm
	}
	if r.ids.Series == 0 {
		return
	}
	url := a.resolve(fmt.Sprintf("%s/series/%d", strings.TrimRight(a.rootPath, "/"), r.ids.Series), "")
	if url == "" {
		return
	}
	ctx, cancel := context.WithCancel(a.sctx)
	r.warm.cancel = cancel
	a.async(func() any {
		c2, c := context.WithTimeout(ctx, 30*time.Second)
		defer c()
		f, err := a.Cl.GetFeed(c2, url)
		if err != nil {
			return warmFeedMsg{r: r, err: errOf(err)}
		}
		rows, _ := a.rowsFromFeed(f, url)
		return warmFeedMsg{r: r, lv: &listView{title: f.Title, url: url, rows: rows}}
	})
}

func (a *App) onWarmFeed(m warmFeedMsg) {
	r := m.r
	if a.rd != r || r.warm == nil {
		return
	}
	if m.err != nil || m.lv == nil {
		return // the boundary falls back to its own fetch
	}
	r.seriesLV = m.lv
	if idx, found := chapterAfter(m.lv, r.ids.Chapter, 0); found && idx > 0 {
		a.warmRow(r, m.lv, idx)
	}
}

// warmRow fetches page 0 of row idx of lv (only when it would be opened there).
func (a *App) warmRow(r *reader, lv *listView, idx int) {
	row := lv.rows[idx]
	if row.stream == nil || row.ids.Chapter == 0 {
		return
	}
	st := *row.stream
	if st.Count > 0 && a.startPage(row.ids, &st) != 0 {
		return // it resumes elsewhere: a page 0 would be wasted
	}
	sp := a.specFor(row.doc)
	if r.warm == nil {
		return
	}
	ctx, cancel := context.WithCancel(a.sctx)
	if r.warm.cancel != nil {
		r.warm.cancel()
	}
	w := r.warm
	w.chapter, w.spec, w.state, w.cancel = row.ids.Chapter, sp, 1, cancel
	url := st.PageURL(0, a.W, a.H)
	chapter := row.ids.Chapter
	a.async(func() any {
		c2, c := context.WithTimeout(ctx, 60*time.Second)
		defer c()
		data, err := a.Cl.GetImage(c2, url)
		if err != nil {
			return warmPageMsg{r: r, w: w, chapter: chapter, err: errOf(err)}
		}
		img, err := a.prepare(data, sp)
		if err != nil {
			return warmPageMsg{r: r, w: w, chapter: chapter, err: &opds.Error{Kind: opds.KindData, Msg: err.Error()}}
		}
		return warmPageMsg{r: r, w: w, chapter: chapter, img: img}
	})
}

func (a *App) onWarmPage(m warmPageMsg) {
	r := m.r
	a.noteDecoded()
	if r.warm != m.w || m.w == nil {
		return // dropped or replaced (settings changed, reader left)
	}
	if m.err != nil {
		m.w.state = 3
		if m.err.Kind == opds.KindAuth {
			a.confirmAuth()
		}
		return
	}
	m.w.img, m.w.state = m.img, 2
}

// installWarm gives a freshly opened reader the page the previous reader
// fetched ahead, when it is the same chapter and was prepared for the same
// settings.
func (a *App) installWarm(r *reader, row row) {
	w := a.handOver
	a.handOver = nil
	if w == nil || w.state != 2 || w.img == nil || w.chapter != row.ids.Chapter || r.page != 0 || r.unknown {
		return
	}
	if w.spec != a.specFor(row.doc) {
		return
	}
	a.slotSeq++
	r.slots[0] = &slot{id: a.slotSeq, state: 2, img: w.img}
	a.logf("warm page used for chapter %d", row.ids.Chapter)
}
