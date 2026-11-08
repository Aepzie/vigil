package screens

import (
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

// testNow is the fixed clock of every fake fleet.
var testNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK9vZ2ZsZWV0bWFuYWdlcmtleWZvcnRlc3RzMDEyMzQ1Njc4 vigil@manager"

// testDeps returns dependencies backed by a store in a temp dir.
func testDeps(t *testing.T) *ui.Deps {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return &ui.Deps{
		Theme:      theme.Default(),
		Store:      st,
		Alerts:     alerts.NewEngine(alerts.Thresholds{CPUWarn: 80, CPUCrit: 95}),
		User:       "admin",
		Version:    "v0.1.0",
		StartedAt:  testNow.Add(-3 * time.Hour),
		ManagerKey: testKey,
		DataDir:    "/var/lib/vigil",
		Listen:     ":2222",
		Term:       "xterm-256color",
	}
}

// wave returns n deterministic values oscillating around base.
func wave(n int, base, amp float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = base + amp*float64((i*7)%11-5)/5
	}
	return out
}

func series(n int, cpu, mem float64) metrics.Series {
	return metrics.Series{
		Step:    time.Second,
		CPU:     wave(n, cpu, 8),
		Mem:     wave(n, mem, 2),
		Swap:    wave(n, 3, 1),
		RxBps:   wave(n, 4e6, 1e6),
		TxBps:   wave(n, 1e6, 3e5),
		Latency: wave(n, 12, 3),
		Load1:   wave(n, 0.8, 0.2),
	}
}

func onlineSnap(id string, cpu float64, cores int, memUsedPct float64) metrics.Snapshot {
	coreVals := make([]float64, cores)
	for i := range coreVals {
		coreVals[i] = float64((i*37)%100) * cpu / 100
	}
	const gib = 1 << 30
	return metrics.Snapshot{
		ServerID: id,
		State:    metrics.StateOnline,
		At:       testNow.Add(-time.Second),
		LastSeen: testNow.Add(-time.Second),
		Latency:  12 * time.Millisecond,
		Hostname: id + ".internal",
		OS:       "Ubuntu 24.04.1 LTS",
		Kernel:   "6.8.0-45-generic",
		Uptime:   (49*time.Hour + 12*time.Minute),
		CPUCount: cores,
		CPU:      metrics.CPU{Total: cpu, User: cpu * 0.7, System: cpu * 0.25, IOWait: 0.4, Cores: coreVals},
		Load:     metrics.Load{One: 0.42, Five: 0.38, Fifteen: 0.31},
		Mem: metrics.Memory{
			Total: 16 * gib, Used: uint64(memUsedPct / 100 * 16 * gib), Cached: 3 * gib,
			SwapTotal: 2 * gib, SwapUsed: gib / 10,
		},
		Disks: []metrics.Disk{
			{Device: "/dev/sda1", Mount: "/", FSType: "ext4", Total: 100 * gib, Used: 61 * gib},
			{Device: "/dev/sdb1", Mount: "/var/lib/postgresql", FSType: "xfs", Total: 500 * gib, Used: 455 * gib},
			{Device: "/dev/sda2", Mount: "/boot", FSType: "ext4", Total: gib, Used: gib / 4},
		},
		Net: metrics.Net{RxBps: 12.4e6, TxBps: 3.1e6},
		Procs: []metrics.Process{
			{PID: 1312, User: "postgres", Command: "postgres: checkpointer", CPU: 12.5, MemPct: 4.2, RSS: 512 << 20},
			{PID: 88, User: "root", Command: "/usr/bin/containerd", CPU: 33.1, MemPct: 1.1, RSS: 128 << 20},
			{PID: 4021, User: "www", Command: "nginx: worker process", CPU: 2.0, MemPct: 0.3, RSS: 24 << 20},
		},
	}
}

// fakeFleet is a populated fleet: two online servers (one critical), one
// offline, one never polled; two active alerts and one resolved alert.
func fakeFleet() ui.Fleet {
	servers := []store.Server{
		{ID: "s1", Name: "web-01", Host: "10.0.0.11", Port: 22, User: "deploy", Tags: []string{"prod", "web"}, CreatedAt: testNow.Add(-48 * time.Hour)},
		{ID: "s2", Name: "db-01", Host: "10.0.0.21", Port: 2222, User: "root", KeyPath: "/keys/db", Tags: []string{"prod", "db"}, CreatedAt: testNow.Add(-24 * time.Hour)},
		{ID: "s3", Name: "edge-03", Host: "edge.example.com", Port: 22, User: "ops", CreatedAt: testNow.Add(-time.Hour)},
		{ID: "s4", Name: "new-04", Host: "2001:db8::4", Port: 22, User: "ops"},
	}
	return ui.Fleet{
		Now:     testNow,
		Servers: servers,
		Snaps: map[string]metrics.Snapshot{
			"s1": onlineSnap("s1", 23.5, 4, 41),
			"s2": onlineSnap("s2", 97.2, 32, 88),
			"s3": {ServerID: "s3", State: metrics.StateOffline, Err: "dial tcp: connection refused", At: testNow, LastSeen: testNow.Add(-5 * time.Minute)},
		},
		History: map[string]metrics.Series{
			"s1": series(300, 23, 41),
			"s2": series(300, 90, 88),
			"s3": series(300, 0, 0),
		},
		Active: []alerts.Alert{
			{ID: "a1", ServerID: "s2", ServerName: "db-01", Kind: alerts.KindCPU, Severity: alerts.SeverityCrit, Message: "CPU at 97% for 2m", Value: 97.2, Threshold: 95, Since: testNow.Add(-192 * time.Second)},
			{ID: "a2", ServerID: "s3", ServerName: "edge-03", Kind: alerts.KindOffline, Severity: alerts.SeverityCrit, Message: "unreachable: connection refused", Since: testNow.Add(-4 * time.Minute), Acked: true},
		},
		Recent: []alerts.Alert{
			{ID: "a1", ServerID: "s2", ServerName: "db-01", Kind: alerts.KindCPU, Severity: alerts.SeverityCrit, Since: testNow.Add(-192 * time.Second)},
			{ID: "a0", ServerID: "s1", ServerName: "web-01", Kind: alerts.KindDisk, Subject: "/", Severity: alerts.SeverityWarn, Message: "disk / at 86%", Since: testNow.Add(-time.Hour), Resolved: true, ResolvedAt: testNow.Add(-50 * time.Minute)},
		},
		Settings: store.DefaultSettings(),
	}
}

// emptyFleet has no servers and no alerts.
func emptyFleet() ui.Fleet {
	return ui.Fleet{Now: testNow, Settings: store.DefaultSettings()}
}

// press builds a key press from its String() form.
func press(name string) tea.KeyPressMsg {
	special := map[string]tea.KeyPressMsg{
		"enter":     {Code: tea.KeyEnter},
		"esc":       {Code: tea.KeyEscape},
		"tab":       {Code: tea.KeyTab},
		"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
		"backspace": {Code: tea.KeyBackspace},
		"up":        {Code: tea.KeyUp},
		"down":      {Code: tea.KeyDown},
		"left":      {Code: tea.KeyLeft},
		"right":     {Code: tea.KeyRight},
		"space":     {Code: tea.KeySpace, Text: " "},
		"delete":    {Code: tea.KeyDelete},
		"ctrl+s":    {Code: 's', Mod: tea.ModCtrl},
		"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
		"ctrl+w":    {Code: 'w', Mod: tea.ModCtrl},
	}
	if k, ok := special[name]; ok {
		return k
	}
	r := []rune(name)[0]
	k := tea.KeyPressMsg{Code: unicode.ToLower(r), Text: name}
	if unicode.IsUpper(r) {
		k.Mod = tea.ModShift
	}
	return k
}

// typeText sends each rune of s as a key press.
func typeText(scr ui.Screen, s string) ui.Screen {
	for _, r := range s {
		scr, _ = scr.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return scr
}

// send delivers a key press and returns the screen and its command.
func send(scr ui.Screen, key string) (ui.Screen, tea.Cmd) {
	return scr.Update(press(key))
}

// collect runs cmd and returns every message it produces, flattening
// batches.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// find returns the first message of type T.
func find[T any](msgs []tea.Msg) (T, bool) {
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// checkSize fails unless block is exactly w×h cells.
func checkSize(t *testing.T, block string, w, h int) {
	t.Helper()
	if w <= 0 || h <= 0 {
		if block != "" {
			t.Fatalf("want empty output for %d×%d, got %q", w, h, block)
		}
		return
	}
	lines := strings.Split(block, "\n")
	if len(lines) != h {
		t.Fatalf("want %d lines, got %d", h, len(lines))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("line %d: want width %d, got %d: %q", i, w, got, ansi.Strip(l))
		}
	}
}
