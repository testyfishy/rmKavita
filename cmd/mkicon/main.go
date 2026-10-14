// Command mkicon draws the app's AppLoad icon (600x600, like KOReader's).
//
//	mkicon -out icon.png
package main

import (
	"flag"
	"image"
	"image/png"
	"log"
	"os"

	"rmkavita/internal/gfx"
)

func main() {
	out := flag.String("out", "icon.png", "output file")
	flag.Parse()
	const n = 600
	fonts, err := gfx.NewFonts()
	if err != nil {
		log.Fatal(err)
	}
	c := gfx.NewCanvas(n, n)
	// a manga page: thick frame with two panels and a big K
	page := image.Rect(60, 40, n-60, n-40)
	c.Frame(page, 22, 0)
	mid := page.Min.X + page.Dx()*58/100
	c.Fill(image.Rect(mid-9, page.Min.Y+22, mid+9, page.Max.Y-22), 0)
	// right panel: halftone-ish dots
	for y := page.Min.Y + 60; y < page.Max.Y-40; y += 36 {
		for x := mid + 40; x < page.Max.X-40; x += 36 {
			c.Disc(x, y, 9, 0)
		}
	}
	// left panel: "rm" over a big K
	cx := page.Min.X + (mid-page.Min.X)/2
	sm := gfx.Style{Size: 125, Bold: true, Gray: 0}
	w := fonts.Width("rm", sm)
	fonts.Draw(c, cx-w/2, 235, "rm", sm)
	st := gfx.Style{Size: 270, Bold: true, Gray: 0}
	w = fonts.Width("K", st)
	fonts.Draw(c, cx-w/2, 485, "K", st)
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, c.Gray); err != nil {
		log.Fatal(err)
	}
}
