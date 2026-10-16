// Command rmkavita is a fullscreen AppLoad app for the reMarkable Paper Pro: a
// lightweight client that streams manga and comics from a Kavita server over
// OPDS-PSE, one page at a time, and stores nothing but small state files.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

	"rmkavita/internal/display"
	"rmkavita/internal/gfx"
	"rmkavita/internal/qtfb"
	"rmkavita/internal/store"
	"rmkavita/internal/ui"
)

var version = "0.3.1"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	var (
		confPath  = flag.String("conf", env("RMKAVITA_CONF", "/home/root/.config/rmkavita.conf"), "config file (mode 600): server, user name, API key")
		setPath   = flag.String("settings", env("RMKAVITA_SETTINGS", "/home/root/.config/rmkavita-settings.json"), "settings file (not secret)")
		statePath = flag.String("state", env("RMKAVITA_STATE", "/home/root/.config/rmkavita-state.json"), "reading positions file")
		logPath   = flag.String("log", env("RMKAVITA_LOG", "/tmp/rmkavita.log"), "log file (tmpfs); empty = stderr only")
		backend   = flag.String("backend", "qtfb", "qtfb | png")
		pngDir    = flag.String("png-dir", "frames", "png backend: output directory")
		script    = flag.String("script", "", "png backend: touch script file")
		size      = flag.String("size", "1620x2160", "png backend: canvas size WxH")
		dpi       = flag.Int("dpi", 229, "png backend: the device's pixel density")
		selftest  = flag.Bool("selftest", false, "fetch a catalogue, a series and evenly spaced pages, print timings (p50/p95), exit")
		selfPages = flag.Int("selftest-pages", 8, "selftest: pages to measure per document kind")
		migrate   = flag.Bool("migrate", false, "bring an older install's config and positions to this layout, then exit")
		oldConf   = flag.String("old-conf", "", "migrate: the previous config file")
		oldState  = flag.String("old-state", "", "migrate: the previous positions file")
		showVer   = flag.Bool("version", false, "print version and exit")
		logStderr = flag.Bool("stderr", false, "also log to stderr (default: only when stderr is a terminal)")
		discard   = flag.Bool("png-discard", false, "png backend: do not draw or write frames (memory measurements)")
		dbg       = flag.Bool("debug", env("RMKAVITA_DEBUG", "") != "", "debug-level log lines")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("rmkavita", version)
		return
	}
	if *migrate {
		os.Exit(runMigrate(*confPath, *statePath, *oldConf, *oldState))
	}

	// The page cache (fitted pages, a byte budget of its own: cache_mb) is the only
	// big thing; the soft limit only makes the collector work harder when the heap
	// nears it, it kills nothing. The out-of-memory protection is the guard in the
	// app (mem_guard_mb, mem_floor_mb), which looks at what the system has left.
	debug.SetGCPercent(40)
	debug.SetMemoryLimit(280 << 20)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	watchParent(stop)

	cfg, warn, cerr := store.LoadConfig(*confPath)
	logger := store.NewLogger(*logPath, nil)
	// Under AppLoad stderr is a pipe the launcher owns: log to the file only.
	logger.Stderr = *logStderr || store.StderrIsTerminal()

	if *selftest {
		os.Exit(runSelftest(ctx, cfg, cerr, warn, *selfPages))
	}

	fonts, err := gfx.NewFonts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fonts:", err)
		os.Exit(2)
	}
	// Open the display first, so even a bad config is explained on screen.
	disp, devDPI, err := openDisplay(*backend, *pngDir, *script, *size, *dpi, *discard, cfg.FBFormat)
	if err != nil {
		logger.Logf("display: %v", err)
		os.Exit(1)
	}
	defer disp.Close()

	if cerr != nil {
		logger.Logf("config: %v", cerr)
		ui.ShowMessage(ctx, disp, fonts, "Cannot read the configuration", "Check the permissions of the config file")
		return
	}
	if warn != "" {
		logger.Logf("warning: %s", warn)
	}
	for _, u := range cfg.Unknown {
		logger.Logf("config: %s", u)
	}
	settings, fromFile := store.LoadSettings(*setPath, cfg)
	if !fromFile {
		// First run of this version: keep what an older config said (direction,
		// refresh interval) once; from now on the settings file is the source of truth.
		if err := store.SaveSettings(*setPath, settings); err != nil {
			logger.Logf("settings: %v", err)
		}
	}
	logger.Logf("start rmkavita %s backend=%s dpi=%d signed-in=%v", version, *backend, devDPI, cfg.APIKey != "")
	fbNote := "n/a"
	if f, ok := disp.(display.Formatted); ok {
		fbNote = f.PixelFormat()
	}
	logger.Logf("framebuffer: %s (fb_format=%s) page_mode=%s ui_mode=%s clear_style=%s border=%s crop=%s kernel=%s",
		fbNote, cfg.FBFormat, cfg.PageMode, cfg.UIMode, cfg.ClearStyle, cfg.Border, cfg.Crop, cfg.Kernel)
	logger.Logf("cache: read_ahead=%d keep_behind=%d cache_mb=%d mem_guard_mb=%d mem_floor_mb=%d zone_middle_pct=%d hot=%s/%s/%s/%s",
		cfg.ReadAhead, cfg.KeepBehind, cfg.CacheMB, cfg.MemGuardMB, cfg.MemFloorMB, cfg.ZoneMiddle, cfg.HotTL, cfg.HotTR, cfg.HotBL, cfg.HotBR)

	pos := store.OpenPositions(*statePath)
	app := ui.New(ui.Options{Cfg: cfg, Set: settings, ConfPath: *confPath, SettingsPath: *setPath, DeviceDPI: devDPI, Debug: *dbg, Version: version},
		disp, pos, fonts, logger)
	reason := app.Run(ctx)
	logger.Logf("exit: %s", reason)
}

func runMigrate(conf, state, oldConf, oldState string) int {
	lines, err := store.Migrate(conf, state, oldConf, oldState)
	for _, l := range lines {
		fmt.Println(l)
	}
	if err != nil {
		fmt.Println("migrate: failed:", err)
		return 1
	}
	return 0
}

func openDisplay(backend, pngDir, script, size string, dpi int, discard bool, fbFormat string) (display.Display, int, error) {
	switch backend {
	case "png":
		w, h := 1620, 2160
		if parts := strings.SplitN(size, "x", 2); len(parts) == 2 {
			if a, e1 := strconv.Atoi(parts[0]); e1 == nil {
				w = a
			}
			if b, e2 := strconv.Atoi(parts[1]); e2 == nil {
				h = b
			}
		}
		var text string
		if script != "" {
			b, err := os.ReadFile(script)
			if err != nil {
				return nil, 0, err
			}
			text = string(b)
		}
		d, err := display.NewPNG(w, h, pngDir, text)
		if d != nil {
			d.Discard = discard
		}
		return d, dpi, err
	case "qtfb":
		return openQTFB(fbFormat)
	}
	return nil, 0, fmt.Errorf("unknown backend %q", backend)
}

func openQTFB(fbFormat string) (display.Display, int, error) {
	machine := env("RMKAVITA_MACHINE", "")
	if machine == "" {
		b, err := os.ReadFile("/sys/devices/soc0/machine")
		if err != nil {
			return nil, 0, fmt.Errorf("cannot identify the device: %w", err)
		}
		machine = string(b)
	}
	model, err := qtfb.ModelFromMachine(machine)
	if err != nil {
		return nil, 0, err
	}
	keyStr := env("QTFB_KEY", "")
	if keyStr == "" {
		return nil, 0, fmt.Errorf("QTFB_KEY is not set: start the app from AppLoad")
	}
	k, err := strconv.ParseInt(keyStr, 10, 32)
	if err != nil {
		return nil, 0, fmt.Errorf("QTFB_KEY is not a number")
	}
	p, err := qtfb.Dial(int32(k), model, qtfb.Options{
		Socket: env("RMKAVITA_QTFB_SOCK", ""),
		ShmDir: env("RMKAVITA_SHM_DIR", ""),
		Format: fbFormat,
	})
	if err != nil {
		return nil, 0, err
	}
	return p, model.DPI, nil
}
