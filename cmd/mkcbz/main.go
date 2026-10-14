// Command mkcbz writes a small tree of synthetic manga archives for a throwaway
// Kavita instance: libraries/<Library>/<Series>/<Series> v01.cbz ...
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"rmkavita/internal/gfx"
	"rmkavita/internal/synth"
)

func writeCBZ(path string, pages [][]byte, ext string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for i, p := range pages {
		w, err := zw.Create(fmt.Sprintf("%03d.%s", i+1, ext))
		if err != nil {
			return err
		}
		if _, err := w.Write(p); err != nil {
			return err
		}
	}
	return zw.Close()
}

func main() {
	out := flag.String("out", "libraries", "output root")
	webp := flag.String("webp", "", "optional .webp file added as the last page of the WebP series")
	ab := flag.String("ab", "", "write only the A/B test chapter (real page sizes) to this .cbz file")
	abW := flag.Int("ab-w", synth.DefaultAB.W, "A/B chapter: page width")
	abH := flag.Int("ab-h", synth.DefaultAB.H, "A/B chapter: page height")
	abQ := flag.Int("ab-quality", synth.DefaultAB.Quality, "A/B chapter: JPEG quality")
	speed := flag.String("speed", "", "write only the speed A/B pair (6 pages each, all grey and all colour) into this folder")
	flag.Parse()
	fonts, err := gfx.NewFonts()
	if err != nil {
		panic(err)
	}
	if *speed != "" {
		o := synth.DefaultAB
		o.W, o.H, o.Quality = *abW, *abH, *abQ
		if err := os.MkdirAll(*speed, 0o755); err != nil {
			panic(err)
		}
		for _, v := range []struct {
			name   string
			colour bool
		}{{"rmKavita Speed Grey v01.cbz", false}, {"rmKavita Speed Colour v01.cbz", true}} {
			pages := synth.SpeedChapter(fonts, o, v.colour)
			var raw [][]byte
			total := 0
			for _, p := range pages {
				raw = append(raw, p.JPEG)
				total += len(p.JPEG)
			}
			path := filepath.Join(*speed, v.name)
			if err := writeCBZ(path, raw, "jpg"); err != nil {
				panic(err)
			}
			fmt.Printf("wrote %s: %d pages, %.1f MB\n", path, len(pages), float64(total)/1e6)
		}
		return
	}
	if *ab != "" {
		o := synth.DefaultAB
		o.W, o.H, o.Quality = *abW, *abH, *abQ
		pages := synth.ABChapter(fonts, o)
		var raw [][]byte
		total := 0
		for _, p := range pages {
			raw = append(raw, p.JPEG)
			total += len(p.JPEG)
		}
		if err := writeCBZ(*ab, raw, "jpg"); err != nil {
			panic(err)
		}
		fmt.Printf("wrote %s: %d pages, %.1f MB\n", *ab, len(pages), float64(total)/1e6)
		return
	}
	type series struct {
		lib, name string
		vols      int
		pages     int
		w, h      int
		kind      string
	}
	list := []series{
		{"Manga A", "Alpha Blade", 3, 12, 1200, 1800, "jpeg"},
		{"Manga A", "Beta Garden", 4, 8, 1100, 1650, "jpegcolor"},
		{"Manga B", "Gamma Png", 1, 6, 1000, 1500, "png"},
		{"Manga B", "Delta Mixed", 1, 7, 1400, 2100, "mixed"},
	}
	for _, s := range list {
		for v := 1; v <= s.vols; v++ {
			var pages [][]byte
			ext := "jpg"
			for p := 0; p < s.pages; p++ {
				w, h := s.w, s.h
				if s.kind == "mixed" && p == 3 {
					w, h = s.h, s.w // a landscape spread
				}
				g := synth.Page(int64(v*1000+p)+int64(len(s.name)), w, h, synth.Label(fmt.Sprintf("%s v%d", s.name, v), p), fonts)
				switch s.kind {
				case "png":
					ext = "png"
					pages = append(pages, synth.PNG(g))
				case "jpegcolor":
					pages = append(pages, synth.JPEGColor(g, 82))
				default:
					pages = append(pages, synth.JPEG(g, 85))
				}
			}
			if s.kind == "mixed" {
				// swap page 5 for a PNG, and optionally append a WebP: the extension decides Kavita's mime type
				pages[4] = synth.PNG(synth.Page(4242, s.w, s.h, "PNG page in a JPEG archive", fonts))
			}
			path := filepath.Join(*out, s.lib, s.name, fmt.Sprintf("%s v%02d.cbz", s.name, v))
			if err := writeCBZ(path, pages, ext); err != nil {
				panic(err)
			}
			fmt.Println("wrote", path, len(pages), "pages")
		}
	}
	if *webp != "" {
		b, err := os.ReadFile(*webp)
		if err != nil {
			panic(err)
		}
		var pages [][]byte
		for p := 0; p < 3; p++ {
			pages = append(pages, synth.JPEG(synth.Page(int64(900+p), 1000, 1500, synth.Label("Epsilon Webp", p), fonts), 85))
		}
		path := filepath.Join(*out, "Manga B", "Epsilon Webp", "Epsilon Webp v01.cbz")
		// WebP pages need a .webp extension inside the archive for the right mime type
		f, _ := os.Create(path + ".tmp")
		_ = f.Close()
		os.Remove(path + ".tmp")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			panic(err)
		}
		zf, err := os.Create(path)
		if err != nil {
			panic(err)
		}
		zw := zip.NewWriter(zf)
		for i, p := range pages {
			w, _ := zw.Create(fmt.Sprintf("%03d.jpg", i+1))
			w.Write(p)
		}
		w, _ := zw.Create(fmt.Sprintf("%03d.webp", len(pages)+1))
		w.Write(b)
		zw.Close()
		zf.Close()
		fmt.Println("wrote", path)
	}
}
