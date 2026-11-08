// Package metrics defines the data model for one poll of a managed server
// and fixed-size histories of recent values.
package metrics

import (
	"sync"
	"time"
)

// State is the reachability of a server.
type State int

const (
	StateUnknown State = iota
	StateConnecting
	StateOnline
	StateOffline
)

// String returns a lowercase label: unknown, connecting, online, offline.
func (s State) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateOnline:
		return "online"
	case StateOffline:
		return "offline"
	default:
		return "unknown"
	}
}

// CPU utilization in percent (0..100).
type CPU struct {
	Total  float64
	User   float64
	System float64
	IOWait float64
	Steal  float64
	Cores  []float64
}

// Memory values in bytes.
type Memory struct {
	Total     uint64
	Used      uint64
	Available uint64
	Buffers   uint64
	Cached    uint64
	SwapTotal uint64
	SwapUsed  uint64
}

// UsedPct is Used/Total in percent.
func (m Memory) UsedPct() float64 { return pct(m.Used, m.Total) }

// SwapPct is SwapUsed/SwapTotal in percent.
func (m Memory) SwapPct() float64 { return pct(m.SwapUsed, m.SwapTotal) }

// Load averages.
type Load struct {
	One, Five, Fifteen float64
}

// Disk is one mounted filesystem; sizes in bytes.
type Disk struct {
	Device string
	Mount  string
	FSType string
	Total  uint64
	Used   uint64
}

// UsedPct is Used/Total in percent.
func (d Disk) UsedPct() float64 { return pct(d.Used, d.Total) }

// Net is the aggregated rate over all non-loopback interfaces.
type Net struct {
	RxBps   float64
	TxBps   float64
	RxBytes uint64
	TxBytes uint64
}

// Process is one entry of the top-process list.
type Process struct {
	PID     int
	User    string
	Command string
	CPU     float64
	MemPct  float64
	RSS     uint64
}

// Snapshot is the result of one poll. All strings that originate from the
// remote host are already sanitized by the collector.
type Snapshot struct {
	ServerID string
	State    State
	// Err is a short human-readable reason when State is offline.
	Err string
	// At is when the poll completed; LastSeen is the last time the server
	// answered successfully.
	At       time.Time
	LastSeen time.Time
	Latency  time.Duration

	Hostname string
	OS       string
	Kernel   string
	Uptime   time.Duration
	CPUCount int

	CPU   CPU
	Load  Load
	Mem   Memory
	Disks []Disk
	Net   Net
	Procs []Process
}

// MaxDiskPct returns the highest disk utilization and its mount point.
func (s Snapshot) MaxDiskPct() (float64, string) {
	best, mount := 0.0, ""
	for _, d := range s.Disks {
		if p := d.UsedPct(); p > best {
			best, mount = p, d.Mount
		}
	}
	return best, mount
}

// Series is a copied, read-only view of a server's history. Every slice
// is ordered oldest to newest and sampled every Step.
type Series struct {
	Step    time.Duration
	CPU     []float64
	Mem     []float64
	Swap    []float64
	RxBps   []float64
	TxBps   []float64
	Latency []float64
	Load1   []float64
}

// History accumulates samples for one server. It is safe for concurrent
// use.
type History struct {
	mu      sync.RWMutex
	step    time.Duration
	cpu     *Ring
	mem     *Ring
	swap    *Ring
	rx      *Ring
	tx      *Ring
	latency *Ring
	load1   *Ring
}

// NewHistory keeps capacity samples taken every step.
func NewHistory(capacity int, step time.Duration) *History {
	return &History{
		step:    step,
		cpu:     NewRing(capacity),
		mem:     NewRing(capacity),
		swap:    NewRing(capacity),
		rx:      NewRing(capacity),
		tx:      NewRing(capacity),
		latency: NewRing(capacity),
		load1:   NewRing(capacity),
	}
}

// Add records a snapshot. Offline snapshots record zeros for rates and
// utilization so gaps remain visible.
func (h *History) Add(s Snapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.State != StateOnline {
		for _, r := range []*Ring{h.cpu, h.mem, h.swap, h.rx, h.tx, h.latency, h.load1} {
			r.Push(0)
		}
		return
	}
	h.cpu.Push(s.CPU.Total)
	h.mem.Push(s.Mem.UsedPct())
	h.swap.Push(s.Mem.SwapPct())
	h.rx.Push(s.Net.RxBps)
	h.tx.Push(s.Net.TxBps)
	h.latency.Push(float64(s.Latency) / float64(time.Millisecond))
	h.load1.Push(s.Load.One)
}

// Series returns a copy of the history.
func (h *History) Series() Series {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Series{
		Step:    h.step,
		CPU:     h.cpu.Values(),
		Mem:     h.mem.Values(),
		Swap:    h.swap.Values(),
		RxBps:   h.rx.Values(),
		TxBps:   h.tx.Values(),
		Latency: h.latency.Values(),
		Load1:   h.load1.Values(),
	}
}

// Ring is a fixed-capacity FIFO of float64. It is not safe for concurrent
// use on its own.
type Ring struct {
	buf  []float64
	head int
	n    int
}

// NewRing creates a ring holding up to capacity values (minimum 1).
func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]float64, max(capacity, 1))}
}

// Push appends v, evicting the oldest value when full.
func (r *Ring) Push(v float64) {
	r.buf[(r.head+r.n)%len(r.buf)] = v
	if r.n < len(r.buf) {
		r.n++
		return
	}
	r.head = (r.head + 1) % len(r.buf)
}

// Len is the number of stored values.
func (r *Ring) Len() int { return r.n }

// Values returns the stored values, oldest first, as a new slice.
func (r *Ring) Values() []float64 {
	out := make([]float64, r.n)
	for i := range r.n {
		out[i] = r.buf[(r.head+i)%len(r.buf)]
	}
	return out
}

// Last returns the newest value, or 0 when empty.
func (r *Ring) Last() float64 {
	if r.n == 0 {
		return 0
	}
	return r.buf[(r.head+r.n-1)%len(r.buf)]
}

func pct(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
