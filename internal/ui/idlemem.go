package ui

import (
	"bytes"
	"image"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"
)

// A 10-megapixel page decodes through tens of megabytes of temporary buffers.
// Go keeps freed memory for a while, so the resident size stays high long after
// the page is on screen. Once the app has been quiet for idleFreeAfter since
// the last decode, the freed memory goes back to the system (one collection,
// off the UI goroutine).
const idleFreeAfter = 1500 * time.Millisecond

// noteDecoded schedules the idle memory release.
func (a *App) noteDecoded() {
	if a.memT == nil {
		return
	}
	if !a.memT.Stop() {
		select {
		case <-a.memT.C:
		default:
		}
	}
	a.memT.Reset(idleFreeAfter)
}

// onIdleMemory runs when idleFreeAfter has passed since the last decode.
func (a *App) onIdleMemory() {
	if a.pending > 0 {
		a.memT.Reset(idleFreeAfter) // a page is still being fetched or decoded
		return
	}
	go debug.FreeOSMemory()
}

// bigPagePixels is the size from which a page's decode takes both decode
// tokens: a 10-megapixel JPEG decodes through tens of megabytes of buffers, and
// two of those at once is the spike the memory budget cannot afford.
const bigPagePixels = 4_000_000

// acquireDecode takes the decode token(s) a page of this data needs and returns
// the function that gives them back. Small pages (covers, PDF pages) can decode
// two at a time; a big comic page decodes alone.
func (a *App) acquireDecode(data []byte) func() {
	cost := 1
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width*cfg.Height > bigPagePixels {
		cost = cap(a.decodeSem) // big (or unreadable: be careful) pages decode alone
	}
	a.decodeMu.Lock() // the tokens are taken together, so two big pages cannot each hold one
	for i := 0; i < cost; i++ {
		a.decodeSem <- struct{}{}
	}
	a.decodeMu.Unlock()
	return func() {
		for i := 0; i < cost; i++ {
			<-a.decodeSem
		}
	}
}

// memLine is one log line about memory: the Go heap and what the process got
// from the system, how many goroutines are alive, what the page cache holds
// (bytes, pages ahead of the reader and behind it) and what the system has
// left. It is written every 25 screen updates, so growth shows up in the log
// without anyone looking for it.
func (a *App) memLine() string {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	cache, ahead, behind := 0, 0, 0
	if r := a.rd; r != nil {
		cache = r.cacheBytes()
		for n, s := range r.slots {
			if s.state != 2 {
				continue
			}
			switch {
			case n > r.page:
				ahead++
			case n < r.page:
				behind++
			}
		}
	}
	avail := -1
	if a.memAvail != nil {
		avail = a.memAvail()
	}
	return "mem heap=" + mb(ms.HeapInuse) + " sys=" + mb(ms.Sys) + " goroutines=" + itoa(runtime.NumGoroutine()) +
		" fds=" + itoa(openFDs()) + " thumbs=" + itoa(len(a.thumbs)) + " cache=" + mb(uint64(cache)) + " ahead=" + itoa(ahead) + " behind=" + itoa(behind) + " avail=" + itoa(avail) + "MB"
}

func mb(b uint64) string { return itoa(int((b+(1<<19))>>20)) + "MB" }

func itoa(n int) string {
	if n < 0 {
		return "unknown"
	}
	return fmtInt(n)
}

func fmtInt(n int) string { return strconv.Itoa(n) }
