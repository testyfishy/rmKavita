package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"strings"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/gfx"
	"rmkavita/internal/kobo"
	"rmkavita/internal/store"
)

// koboDir is where everything of the app lives on a Kobo: the user partition,
// next to NickelMenu's own folder (it survives firmware updates and is
// reachable over USB for the log).
const koboDir = "/mnt/onboard/.adds/rmkavita"

// onKobo reports whether this looks like a Kobo running the app outside
// AppLoad: a framebuffer, the Kobo user partition, no qtfb key.
func onKobo() bool {
	if os.Getenv("QTFB_KEY") != "" {
		return false
	}
	if _, err := os.Stat("/dev/fb0"); err != nil {
		return false
	}
	_, err := os.Stat("/mnt/onboard/.kobo")
	return err == nil
}

// koboDPI is the panel density of the Kobo models with an i.MX EPDC panel,
// by Nickel's PRODUCT codename (the launcher inherits Nickel's environment).
func koboDPI() (int, string) {
	product := strings.TrimSpace(os.Getenv("PRODUCT"))
	switch product {
	case "io", "goldfinch", "nova", "storm", "frost", "snow", "spaBW", "spaTolino", "monza", "monzaTolino", "spaColour", "spaColourTolino":
		return 300, product
	case "daylight", "kraken", "phoenix", "star", "alyssum", "dahlia", "pika", "dragon":
		return 265, product
	}
	return 300, product
}

// koboLayoutScale shrinks the layout on a Kobo: the design is for an 11.8-inch
// tablet held at arm's length; on a 6 to 8-inch reader at the same physical
// size the bars and rows eat the screen. 0.73 keeps every target near 10 mm.
const koboLayoutScale = 0.73

// layoutDPI is the density the UI lays itself out for.
func layoutDPI(panelDPI int) int { return int(float64(panelDPI)*koboLayoutScale + 0.5) }

type koboPanel interface {
	display.Display
	SetTouchMap(swap, mx, my bool)
	SetRaw(on bool)
	WaitIdle(time.Duration)
}

// openKobo takes over the screen and the input, and calibrates the touch
// screen on the first launch (the result is kept in the config file as
// touch_map; delete that line to calibrate again).
func openKobo(cfg *store.Config, confPath string, fonts *gfx.Fonts, logger *store.Logger) (display.Display, int, error) {
	dpi, product := koboDPI()
	p, err := kobo.Open(kobo.Options{Rotate: cfg.FBRotate, TouchMap: cfg.TouchMap, Logf: logger.Logf})
	if err != nil {
		return nil, 0, err
	}
	logger.Logf("kobo: product=%q dpi=%d layout-dpi=%d format=%s", product, dpi, layoutDPI(dpi), p.PixelFormat())
	dpi = layoutDPI(dpi)
	splash(p, fonts, dpi, "Opening", true)
	if cfg.TouchMap == "" {
		m, err := calibrate(p, fonts, dpi)
		if err != nil {
			// keep the app usable: the guess from the axis ranges, not saved, so the
			// next launch calibrates again
			rx, ry := p.RawRanges()
			w, h := p.Size()
			g := kobo.GuessMap(rx, ry, w, h)
			p.SetTouchMap(kobo.ParseMap(g))
			logger.Logf("kobo: calibration: %v; using the guess %s for now", err, g)
		} else {
			cfg.TouchMap = m
			logger.Logf("kobo: touch_map = %s", m)
			if err := store.UpdateConf(confPath, map[string]string{"touch_map": m}, nil); err != nil {
				logger.Logf("kobo: could not save touch_map: %v", err)
			}
		}
	}
	return p, dpi, nil
}

// splash draws the app's name with a short progress animation: four steps of
// a bar, each a small fast (DU) update, so it is plain that something is
// happening while Nickel goes away (opening) or comes back (closing). The
// first frame is a flashing refresh, which also wipes Nickel's last screen.
func splash(p display.Display, fonts *gfx.Fonts, dpi int, verb string, opening bool) {
	w, h := p.Size()
	k := float64(dpi) / 229
	u := func(v int) int { return int(float64(v)*k + 0.5) }
	cv := gfx.NewCanvas(w, h)
	cv.Fill(cv.Bounds(), 255)
	title := gfx.Style{Size: u(110), Bold: true, Gray: 0}
	sub := gfx.Style{Size: u(44), Gray: 60}
	name := "rmKavita"
	ty := h * 42 / 100
	fonts.Draw(cv, (w-fonts.Width(name, title))/2, ty, name, title)
	line := verb + "..."
	if !opening {
		line = "Returning to your Kobo..."
	}
	fonts.Draw(cv, (w-fonts.Width(line, sub))/2, ty+u(110), line, sub)
	bw, bh := w*55/100, u(26)
	bx, by := (w-bw)/2, ty+u(190)
	bar := image.Rect(bx, by, bx+bw, by+bh)
	cv.Frame(bar, u(4), 0)
	_ = p.Show(display.Frame{Gray: cv.Gray, Rect: cv.Bounds(), Mode: display.ModeContent, Full: true, Tag: "splash"})
	inner := bar.Inset(u(8))
	steps := 4
	for i := 1; i <= steps; i++ {
		time.Sleep(140 * time.Millisecond)
		x1 := inner.Min.X + inner.Dx()*i/steps
		cv.Fill(image.Rect(inner.Min.X, inner.Min.Y, x1, inner.Max.Y), 0)
		_ = p.Show(display.Frame{Gray: cv.Gray, Rect: bar, Mode: display.ModeFast, Tag: "splash-step"})
	}
}

// calibrate shows two targets, one after the other, and works out from where
// the taps landed how the touch panel is turned against the screen.
func calibrate(p koboPanel, fonts *gfx.Fonts, dpi int) (string, error) {
	w, h := p.Size()
	k := float64(dpi) / 229
	u := func(v int) int { return int(float64(v)*k + 0.5) }
	cv := gfx.NewCanvas(w, h)
	targets := [][2]float64{kobo.CalA, kobo.CalB}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		p.SetRaw(true)
		var got [][2]float64
		for i, t := range targets {
			cv.Fill(cv.Bounds(), 255)
			title := gfx.Style{Size: u(54), Bold: true, Gray: 0}
			body := gfx.Style{Size: u(40), Gray: 40}
			msg := "rmKavita: touch setup"
			fonts.Draw(cv, (w-fonts.Width(msg, title))/2, h*68/100, msg, title)
			line := fmt.Sprintf("Tap the centre of the cross (%d of 2)", i+1)
			if attempt > 0 {
				line = "That did not line up. " + line
			}
			fonts.Draw(cv, (w-fonts.Width(line, body))/2, h*68/100+u(80), line, body)
			cx, cy := int(t[0]*float64(w)), int(t[1]*float64(h))
			arm, th := u(70), u(6)
			cv.Fill(image.Rect(cx-arm, cy-th/2, cx+arm, cy+th/2), 0)
			cv.Fill(image.Rect(cx-th/2, cy-arm, cx+th/2, cy+arm), 0)
			cv.Frame(image.Rect(cx-u(24), cy-u(24), cx+u(24), cy+u(24)), u(4), 0)
			if err := p.Show(display.Frame{Gray: cv.Gray, Rect: cv.Bounds(), Mode: display.ModeContent, Full: i == 0 && attempt == 0, Tag: "calibrate"}); err != nil {
				return "", err
			}
			x, y, err := waitTap(ctx, p.Events())
			if err != nil {
				return "", err
			}
			got = append(got, [2]float64{float64(x) / float64(max(1, w-1)), float64(y) / float64(max(1, h-1))})
		}
		p.SetRaw(false)
		swap, mx, my, ok := kobo.Calibrate(got[0], got[1])
		if ok {
			p.SetTouchMap(swap, mx, my)
			return kobo.FormatMap(swap, mx, my), nil
		}
	}
	p.SetRaw(false)
	return "", fmt.Errorf("the taps did not match the targets")
}

// waitTap waits for a press and returns where its finger lifted.
func waitTap(ctx context.Context, ev <-chan display.Event) (int, int, error) {
	down := false
	for {
		select {
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		case e, ok := <-ev:
			if !ok {
				return 0, 0, fmt.Errorf("input closed")
			}
			switch e.Kind {
			case display.Down:
				down = true
			case display.Up:
				if down {
					return e.X, e.Y, nil
				}
			}
		}
	}
}
