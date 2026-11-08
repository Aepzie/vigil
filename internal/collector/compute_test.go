package collector

import (
	"math"
	"testing"
	"time"

	"github.com/CraguS/vigil/internal/metrics"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func fixtureSample(t *testing.T, poll string, at time.Time) *sample {
	t.Helper()
	s, err := parseSample(fixtureOutput(t, poll, "n"), "n", at)
	if err != nil {
		t.Fatalf("parse %s: %v", poll, err)
	}
	return s
}

func TestBuildSnapshotFirstPoll(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	snap := buildSnapshot("srv", fixtureSample(t, "poll1", t0), nil, 3*time.Millisecond)

	if snap.ServerID != "srv" || snap.State != metrics.StateOnline || !snap.At.Equal(t0) || !snap.LastSeen.Equal(t0) {
		t.Errorf("header = %+v", snap)
	}
	if snap.Latency != 3*time.Millisecond || snap.CPUCount != 4 || snap.Hostname != "web-01" ||
		snap.OS != "Ubuntu 24.04.1 LTS" || snap.Kernel != "6.8.0-45-generic" {
		t.Errorf("identity = %+v", snap)
	}
	if want := time.Duration(12345.67 * float64(time.Second)); snap.Uptime != want {
		t.Errorf("uptime = %v, want %v", snap.Uptime, want)
	}
	if snap.CPU.Total != 0 || len(snap.CPU.Cores) != 4 || snap.Net.RxBps != 0 || snap.Net.TxBps != 0 {
		t.Errorf("first poll must not report rates: cpu=%+v net=%+v", snap.CPU, snap.Net)
	}
	if snap.Net.RxBytes != 1002000000 || snap.Net.TxBytes != 503000000 {
		t.Errorf("net totals = %+v", snap.Net)
	}
	wantPIDs := []int{1234, 1, 3456, 1500, 5678, 2345, 105, 104, 103, 102}
	if len(snap.Procs) != len(wantPIDs) {
		t.Fatalf("procs = %d, want %d", len(snap.Procs), len(wantPIDs))
	}
	for i, pid := range wantPIDs {
		if snap.Procs[i].PID != pid || snap.Procs[i].CPU != 0 {
			t.Errorf("procs[%d] = %+v, want PID %d with 0%% CPU (sorted by RSS)", i, snap.Procs[i], pid)
		}
	}
}

func TestBuildSnapshotDeltas(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	prev := fixtureSample(t, "poll1", t0)
	cur := fixtureSample(t, "poll2", t0.Add(2600*time.Millisecond))
	snap := buildSnapshot("srv", cur, prev, time.Millisecond)

	wantCPU := metrics.CPU{Total: 45, User: 30, System: 13, IOWait: 5, Steal: 2}
	if !approx(snap.CPU.Total, wantCPU.Total) || !approx(snap.CPU.User, wantCPU.User) ||
		!approx(snap.CPU.System, wantCPU.System) || !approx(snap.CPU.IOWait, wantCPU.IOWait) ||
		!approx(snap.CPU.Steal, wantCPU.Steal) {
		t.Errorf("cpu = %+v, want %+v", snap.CPU, wantCPU)
	}
	wantCores := []float64{100, 30, 30, 20}
	for i, want := range wantCores {
		if !approx(snap.CPU.Cores[i], want) {
			t.Errorf("core %d = %v, want %v", i, snap.CPU.Cores[i], want)
		}
	}

	// Rates use the remote uptime delta (2.5s), not wall time (2.6s).
	if !approx(snap.Net.RxBps, 1_100_000) || !approx(snap.Net.TxBps, 200_000) {
		t.Errorf("net rates = %+v, want rx 1.1 Mbit/s, tx 200 kbit/s", snap.Net)
	}

	const kb = 1024
	if snap.Mem.Used != 3200000*kb || !approx(snap.Mem.UsedPct(), 40) || !approx(snap.Mem.SwapPct(), 25) {
		t.Errorf("mem = %+v", snap.Mem)
	}
	if snap.Load != (metrics.Load{One: 1.25, Five: 0.73, Fifteen: 0.63}) {
		t.Errorf("load = %+v", snap.Load)
	}
	if pct, mount := snap.MaxDiskPct(); mount != "/mnt/My  Backups" || !approx(pct, 90) {
		t.Errorf("max disk = %v %q", pct, mount)
	}

	want := []struct {
		pid     int
		command string
		user    string
		cpu     float64
	}{
		{1234, "java", "alice", 120},
		{2345, "weird) b", "alice", 20},
		{5678, "sshd", "root", 12},
		{3456, "nginx", "www-data", 10},
		{6789, "bash", "alice", 2},
		{4567, "kworker/0:1-events", "root", 2},
		{1, "systemd", "root", 0.8},
		{1500, "tmux: server", "4242", 0},
		{7000, "cron", "root", 0},
		{105, "agetty5", "root", 0},
	}
	if len(snap.Procs) != len(want) {
		t.Fatalf("procs = %d, want %d", len(snap.Procs), len(want))
	}
	for i, w := range want {
		p := snap.Procs[i]
		if p.PID != w.pid || p.Command != w.command || p.User != w.user || !approx(p.CPU, w.cpu) {
			t.Errorf("procs[%d] = %+v, want pid=%d cmd=%q user=%q cpu=%v", i, p, w.pid, w.command, w.user, w.cpu)
		}
	}
	java := snap.Procs[0]
	if java.RSS != 500000*4096 || !approx(java.MemPct, 25) {
		t.Errorf("java rss=%d mem%%=%v", java.RSS, java.MemPct)
	}
}

func TestBuildSnapshotAfterReboot(t *testing.T) {
	t0 := time.Now()
	prev := fixtureSample(t, "poll2", t0)
	cur := fixtureSample(t, "poll1", t0.Add(time.Minute))
	cur.uptime = 30
	snap := buildSnapshot("srv", cur, prev, 0)
	if snap.CPU.Total != 0 || snap.Net.RxBps != 0 || snap.Procs[0].CPU != 0 {
		t.Errorf("rates after reboot must be zero: cpu=%v net=%v proc=%v", snap.CPU.Total, snap.Net.RxBps, snap.Procs[0].CPU)
	}
}

func TestCPUUsageCounterAnomalies(t *testing.T) {
	prev := cpuTimes{user: 100, idle: 100, iowait: 50}
	cur := cpuTimes{user: 150, idle: 150, iowait: 40}
	got := cpuUsage(prev, cur)
	if !approx(got.Total, 50) || got.IOWait != 0 {
		t.Errorf("iowait going backwards: %+v", got)
	}
	if got := cpuUsage(cur, cur); got.Total != 0 {
		t.Errorf("no elapsed jiffies: %+v", got)
	}
}

func TestNetUsageCounterReset(t *testing.T) {
	t0 := time.Now()
	prev := &sample{at: t0, uptime: 100, ifaces: map[string]ifaceCounters{
		"eth0": {rx: 1000, tx: 1000}, "eth1": {rx: 5000, tx: 5000},
	}}
	cur := &sample{at: t0.Add(time.Second), uptime: 100, ifaces: map[string]ifaceCounters{
		"eth0": {rx: 2000, tx: 500}, "eth2": {rx: 99999, tx: 99999},
	}}
	n := netUsage(cur, prev)
	if !approx(n.RxBps, 8000) || n.TxBps != 0 {
		t.Errorf("net = %+v, want rx 8000 bit/s from eth0 only (wall-clock fallback), tx 0 after reset", n)
	}
}

func TestProcessSanitizedAndBounded(t *testing.T) {
	t0 := time.Now()
	prev := &sample{at: t0, uptime: 10, cpu: cpuTimes{idle: 0}, cores: []coreTimes{{id: 0}}, clkTck: 100}
	cur := &sample{
		at: t0.Add(time.Second), uptime: 11, clkTck: 100, pageSize: 4096,
		cpu:   cpuTimes{user: 100},
		cores: []coreTimes{{id: 0, times: cpuTimes{user: 100}}},
		mem:   metrics.Memory{Total: 1 << 30},
		procs: []pidStat{
			{pid: 7, uid: 0, comm: "\x1b[31mred\x1b[0m\x07", ticks: 100000, start: 1050},
		},
		users: map[int]string{0: "root"},
	}
	snap := buildSnapshot("x", cur, prev, 0)
	p := snap.Procs[0]
	if p.Command != "red" || p.User != "root" {
		t.Errorf("command not sanitized or user missing: %q %q", p.Command, p.User)
	}
	if p.CPU != 100 {
		t.Errorf("cpu = %v, want clamp at 100%% * 1 core", p.CPU)
	}
}
