// Package alerts evaluates snapshots against thresholds and keeps the list
// of active and recently resolved alerts plus an event log that each TUI
// session polls for toasts.
package alerts

import (
	"time"

	"github.com/CraguS/vigil/internal/store"
)

// Kind is what an alert is about.
type Kind int

const (
	KindOffline Kind = iota
	KindCPU
	KindMem
	KindDisk
)

// String returns a short label: offline, cpu, memory, disk.
func (k Kind) String() string {
	switch k {
	case KindCPU:
		return "cpu"
	case KindMem:
		return "memory"
	case KindDisk:
		return "disk"
	default:
		return "offline"
	}
}

// Severity of an alert.
type Severity int

const (
	SeverityWarn Severity = iota
	SeverityCrit
)

// String returns "warn" or "crit".
func (s Severity) String() string {
	if s == SeverityCrit {
		return "crit"
	}
	return "warn"
}

// Alert is one condition on one server. Its ID is stable for the
// (server, kind, subject) triple while the condition lasts; a new
// occurrence after resolution gets a new ID.
type Alert struct {
	ID         string
	ServerID   string
	ServerName string
	Kind       Kind
	Severity   Severity
	// Subject narrows the alert, e.g. the mount point for disk alerts.
	Subject    string
	Message    string
	Value      float64
	Threshold  float64
	Since      time.Time
	Resolved   bool
	ResolvedAt time.Time
	Acked      bool
}

// EventType tells whether an alert fired, escalated or resolved.
type EventType int

const (
	EventFired EventType = iota
	EventEscalated
	EventResolved
)

// String returns "fired", "escalated" or "resolved".
func (t EventType) String() string {
	switch t {
	case EventEscalated:
		return "escalated"
	case EventResolved:
		return "resolved"
	default:
		return "fired"
	}
}

// Event is appended to the log whenever an alert changes state.
type Event struct {
	Seq   uint64
	Type  EventType
	Alert Alert
	At    time.Time
}

// Thresholds are utilization percentages and the offline grace period. A
// percentage of 0 or less disables that level.
type Thresholds struct {
	CPUWarn, CPUCrit   float64
	MemWarn, MemCrit   float64
	DiskWarn, DiskCrit float64
	OfflineGrace       time.Duration
}

// ThresholdsFromSettings extracts the alert thresholds from the manager
// settings.
func ThresholdsFromSettings(set store.Settings) Thresholds {
	return Thresholds{
		CPUWarn:      set.CPUWarn,
		CPUCrit:      set.CPUCrit,
		MemWarn:      set.MemWarn,
		MemCrit:      set.MemCrit,
		DiskWarn:     set.DiskWarn,
		DiskCrit:     set.DiskCrit,
		OfflineGrace: set.OfflineGrace,
	}
}
