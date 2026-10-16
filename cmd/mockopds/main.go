// Command mockopds serves the mock Kavita catalogue (internal/mockkavita) on a
// local port, for browsing it with the app's PNG backend or on the tablet.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"rmkavita/internal/gfx"
	"rmkavita/internal/mockkavita"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5090", "listen address")
	nA := flag.Int("series", 45, "series in the first library")
	pages := flag.Int("pages", 16, "pages per chapter in the first library")
	w := flag.Int("w", 1200, "page width")
	h := flag.Int("h", 1800, "page height")
	delay := flag.Duration("delay", 0, "artificial latency per request")
	big := flag.Int("big", 0, "add a library \"Big Library\" with this many generated series (900+ for the long-list tests)")
	bigArt := flag.Bool("big-article-sort", false, "the big library is sorted as if a leading The / A / An was not there")
	bigRaw := flag.Bool("big-unsorted", false, "the big library is left in generation order (not alphabetical)")
	webp := flag.String("webp", "", ".webp file used as the last page of the WebP series")
	confOut := flag.String("write-conf", "", "write a signed-in client config (server, user, key) to this file")
	confOut2 := flag.String("write-conf-signedout", "", "write a client config with the server only to this file")
	flag.Parse()
	fonts, err := gfx.NewFonts()
	if err != nil {
		log.Fatal(err)
	}
	s := mockkavita.Build(*nA, *pages, *w, *h)
	s.SetFonts(fonts)
	s.SetDelay(*delay)
	if *big > 0 {
		s.AddBigLibrary(mockkavita.BigOptions{Series: *big, ArticleSort: *bigArt, Unsorted: *bigRaw})
	}
	if *webp != "" {
		if b, err := os.ReadFile(*webp); err == nil {
			s.SetWebP(b)
		}
	}
	if *confOut != "" {
		_ = os.WriteFile(*confOut, []byte("server = http://"+*listen+"\nusername = reader\napi_key = "+s.Key()+"\n"), 0o600)
	}
	if *confOut2 != "" {
		_ = os.WriteFile(*confOut2, []byte("server = http://"+*listen+"\nusername = reader\n"), 0o600)
	}
	srv := &http.Server{Addr: *listen, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("mockopds on http://%s (sign-in: reader / correct horse)", *listen)
	log.Fatal(srv.ListenAndServe())
}
