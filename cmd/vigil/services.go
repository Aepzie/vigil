package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/collector"
	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/sanitize"
	"github.com/CraguS/vigil/internal/sshclient"
	"github.com/CraguS/vigil/internal/store"
)

// dataDirHelp documents the --data flag.
const dataDirHelp = "data directory (default: $VIGIL_DATA, /var/lib/vigil as root on Linux, else the user config dir)"

// resolveDataDir returns flagValue if set, else the default data
// directory.
func resolveDataDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	return defaultDataDir(os.Getenv, runtime.GOOS, os.Geteuid(), os.UserConfigDir)
}

// defaultDataDir implements the data directory lookup order: $VIGIL_DATA,
// /var/lib/vigil for root on Linux, the user config directory.
func defaultDataDir(getenv func(string) string, goos string, euid int, configDir func() (string, error)) (string, error) {
	if d := getenv("VIGIL_DATA"); d != "" {
		return filepath.Abs(d)
	}
	if goos == "linux" && euid == 0 {
		return "/var/lib/vigil", nil
	}
	dir, err := configDir()
	if err != nil {
		return "", fmt.Errorf("no data directory: set $VIGIL_DATA or use --data (%w)", err)
	}
	return filepath.Join(dir, "vigil"), nil
}

// services are the long-lived parts shared by `serve` and `local`.
type services struct {
	store  *store.Store
	engine *alerts.Engine
	dialer *sshclient.Dialer
	hub    *collector.Hub
}

// openServices opens the store and wires the collector hub to the alerts
// engine.
func openServices(dataDir string) (*services, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	engine := alerts.NewEngine(thresholdsFrom(st.Settings()))
	dialer, err := sshclient.NewDialer(dataDir)
	if err != nil {
		return nil, fmt.Errorf("manager key: %w", err)
	}
	hub, err := collector.New(collector.Options{
		Store:  st,
		Dialer: dialer,
		OnSnapshot: func(srv store.Server, snap metrics.Snapshot) {
			engine.SetThresholds(thresholdsFrom(st.Settings()))
			engine.Evaluate(srv.Name, snap)
		},
		OnRemove: engine.Forget,
	})
	if err != nil {
		return nil, fmt.Errorf("collector: %w", err)
	}
	return &services{store: st, engine: engine, dialer: dialer, hub: hub}, nil
}

// thresholdsFrom converts the alert settings of the store.
func thresholdsFrom(s store.Settings) alerts.Thresholds {
	return alerts.Thresholds{
		CPUWarn:      s.CPUWarn,
		CPUCrit:      s.CPUCrit,
		MemWarn:      s.MemWarn,
		MemCrit:      s.MemCrit,
		DiskWarn:     s.DiskWarn,
		DiskCrit:     s.DiskCrit,
		OfflineGrace: s.OfflineGrace,
	}
}

// localUser returns the name of the user running vigil, for display.
func localUser() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" {
		name = os.Getenv("USERNAME")
	}
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = "local"
	}
	return sanitize.Line(name)
}
