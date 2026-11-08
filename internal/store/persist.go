package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	serversFile  = "servers.json"
	settingsFile = "settings.json"
	fileVersion  = 1
)

type serversEnvelope struct {
	Version int      `json:"version"`
	Servers []Server `json:"servers"`
}

type settingsEnvelope struct {
	Version  int          `json:"version"`
	Settings settingsJSON `json:"settings"`
}

// settingsJSON is the on-disk form of Settings: pointer fields tell a
// missing value (filled from DefaultSettings) from an explicit zero, and
// durations are human-editable strings such as "3s".
type settingsJSON struct {
	PollInterval   *duration `json:"poll_interval,omitempty"`
	HistoryMinutes *int      `json:"history_minutes,omitempty"`
	CPUWarn        *float64  `json:"cpu_warn,omitempty"`
	CPUCrit        *float64  `json:"cpu_crit,omitempty"`
	MemWarn        *float64  `json:"mem_warn,omitempty"`
	MemCrit        *float64  `json:"mem_crit,omitempty"`
	DiskWarn       *float64  `json:"disk_warn,omitempty"`
	DiskCrit       *float64  `json:"disk_crit,omitempty"`
	OfflineGrace   *duration `json:"offline_grace,omitempty"`
	Compat         *string   `json:"compat,omitempty"`
}

func toSettingsJSON(s Settings) settingsJSON {
	poll, grace := duration(s.PollInterval), duration(s.OfflineGrace)
	return settingsJSON{
		PollInterval:   &poll,
		HistoryMinutes: &s.HistoryMinutes,
		CPUWarn:        &s.CPUWarn,
		CPUCrit:        &s.CPUCrit,
		MemWarn:        &s.MemWarn,
		MemCrit:        &s.MemCrit,
		DiskWarn:       &s.DiskWarn,
		DiskCrit:       &s.DiskCrit,
		OfflineGrace:   &grace,
		Compat:         &s.Compat,
	}
}

// mergeOver returns base with every field present in j replaced.
func (j settingsJSON) mergeOver(base Settings) Settings {
	setIf(&base.PollInterval, (*time.Duration)(j.PollInterval))
	setIf(&base.HistoryMinutes, j.HistoryMinutes)
	setIf(&base.CPUWarn, j.CPUWarn)
	setIf(&base.CPUCrit, j.CPUCrit)
	setIf(&base.MemWarn, j.MemWarn)
	setIf(&base.MemCrit, j.MemCrit)
	setIf(&base.DiskWarn, j.DiskWarn)
	setIf(&base.DiskCrit, j.DiskCrit)
	setIf(&base.OfflineGrace, (*time.Duration)(j.OfflineGrace))
	setIf(&base.Compat, j.Compat)
	return base
}

func setIf[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// duration marshals as a Go duration string ("3s") and unmarshals from
// either such a string or a number of nanoseconds.
type duration time.Duration

func (d duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q", s)
		}
		*d = duration(parsed)
		return nil
	}
	var ns int64
	if err := json.Unmarshal(b, &ns); err != nil {
		return fmt.Errorf("invalid duration %s", b)
	}
	*d = duration(ns)
	return nil
}

// readFile returns the file content, or nil without error when the file
// does not exist.
func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	return b, nil
}

func checkVersion(path string, version int) error {
	if version > fileVersion {
		return fmt.Errorf("store: %s has unsupported version %d (this vigil understands %d)", path, version, fileVersion)
	}
	if version < 0 {
		return fmt.Errorf("store: %s has invalid version %d", path, version)
	}
	return nil
}

func decodeStrict(path string, b []byte, v any) error {
	if len(bytes.TrimSpace(b)) == 0 {
		return fmt.Errorf("store: %s is empty or corrupt; fix or remove it", path)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("store: %s is corrupt (%v); fix or remove it", path, err)
	}
	return nil
}

func loadServers(path string) ([]Server, error) {
	b, err := readFile(path)
	if err != nil || b == nil {
		return nil, err
	}
	var env serversEnvelope
	if err := decodeStrict(path, b, &env); err != nil {
		return nil, err
	}
	if err := checkVersion(path, env.Version); err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(env.Servers))
	names := make(map[string]bool, len(env.Servers))
	servers := make([]Server, 0, len(env.Servers))
	for i, srv := range env.Servers {
		srv = srv.Normalized()
		if err := srv.Validate(); err != nil {
			return nil, fmt.Errorf("store: %s: server #%d (%q): %w", path, i+1, srv.Name, err)
		}
		key := strings.ToLower(srv.Name)
		switch {
		case srv.ID == "":
			return nil, fmt.Errorf("store: %s: server #%d (%q) has no id", path, i+1, srv.Name)
		case ids[srv.ID]:
			return nil, fmt.Errorf("store: %s: duplicate server id %q", path, srv.ID)
		case names[key]:
			return nil, fmt.Errorf("store: %s: duplicate server name %q", path, srv.Name)
		}
		ids[srv.ID], names[key] = true, true
		servers = append(servers, srv)
	}
	sortServers(servers)
	return servers, nil
}

func loadSettings(path string) (Settings, error) {
	b, err := readFile(path)
	if err != nil || b == nil {
		return DefaultSettings(), err
	}
	var env settingsEnvelope
	if err := decodeStrict(path, b, &env); err != nil {
		return Settings{}, err
	}
	if err := checkVersion(path, env.Version); err != nil {
		return Settings{}, err
	}
	set := env.Settings.mergeOver(DefaultSettings())
	if err := set.Validate(); err != nil {
		return Settings{}, fmt.Errorf("store: %s: %w", path, err)
	}
	return set, nil
}

// writeJSONAtomic writes v as indented JSON to path via a temporary file
// in the same directory that is synced and renamed over the target, so
// readers never observe a partially written file.
func writeJSONAtomic(path string, v any) (err error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", filepath.Base(path), err)
	}
	b = append(b, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("store: write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err = tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("store: write %s: %w", path, err)
	}
	if _, err = tmp.Write(b); err != nil {
		return fmt.Errorf("store: write %s: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("store: sync %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("store: close %s: %w", path, err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("store: replace %s: %w", path, err)
	}
	syncDir(dir)
	return nil
}

// syncDir makes the rename durable where the platform supports it.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
