// Package alerts evaluates snapshots against thresholds and keeps the list
// of active and recently resolved alerts plus an event log that each TUI
// session polls for toasts.
package alerts

import (
	"sync"
	"time"

	"github.com/CraguS/vigil/internal/metrics"
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
// (server, kind, subject) triple while the condition lasts.
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

// Event is appended to the log whenever an alert changes state.
type Event struct {
	Seq   uint64
	Type  EventType
	Alert Alert
	At    time.Time
}

// Thresholds are utilization percentages and the offline grace period.
type Thresholds struct {
	CPUWarn, CPUCrit   float64
	MemWarn, MemCrit   float64
	DiskWarn, DiskCrit float64
	OfflineGrace       time.Duration
}

// Engine is safe for concurrent use.
type Engine struct {
	mu         sync.RWMutex
	thresholds Thresholds
	seq        uint64
}

// NewEngine creates an engine with the given thresholds.
func NewEngine(th Thresholds) *Engine {
	return &Engine{thresholds: th}
}

// SetThresholds replaces the thresholds; they apply on the next Evaluate.
func (e *Engine) SetThresholds(th Thresholds) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.thresholds = th
}

// Evaluate checks one snapshot and updates alert state. serverName is
// used for messages.
func (e *Engine) Evaluate(serverName string, snap metrics.Snapshot) {}

// Forget drops all alerts of a removed server (resolving them).
func (e *Engine) Forget(serverID string) {}

// Active returns unresolved alerts, most severe and newest first.
func (e *Engine) Active() []Alert { return nil }

// Recent returns up to n alerts including resolved ones, newest first.
func (e *Engine) Recent(n int) []Alert { return nil }

// Ack marks an alert as acknowledged.
func (e *Engine) Ack(id string) {}

// EventsSince returns events with Seq > seq and the latest sequence
// number. Callers start with seq = LatestSeq() to skip history.
func (e *Engine) EventsSince(seq uint64) ([]Event, uint64) { return nil, seq }

// LatestSeq returns the current sequence number.
func (e *Engine) LatestSeq() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.seq
}
