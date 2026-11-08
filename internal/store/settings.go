package store

import (
	"fmt"
	"slices"
	"time"
)

// Bounds applied by Settings.Validate.
const (
	MinPollInterval   = time.Second
	MaxPollInterval   = 60 * time.Second
	MinHistoryMinutes = 1
	MaxHistoryMinutes = 240
	MaxOfflineGrace   = 10 * time.Minute
)

// CompatModes are the accepted values of Settings.Compat.
var CompatModes = []string{"auto", "blocks", "ascii", "off"}

// Settings are manager-wide options editable in the Settings tab.
type Settings struct {
	PollInterval time.Duration `json:"poll_interval"`
	// HistoryMinutes is how much history the collector keeps per server.
	HistoryMinutes int     `json:"history_minutes"`
	CPUWarn        float64 `json:"cpu_warn"`
	CPUCrit        float64 `json:"cpu_crit"`
	MemWarn        float64 `json:"mem_warn"`
	MemCrit        float64 `json:"mem_crit"`
	DiskWarn       float64 `json:"disk_warn"`
	DiskCrit       float64 `json:"disk_crit"`
	// OfflineGrace is how long a server must be unreachable before an
	// offline alert fires.
	OfflineGrace time.Duration `json:"offline_grace"`
	// Compat is the default glyph compatibility mode name
	// (auto, blocks, ascii, off).
	Compat string `json:"compat"`
}

// DefaultSettings returns sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		PollInterval:   3 * time.Second,
		HistoryMinutes: 15,
		CPUWarn:        80,
		CPUCrit:        95,
		MemWarn:        85,
		MemCrit:        95,
		DiskWarn:       85,
		DiskCrit:       95,
		OfflineGrace:   10 * time.Second,
		Compat:         "auto",
	}
}

// Validate checks every field and returns a readable *FieldError for the
// first invalid one.
func (s Settings) Validate() error {
	if s.PollInterval < MinPollInterval || s.PollInterval > MaxPollInterval {
		return fieldErr("poll_interval", fmt.Sprintf("must be between %v and %v (got %v)", MinPollInterval, MaxPollInterval, s.PollInterval))
	}
	if s.HistoryMinutes < MinHistoryMinutes || s.HistoryMinutes > MaxHistoryMinutes {
		return fieldErr("history_minutes", fmt.Sprintf("must be between %d and %d (got %d)", MinHistoryMinutes, MaxHistoryMinutes, s.HistoryMinutes))
	}
	pairs := []struct {
		name       string
		warn, crit float64
	}{
		{"cpu", s.CPUWarn, s.CPUCrit},
		{"mem", s.MemWarn, s.MemCrit},
		{"disk", s.DiskWarn, s.DiskCrit},
	}
	for _, p := range pairs {
		if err := validateThresholds(p.name, p.warn, p.crit); err != nil {
			return err
		}
	}
	if s.OfflineGrace < 0 || s.OfflineGrace > MaxOfflineGrace {
		return fieldErr("offline_grace", fmt.Sprintf("must be between 0s and %v (got %v)", MaxOfflineGrace, s.OfflineGrace))
	}
	if !slices.Contains(CompatModes, s.Compat) {
		return fieldErr("compat", fmt.Sprintf("%q is not one of auto, blocks, ascii, off", s.Compat))
	}
	return nil
}

func validateThresholds(name string, warn, crit float64) error {
	warnField, critField := name+"_warn", name+"_crit"
	if !inPercentRange(warn) {
		return fieldErr(warnField, fmt.Sprintf("must be between 1 and 100 (got %g)", warn))
	}
	if !inPercentRange(crit) {
		return fieldErr(critField, fmt.Sprintf("must be between 1 and 100 (got %g)", crit))
	}
	if warn >= crit {
		return fieldErr(warnField, fmt.Sprintf("must be lower than %s (%g ≥ %g)", critField, warn, crit))
	}
	return nil
}

func inPercentRange(v float64) bool {
	return v >= 1 && v <= 100
}
