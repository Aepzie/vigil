package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/CraguS/vigil/internal/compat"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui"
	"github.com/CraguS/vigil/internal/tui/ui"
)

// Frame size limits of `vigil render`.
const (
	minRenderWidth  = 40
	minRenderHeight = 10
	maxRenderSize   = 1000
)

var tabNames = map[string]ui.TabID{
	"overview": ui.TabOverview,
	"servers":  ui.TabServers,
	"detail":   ui.TabDetail,
	"alerts":   ui.TabAlerts,
	"settings": ui.TabSettings,
}

func cmdRender(args []string, std stdio) error {
	fs := newFlagSet("render", "render [--size WxH] [--tab NAME] [--compat MODE] [--seed N]", std)
	size := fs.String("size", "150x44", "frame size in cells, WIDTHxHEIGHT")
	tab := fs.String("tab", "overview", "tab to render: overview, servers, detail, alerts, settings")
	mode := fs.String("compat", "auto", "glyph compatibility: auto, blocks, ascii, off")
	seed := fs.Int64("seed", 1, "random seed of the demo data")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return handleParseErr(err)
	}
	if err := expectArgs(fs, rest, 0); err != nil {
		return err
	}
	width, height, err := parseSize(*size)
	if err != nil {
		return err
	}
	tabID, ok := tabNames[strings.ToLower(*tab)]
	if !ok {
		return fmt.Errorf("unknown tab %q (want overview, servers, detail, alerts or settings)", *tab)
	}
	compatMode, err := parseCompat(*mode)
	if err != nil {
		return err
	}

	fleet, err := newDemoFleet(*seed)
	if err != nil {
		return err
	}
	defer fleet.Close()
	deps := fleet.deps("admin", defaultListen, "xterm-256color")
	deps.DataDir = "/var/lib/vigil"
	deps.StartedAt = time.Now().Add(-(52*time.Hour + 17*time.Minute))
	frame := tui.RenderFrame(deps, tui.Options{
		Compat: compatMode,
		Env: compat.Env{
			Term:      "xterm-256color",
			ColorTerm: "truecolor",
			Lang:      "en_US.UTF-8",
		},
		SkipSplash: true,
	}, width, height, tabID)
	_, err = fmt.Fprint(std.out, frame, theme.Reset, "\n")
	return err
}

// parseSize parses "WIDTHxHEIGHT".
func parseSize(s string) (int, int, error) {
	ws, hs, ok := strings.Cut(strings.ToLower(s), "x")
	if !ok {
		return 0, 0, fmt.Errorf("invalid size %q: want WIDTHxHEIGHT, e.g. 150x44", s)
	}
	w, errW := strconv.Atoi(ws)
	h, errH := strconv.Atoi(hs)
	if errW != nil || errH != nil {
		return 0, 0, fmt.Errorf("invalid size %q: want WIDTHxHEIGHT, e.g. 150x44", s)
	}
	if w < minRenderWidth || h < minRenderHeight || w > maxRenderSize || h > maxRenderSize {
		return 0, 0, fmt.Errorf("size %dx%d out of range (%dx%d to %dx%d)", w, h, minRenderWidth, minRenderHeight, maxRenderSize, maxRenderSize)
	}
	return w, h, nil
}

// parseCompat is compat.Parse that rejects unknown names.
func parseCompat(s string) (compat.Mode, error) {
	m := compat.Parse(strings.ToLower(s))
	if m.String() != strings.ToLower(s) {
		return 0, fmt.Errorf("unknown compat mode %q (want auto, blocks, ascii or off)", s)
	}
	return m, nil
}
