package collector

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	"github.com/Aepzie/vigil/internal/metrics"
)

// topProcesses is the number of processes reported per snapshot.
const topProcesses = 10

// buildSnapshot turns a parsed sample into an online snapshot. Rates
// (CPU, network, per-process CPU) are computed from the deltas against
// prev; without a usable previous sample of the same boot they are zero.
func buildSnapshot(id string, cur, prev *sample, latency time.Duration) metrics.Snapshot {
	if prev != nil && cur.uptime < prev.uptime {
		prev = nil
	}
	cpuCount := max(len(cur.cores), 1)
	osName := cur.osName
	if osName == "" {
		osName = cur.system
	}
	snap := metrics.Snapshot{
		ServerID: id,
		State:    metrics.StateOnline,
		At:       cur.at,
		LastSeen: cur.at,
		Latency:  latency,
		Hostname: cur.hostname,
		OS:       osName,
		Kernel:   cur.kernel,
		Uptime:   time.Duration(cur.uptime * float64(time.Second)),
		CPUCount: cpuCount,
		Load:     cur.load,
		Mem:      cur.mem,
		Disks:    slices.Clone(cur.disks),
		Net:      netUsage(cur, prev),
		Procs:    processList(cur, prev, cpuCount),
	}
	if prev != nil {
		snap.CPU = cpuUsage(prev.cpu, cur.cpu)
		snap.CPU.Cores = coreUsage(prev.cores, cur.cores)
	} else {
		snap.CPU.Cores = make([]float64, len(cur.cores))
	}
	return snap
}

// cpuUsage computes utilization percentages between two samples. Fields
// that went backwards (some kernels' iowait does) count as zero.
func cpuUsage(prev, cur cpuTimes) metrics.CPU {
	d := cpuTimes{
		user:    delta(prev.user, cur.user),
		nice:    delta(prev.nice, cur.nice),
		system:  delta(prev.system, cur.system),
		idle:    delta(prev.idle, cur.idle),
		iowait:  delta(prev.iowait, cur.iowait),
		irq:     delta(prev.irq, cur.irq),
		softirq: delta(prev.softirq, cur.softirq),
		steal:   delta(prev.steal, cur.steal),
	}
	total := float64(d.total())
	if total == 0 {
		return metrics.CPU{}
	}
	pct := func(v uint64) float64 { return clampPct(float64(v) / total * 100) }
	return metrics.CPU{
		Total:  pct(d.total() - d.idle - d.iowait),
		User:   pct(d.user + d.nice),
		System: pct(d.system + d.irq + d.softirq),
		IOWait: pct(d.iowait),
		Steal:  pct(d.steal),
	}
}

// coreUsage returns per-core busy percentages, matching cores by index
// so CPU hotplug does not misalign them.
func coreUsage(prev, cur []coreTimes) []float64 {
	byID := make(map[int]cpuTimes, len(prev))
	for _, c := range prev {
		byID[c.id] = c.times
	}
	out := make([]float64, len(cur))
	for i, c := range cur {
		if p, ok := byID[c.id]; ok {
			out[i] = cpuUsage(p, c.times).Total
		}
	}
	return out
}

// netUsage sums counters of all counted interfaces and derives bit rates
// from per-interface byte deltas, so interfaces that appear, disappear or
// reset their counters do not produce spikes.
func netUsage(cur, prev *sample) metrics.Net {
	var n metrics.Net
	var drx, dtx uint64
	for name, c := range cur.ifaces {
		n.RxBytes = satAdd(n.RxBytes, c.rx)
		n.TxBytes = satAdd(n.TxBytes, c.tx)
		if prev == nil {
			continue
		}
		p, ok := prev.ifaces[name]
		if !ok {
			continue
		}
		drx = satAdd(drx, delta(p.rx, c.rx))
		dtx = satAdd(dtx, delta(p.tx, c.tx))
	}
	if prev == nil {
		return n
	}
	secs := elapsedSeconds(prev, cur)
	if secs <= 0 {
		return n
	}
	n.RxBps = float64(drx) * 8 / secs
	n.TxBps = float64(dtx) * 8 / secs
	return n
}

// elapsedSeconds prefers the remote uptime delta, which is immune to
// network latency jitter, and falls back to local wall time.
func elapsedSeconds(prev, cur *sample) float64 {
	if d := cur.uptime - prev.uptime; d > 0 {
		return d
	}
	return cur.at.Sub(prev.at).Seconds()
}

type procKey struct {
	pid   int
	start uint64
}

// processList returns the top processes by CPU, then RSS. CPU is percent
// of one core (like top), so a process using two full cores reports 200.
// Processes started since prev are charged their whole CPU time; a PID
// reused by a new process is told apart by its start time.
func processList(cur, prev *sample, cpuCount int) []metrics.Process {
	var prevTicks map[procKey]uint64
	var tickWindow, prevBootTicks float64
	if prev != nil {
		prevTicks = make(map[procKey]uint64, len(prev.procs))
		for _, p := range prev.procs {
			prevTicks[procKey{p.pid, p.start}] = p.ticks
		}
		tickWindow = float64(delta(prev.cpu.total(), cur.cpu.total())) / float64(cpuCount)
		prevBootTicks = prev.uptime * float64(cur.clkTck)
	}
	procs := make([]metrics.Process, 0, len(cur.procs))
	uids := make(map[int]int, len(cur.procs))
	for _, p := range cur.procs {
		uids[p.pid] = p.uid
		rss := satMul(p.rss, cur.pageSize)
		proc := metrics.Process{
			PID:     p.pid,
			Command: p.comm,
			RSS:     rss,
			MemPct:  clampPct(float64(rss) / float64(max(cur.mem.Total, 1)) * 100),
		}
		if tickWindow > 0 {
			var used uint64
			if before, ok := prevTicks[procKey{p.pid, p.start}]; ok {
				used = delta(before, p.ticks)
			} else if float64(p.start) >= prevBootTicks {
				used = p.ticks
			}
			proc.CPU = min(float64(used)/tickWindow*100, 100*float64(cpuCount))
		}
		procs = append(procs, proc)
	}
	slices.SortFunc(procs, func(a, b metrics.Process) int {
		if c := cmp.Compare(b.CPU, a.CPU); c != 0 {
			return c
		}
		if c := cmp.Compare(b.RSS, a.RSS); c != 0 {
			return c
		}
		return cmp.Compare(a.PID, b.PID)
	})
	procs = procs[:min(len(procs), topProcesses)]

	for i := range procs {
		procs[i].Command = cleanString(procs[i].Command)
		procs[i].User = userName(cur, uids[procs[i].PID])
	}
	return procs
}

func userName(s *sample, uid int) string {
	if name, ok := s.users[uid]; ok {
		return name
	}
	return strconv.Itoa(uid)
}

func delta(prev, cur uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func clampPct(v float64) float64 {
	switch {
	case v != v || v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}
