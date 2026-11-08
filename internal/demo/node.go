package demo

import (
	"cmp"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/store"
)

const (
	// dayPeriod is the compressed "day" of the diurnal network and CPU
	// pattern, short enough to be visible in a 15-minute chart.
	dayPeriod = 10 * time.Minute
	// offlineFor is how long before its first sample the offline server
	// was last seen.
	offlineFor = 47*time.Minute + 13*time.Second
)

var (
	load1Alpha  = 1 - math.Exp(-1.0/60)
	load5Alpha  = 1 - math.Exp(-1.0/300)
	load15Alpha = 1 - math.Exp(-1.0/900)
)

// node is the simulation state of one server. It is guarded by Source.mu.
type node struct {
	srv  store.Server
	prof profile
	rng  *rand.Rand
	hist *metrics.History

	last    metrics.Snapshot
	hasLast bool

	phase    float64
	coreBias []float64
	pids     []int

	cpuLevel float64
	burst    float64
	memFrac  float64
	swapFrac float64
	diskFill []float64
	load     metrics.Load
	netBurst float64
	latency  float64
	rxBytes  uint64
	txBytes  uint64
	bootAt   time.Time
	lastSeen time.Time
}

// newNode derives a node from the server name and seed so a server's
// output does not depend on which other servers exist.
func newNode(srv store.Server, seed uint64, capacity int) *node {
	h := fnv.New64a()
	_, _ = h.Write([]byte(srv.Name))
	rng := rand.New(rand.NewPCG(seed, h.Sum64()))
	prof := profileFor(srv.Name, rng)

	n := &node{
		srv:      srv,
		prof:     prof,
		rng:      rng,
		hist:     metrics.NewHistory(capacity, time.Second),
		phase:    rng.Float64() * 2 * math.Pi,
		coreBias: make([]float64, prof.cores),
		cpuLevel: prof.cpuBase,
		memFrac:  prof.memBase,
		swapFrac: prof.swapBase,
		latency:  prof.latencyMs,
		rxBytes:  uint64(rng.Float64() * float64(400*gib)),
		txBytes:  uint64(rng.Float64() * float64(900*gib)),
	}
	for i := range n.coreBias {
		n.coreBias[i] = rng.NormFloat64() * 7
	}
	if len(n.coreBias) > 0 {
		n.coreBias[0] += 4
	}
	for _, d := range prof.disks {
		n.diskFill = append(n.diskFill, d.fill)
	}
	procs := slices.Concat(prof.procs, commonProcs)
	for _, p := range procs {
		pid := 400 + rng.IntN(60000)
		if p.user == "root" && rng.IntN(2) == 0 {
			pid = 1 + rng.IntN(900)
		}
		n.pids = append(n.pids, pid)
	}
	steady := prof.cpuBase / 100 * float64(prof.cores)
	n.load = metrics.Load{One: steady, Five: steady, Fifteen: steady}
	return n
}

// step advances the simulation by one second and returns the snapshot
// for time at.
func (n *node) step(at time.Time) metrics.Snapshot {
	if n.bootAt.IsZero() {
		n.bootAt = at.Add(-n.prof.uptime)
		n.lastSeen = at.Add(-offlineFor)
	}
	snap := metrics.Snapshot{
		ServerID: n.srv.ID,
		At:       at,
		Hostname: n.srv.Name,
		OS:       n.prof.os,
		Kernel:   n.prof.kernel,
		CPUCount: n.prof.cores,
	}
	if n.prof.offline {
		snap.State = metrics.StateOffline
		snap.Err = n.prof.err
		snap.LastSeen = n.lastSeen
		snap.Uptime = n.lastSeen.Sub(n.bootAt)
		return snap
	}

	t := float64(at.UnixMilli()) / 1000
	day := math.Sin(2*math.Pi*t/dayPeriod.Seconds() + n.phase)
	wave := math.Sin(2*math.Pi*t/97 + 1.7*n.phase)

	snap.State = metrics.StateOnline
	snap.LastSeen = at
	snap.Uptime = at.Sub(n.bootAt)
	snap.CPU = n.stepCPU(day, wave)
	snap.Load = n.stepLoad(snap.CPU.Total)
	snap.Mem = n.stepMem()
	snap.Disks = n.stepDisks()
	snap.Net = n.stepNet(day)
	snap.Latency = n.stepLatency()
	snap.Procs = n.procs(snap.CPU.Total, snap.Mem.Total)
	return snap
}

func (n *node) stepCPU(day, wave float64) metrics.CPU {
	p, r := n.prof, n.rng
	n.cpuLevel += (p.cpuBase-n.cpuLevel)*0.08 + r.NormFloat64()*p.cpuNoise
	if r.Float64() < p.burstChance {
		n.burst += 10 + r.Float64()*18
	}
	n.burst *= 0.75
	swing := cmp.Or(p.daySwing, p.cpuBase*0.12)
	total := clamp(n.cpuLevel+day*swing+wave*2.5+n.burst, p.cpuMin, p.cpuMax)

	iowait := total * p.ioWait * (0.6 + 0.8*r.Float64())
	steal := total * p.steal * r.Float64()
	system := total * (0.18 + 0.08*r.Float64())
	cpu := metrics.CPU{
		Total:  total,
		IOWait: iowait,
		Steal:  steal,
		System: system,
		User:   max(total-system-iowait-steal, 0),
		Cores:  make([]float64, len(n.coreBias)),
	}
	for i, bias := range n.coreBias {
		cpu.Cores[i] = clamp(total+bias+r.NormFloat64()*5, 0, 100)
	}
	return cpu
}

func (n *node) stepLoad(cpuTotal float64) metrics.Load {
	inst := cpuTotal / 100 * float64(n.prof.cores) * (0.9 + 0.2*n.rng.Float64()) * (1 + n.prof.ioWait)
	n.load.One += (inst - n.load.One) * load1Alpha
	n.load.Five += (inst - n.load.Five) * load5Alpha
	n.load.Fifteen += (inst - n.load.Fifteen) * load15Alpha
	return n.load
}

func (n *node) stepMem() metrics.Memory {
	p, r := n.prof, n.rng
	n.memFrac = clamp(n.memFrac+(p.memBase-n.memFrac)*0.02+r.NormFloat64()*0.002, 0.05, 0.97)
	n.swapFrac = clamp(n.swapFrac+(p.swapBase-n.swapFrac)*0.01+r.NormFloat64()*0.001, 0, 0.9)
	total := float64(p.memTotal)
	used := uint64(total * n.memFrac)
	return metrics.Memory{
		Total:     p.memTotal,
		Used:      used,
		Available: p.memTotal - used,
		Buffers:   uint64(total * 0.015),
		Cached:    uint64(total * (1 - n.memFrac) * 0.7),
		SwapTotal: p.swapTotal,
		SwapUsed:  uint64(float64(p.swapTotal) * n.swapFrac),
	}
}

func (n *node) stepDisks() []metrics.Disk {
	disks := make([]metrics.Disk, len(n.prof.disks))
	for i, spec := range n.prof.disks {
		lo := cmp.Or(spec.minFill, 0.01)
		hi := cmp.Or(spec.maxFill, 0.99)
		n.diskFill[i] = clamp(n.diskFill[i]+spec.growth+n.rng.NormFloat64()*2e-5, lo, hi)
		disks[i] = metrics.Disk{
			Device: spec.device,
			Mount:  spec.mount,
			FSType: spec.fstype,
			Total:  spec.total,
			Used:   uint64(float64(spec.total) * n.diskFill[i]),
		}
	}
	return disks
}

func (n *node) stepNet(day float64) metrics.Net {
	p, r := n.prof, n.rng
	if r.Float64() < 0.012 {
		n.netBurst += 2 + 3*r.Float64()
	}
	n.netBurst *= 0.85
	factor := (1 + 0.45*day) * (1 + 0.08*r.NormFloat64())
	rx := max(p.rxBase*(factor+n.netBurst), 0)
	tx := max(p.txBase*(factor+0.6*n.netBurst), 0)
	n.rxBytes += uint64(rx)
	n.txBytes += uint64(tx)
	return metrics.Net{RxBps: rx, TxBps: tx, RxBytes: n.rxBytes, TxBytes: n.txBytes}
}

func (n *node) stepLatency() time.Duration {
	base := n.prof.latencyMs
	n.latency += (base-n.latency)*0.2 + n.rng.NormFloat64()*base*0.08
	if n.rng.Float64() < 0.01 {
		n.latency += base * (1 + 2*n.rng.Float64())
	}
	n.latency = clamp(n.latency, base*0.5, base*4)
	return time.Duration(n.latency * float64(time.Millisecond))
}

func (n *node) procs(cpuTotal float64, memTotal uint64) []metrics.Process {
	specs := slices.Concat(n.prof.procs, commonProcs)
	procs := make([]metrics.Process, len(specs))
	for i, spec := range specs {
		rss := uint64(float64(spec.rss) * (0.97 + 0.06*n.rng.Float64()))
		procs[i] = metrics.Process{
			PID:     n.pids[i],
			User:    spec.user,
			Command: spec.command,
			CPU:     clamp(spec.share*cpuTotal*(0.7+0.6*n.rng.Float64()), 0, 100),
			MemPct:  float64(rss) / float64(max(memTotal, 1)) * 100,
			RSS:     rss,
		}
	}
	slices.SortStableFunc(procs, func(a, b metrics.Process) int {
		return cmp.Or(cmp.Compare(b.CPU, a.CPU), cmp.Compare(a.PID, b.PID))
	})
	return procs
}

func clamp(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}

func cloneSnapshot(s metrics.Snapshot) metrics.Snapshot {
	s.CPU.Cores = slices.Clone(s.CPU.Cores)
	s.Disks = slices.Clone(s.Disks)
	s.Procs = slices.Clone(s.Procs)
	return s
}
