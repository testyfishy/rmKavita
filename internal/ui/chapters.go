package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rmkavita/internal/opds"
)

// prevMsg is the answer of the series feed fetched to find the previous chapter.
type prevMsg struct {
	r   *reader
	lv  *listView
	err *opds.Error
}

// chapterBefore finds the chapter that precedes `chapter` in a chapter list
// (the mirror image of chapterAfter). found reports whether the chapter is in
// the list; ok that there is a streamable chapter before it, at idx.
func chapterBefore(lv *listView, chapter int) (idx int, found, ok bool) {
	if lv == nil {
		return 0, false, false
	}
	base := -1
	for i, x := range lv.rows {
		if x.kind == rowChapter && !x.shortcut && x.ids.Chapter == chapter {
			base = i
			break
		}
	}
	if base < 0 {
		return 0, false, false
	}
	for i := base - 1; i >= 0; i-- {
		x := lv.rows[i]
		if x.kind == rowChapter && !x.shortcut && x.stream != nil && x.ids.Chapter != chapter {
			return i, true, true
		}
	}
	return 0, true, false
}

// prevChapter opens the chapter before the one being read (a hot-corner
// function). It resumes that chapter where it was left; the current chapter
// keeps its position. At the first chapter of a series it does nothing.
func (a *App) prevChapter() {
	r := a.rd
	if r == nil || r.nextLoading {
		return
	}
	idx, found, ok := chapterBefore(r.from, r.ids.Chapter)
	if !found && r.seriesLV != nil {
		idx, found, ok = chapterBefore(r.seriesLV, r.ids.Chapter)
		if found {
			r.from = r.seriesLV
		}
	}
	switch {
	case found && ok:
		a.openPrev(r, r.from, idx)
		return
	case found:
		a.logf("previous chapter: this is the first chapter")
		return
	case r.ids.Series == 0 || r.unknown:
		return
	}
	// opened from the home Continue row: the series feed names the chapters
	r.nextLoading = true
	a.readerRedraw("loading")
	url := a.resolve(fmt.Sprintf("%s/series/%d", strings.TrimRight(a.rootPath, "/"), r.ids.Series), "")
	sctx := a.sctx
	a.async(func() any {
		ctx, cancel := context.WithTimeout(sctx, 30*time.Second)
		defer cancel()
		f, err := a.Cl.GetFeed(ctx, url)
		if err != nil {
			return prevMsg{r: r, err: errOf(err)}
		}
		rows, _ := a.rowsFromFeed(f, url)
		return prevMsg{r: r, lv: &listView{title: f.Title, url: url, rows: rows}}
	})
}

func (a *App) onPrev(m prevMsg) {
	r := m.r
	if a.rd != r {
		return
	}
	r.nextLoading = false
	if m.err != nil {
		a.logf("previous chapter: %s", m.err.Error())
		a.readerRedraw("ui")
		return
	}
	idx, found, ok := chapterBefore(m.lv, r.ids.Chapter)
	if !found || !ok {
		a.readerRedraw("ui")
		return
	}
	r.seriesLV = m.lv
	a.openPrev(r, m.lv, idx)
}

// openPrev leaves the chapter (position kept) and opens row i of lv.
func (a *App) openPrev(r *reader, lv *listView, i int) {
	r.savePos(a)
	for _, s := range r.slots {
		if s.cancel != nil {
			s.cancel()
		}
	}
	r.dropWarm()
	a.syncSoon(r)
	a.rd = nil
	a.ovT.Stop()
	a.openReader(lv, i)
}
