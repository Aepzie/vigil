package alerts

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/store"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func testThresholds() Thresholds {
	return Thresholds{
		CPUWarn: 80, CPUCrit: 95,
		MemWarn: 85, MemCrit: 95,
		DiskWarn: 85, DiskCrit: 95,
		OfflineGrace: 10 * time.Second,
	}
}

// harness feeds snapshots one second apart to an engine.
type harness struct {
	t   *testing.T
	e   *Engine
	at  time.Time
	seq uint64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, e: NewEngine(testThresholds()), at: t0}
	h.e.now = func() time.Time { return h.at }
	return h
}

func (h *harness) feed(snap metrics.Snapshot) {
	h.t.Helper()
	h.at = h.at.Add(time.Second)
	if snap.ServerID == "" {
		snap.ServerID = "s1"
	}
	snap.At = h.at
	h.e.Evaluate("web-01", snap)
}

func (h *harness) cpu(values ...float64) {
	h.t.Helper()
	for _, v := range values {
		h.feed(online(v, 10))
	}
}

// events returns the events since the previous call.
func (h *harness) events() []Event {
	h.t.Helper()
	evs, latest := h.e.EventsSince(h.seq)
	h.seq = latest
	return evs
}

func (h *harness) expectEvents(want ...string) {
	h.t.Helper()
	var got []string
	for _, ev := range h.events() {
		got = append(got, fmt.Sprintf("%s %s %s", ev.Type, ev.Alert.Kind, ev.Alert.Severity))
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		h.t.Fatalf("events = %q, want %q", got, want)
	}
}

func (h *harness) only() Alert {
	h.t.Helper()
	active := h.e.Active()
	if len(active) != 1 {
		h.t.Fatalf("Active() = %d alerts, want 1: %+v", len(active), active)
	}
	return active[0]
}

func online(cpu, mem float64, disks ...metrics.Disk) metrics.Snapshot {
	return metrics.Snapshot{
		State: metrics.StateOnline,
		CPU:   metrics.CPU{Total: cpu},
		Mem:   metrics.Memory{Total: 1000, Used: uint64(mem * 10)},
		Disks: disks,
	}
}

func disk(mount string, pct float64) metrics.Disk {
	return metrics.Disk{Mount: mount, Total: 1000, Used: uint64(pct * 10)}
}

func offline(reason string) metrics.Snapshot {
	return metrics.Snapshot{State: metrics.StateOffline, Err: reason}
}

func TestCPUSpikeFilter(t *testing.T) {
	h := newHarness(t)
	h.cpu(93, 93)
	h.expectEvents()
	h.cpu(50, 93, 93)
	h.expectEvents()
	h.cpu(93)
	h.expectEvents("fired cpu warn")

	a := h.only()
	if a.Message != "CPU 93% ≥ 80%" || a.Value != 93 || a.Threshold != 80 || a.ServerName != "web-01" || a.ServerID != "s1" {
		t.Errorf("alert = %+v", a)
	}
	if want := fmt.Sprintf("s1:cpu::%d", t0.Add(6*time.Second).UnixNano()); a.ID != want {
		t.Errorf("ID = %q, want %q", a.ID, want)
	}
}

func TestCPUEscalationNeedsConsecutiveSamples(t *testing.T) {
	h := newHarness(t)
	h.cpu(85, 85, 85)
	h.expectEvents("fired cpu warn")
	id := h.only().ID
	h.e.Ack(id)

	h.cpu(97, 97)
	h.expectEvents()
	if h.only().Severity != SeverityWarn {
		t.Fatal("escalated before 3 consecutive samples")
	}
	h.cpu(97)
	h.expectEvents("escalated cpu crit")
	a := h.only()
	if a.ID != id || a.Threshold != 95 || a.Acked || a.Message != "CPU 97% ≥ 95%" {
		t.Errorf("after escalation: %+v", a)
	}
}

func TestCPUFiresDirectlyAsCrit(t *testing.T) {
	h := newHarness(t)
	h.cpu(99, 99, 99)
	h.expectEvents("fired cpu crit")
}

func TestCPUHysteresisAndDeescalation(t *testing.T) {
	h := newHarness(t)
	h.cpu(97, 97, 97)
	h.expectEvents("fired cpu crit")

	h.cpu(91)
	h.expectEvents()
	if a := h.only(); a.Severity != SeverityCrit {
		t.Fatalf("de-escalated inside hysteresis band: %+v", a)
	}
	h.cpu(89.9)
	h.expectEvents()
	a := h.only()
	if a.Severity != SeverityWarn || a.Threshold != 80 || a.Message != "CPU 89% ≥ 80%" {
		t.Fatalf("expected silent de-escalation to warn, got %+v", a)
	}

	h.cpu(96)
	h.expectEvents()
	if h.only().Severity != SeverityWarn {
		t.Fatal("re-escalated without spike filter")
	}

	h.cpu(77)
	if a := h.only(); a.Message != "CPU 77% (threshold 80%)" {
		t.Errorf("band message = %q", a.Message)
	}
	h.cpu(75)
	h.expectEvents()
	h.cpu(74.9)
	h.expectEvents("resolved cpu warn")
	if len(h.e.Active()) != 0 {
		t.Fatal("alert still active")
	}
}

func TestMemoryFiresImmediately(t *testing.T) {
	h := newHarness(t)
	h.feed(online(10, 90))
	h.expectEvents("fired memory warn")
	if a := h.only(); a.Message != "Memory 90% ≥ 85%" {
		t.Errorf("message = %q", a.Message)
	}
	h.feed(online(10, 96))
	h.expectEvents("escalated memory crit")
	h.feed(online(10, 90))
	h.expectEvents()
	h.feed(online(10, 89))
	h.expectEvents()
	if h.only().Severity != SeverityWarn {
		t.Fatal("expected de-escalation below crit-5")
	}
	h.feed(online(10, 80))
	h.expectEvents()
	h.feed(online(10, 79.9))
	h.expectEvents("resolved memory warn")
}

func TestDiskPerMount(t *testing.T) {
	h := newHarness(t)
	h.feed(online(10, 10, disk("/", 40), disk("/var", 97), disk("/data", 88)))
	evs := h.events()
	if len(evs) != 2 || evs[0].Alert.Subject != "/data" || evs[1].Alert.Subject != "/var" {
		t.Fatalf("events = %+v", evs)
	}
	active := h.e.Active()
	if len(active) != 2 || active[0].Subject != "/var" || active[0].Severity != SeverityCrit {
		t.Fatalf("Active() = %+v", active)
	}
	if active[0].Message != "Disk /var 97% ≥ 95%" {
		t.Errorf("message = %q", active[0].Message)
	}
	if !strings.HasPrefix(active[0].ID, "s1:disk:/var:") {
		t.Errorf("ID = %q", active[0].ID)
	}

	h.feed(online(10, 10, disk("/", 40), disk("/var", 97)))
	h.expectEvents("resolved disk warn")
	if a := h.only(); a.Subject != "/var" {
		t.Errorf("remaining = %+v", a)
	}
}

func TestOfflineGrace(t *testing.T) {
	h := newHarness(t)
	h.cpu(10)
	for range 9 {
		h.feed(offline("connection refused"))
	}
	h.feed(metrics.Snapshot{State: metrics.StateConnecting})
	h.expectEvents()
	h.feed(offline("connection refused"))
	h.expectEvents("fired offline crit")
	a := h.only()
	if a.Message != "offline: connection refused" || a.Value != 10 || a.Threshold != 10 {
		t.Errorf("alert = %+v", a)
	}

	h.feed(offline("no route to host"))
	h.expectEvents()
	if a := h.only(); a.Message != "offline: no route to host" || a.ID == "" {
		t.Errorf("updated alert = %+v", a)
	}
	h.cpu(10)
	h.expectEvents("resolved offline crit")
}

func TestOfflineUsesLastSeen(t *testing.T) {
	h := newHarness(t)
	snap := offline("")
	snap.LastSeen = t0.Add(-time.Minute)
	h.feed(snap)
	h.expectEvents("fired offline crit")
	if a := h.only(); a.Message != "offline: unreachable" {
		t.Errorf("message = %q", a.Message)
	}
}

func TestOfflineZeroGraceAndStates(t *testing.T) {
	h := newHarness(t)
	th := testThresholds()
	th.OfflineGrace = 0
	h.e.SetThresholds(th)
	for range 20 {
		h.feed(metrics.Snapshot{State: metrics.StateConnecting})
		h.feed(metrics.Snapshot{State: metrics.StateUnknown})
	}
	h.expectEvents()
	h.feed(offline("connection timed out"))
	h.expectEvents("fired offline crit")
}

func TestOfflineKeepsUtilizationAlerts(t *testing.T) {
	h := newHarness(t)
	hot := online(99, 99, disk("/", 99))
	h.feed(hot)
	h.feed(hot)
	h.feed(hot)
	h.expectEvents("fired memory crit", "fired disk crit", "fired cpu crit")
	for range 15 {
		h.feed(offline("timeout"))
	}
	h.expectEvents("fired offline crit")
	if n := len(h.e.Active()); n != 4 {
		t.Fatalf("Active() = %d, want 4", n)
	}
	h.feed(hot)
	h.expectEvents("resolved offline crit")

	h.cpu(10, 10)
	if n := len(h.e.Active()); n != 0 {
		t.Fatalf("Active() = %d after recovery, want 0", n)
	}
}

func TestOfflineResetsCPUStreak(t *testing.T) {
	h := newHarness(t)
	h.cpu(90, 90)
	h.feed(offline("x"))
	h.cpu(90, 90)
	h.expectEvents()
	h.cpu(90)
	h.expectEvents("fired cpu warn")
}

func TestNewOccurrenceGetsNewID(t *testing.T) {
	h := newHarness(t)
	h.feed(online(10, 90))
	first := h.only().ID
	h.feed(online(10, 10))
	h.feed(online(10, 90))
	if second := h.only().ID; second == first {
		t.Errorf("IDs equal: %q", first)
	}

	e := NewEngine(testThresholds())
	snap := online(10, 90)
	snap.ServerID, snap.At = "x", t0
	e.Evaluate("x", snap)
	snap.Mem.Used = 0
	e.Evaluate("x", snap)
	snap.Mem.Used = 900
	e.Evaluate("x", snap)
	recent := e.Recent(10)
	if len(recent) != 2 || recent[0].ID == recent[1].ID {
		t.Errorf("same-instant occurrences share an ID: %+v", recent)
	}
}

func TestActiveOrdering(t *testing.T) {
	h := newHarness(t)
	h.feed(metrics.Snapshot{ServerID: "a", State: metrics.StateOnline, Mem: metrics.Memory{Total: 100, Used: 90}})
	h.feed(metrics.Snapshot{ServerID: "b", State: metrics.StateOnline, Mem: metrics.Memory{Total: 100, Used: 99}})
	h.feed(metrics.Snapshot{ServerID: "c", State: metrics.StateOnline, Mem: metrics.Memory{Total: 100, Used: 88}})
	h.feed(metrics.Snapshot{ServerID: "d", State: metrics.StateOnline, Mem: metrics.Memory{Total: 100, Used: 97}})
	var got []string
	for _, a := range h.e.Active() {
		got = append(got, a.ServerID)
	}
	if strings.Join(got, "") != "dbca" {
		t.Errorf("order = %v, want d b c a", got)
	}
}

func TestRecentAndAck(t *testing.T) {
	h := newHarness(t)
	for i := range MaxResolved + 10 {
		h.feed(metrics.Snapshot{ServerID: fmt.Sprint("s", i), State: metrics.StateOnline, Mem: metrics.Memory{Total: 1, Used: 1}})
		h.feed(metrics.Snapshot{ServerID: fmt.Sprint("s", i), State: metrics.StateOnline, Mem: metrics.Memory{Total: 1}})
	}
	h.feed(online(10, 99))

	if got := h.e.Recent(0); got != nil {
		t.Errorf("Recent(0) = %v", got)
	}
	all := h.e.Recent(1000)
	if len(all) != MaxResolved+1 {
		t.Fatalf("len(Recent) = %d, want %d", len(all), MaxResolved+1)
	}
	if all[0].Resolved || all[0].ServerID != "s1" {
		t.Errorf("newest = %+v, want the active alert", all[0])
	}
	if !all[1].Resolved || all[1].ServerID != fmt.Sprint("s", MaxResolved+9) {
		t.Errorf("second = %+v", all[1])
	}
	for i := 1; i < len(all); i++ {
		if recencyOf(all[i]).After(recencyOf(all[i-1])) {
			t.Fatalf("Recent not newest first at %d", i)
		}
	}
	if got := h.e.Recent(3); len(got) != 3 || got[0].ID != all[0].ID {
		t.Errorf("Recent(3) = %+v", got)
	}

	h.e.Ack(all[0].ID)
	h.e.Ack(all[1].ID)
	h.e.Ack("unknown")
	if !h.only().Acked {
		t.Error("active alert not acked")
	}
	if r := h.e.Recent(2); !r[1].Acked {
		t.Error("resolved alert not acked")
	}
}

func TestEventLog(t *testing.T) {
	h := newHarness(t)
	if evs, seq := h.e.EventsSince(0); evs != nil || seq != 0 {
		t.Fatalf("empty log = %v, %d", evs, seq)
	}
	const toggles = 300
	for range toggles {
		h.feed(online(10, 99))
		h.feed(online(10, 10))
	}
	latest := h.e.LatestSeq()
	if latest != 2*toggles {
		t.Fatalf("LatestSeq = %d, want %d", latest, 2*toggles)
	}

	evs, seq := h.e.EventsSince(0)
	if seq != latest || len(evs) != MaxEvents {
		t.Fatalf("EventsSince(0) = %d events, seq %d", len(evs), seq)
	}
	if evs[0].Seq != latest-MaxEvents+1 || evs[len(evs)-1].Seq != latest {
		t.Errorf("range = %d..%d", evs[0].Seq, evs[len(evs)-1].Seq)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq != evs[i-1].Seq+1 {
			t.Fatalf("gap at %d", i)
		}
	}

	evs, _ = h.e.EventsSince(latest - 2)
	if len(evs) != 2 || evs[0].Type != EventFired || evs[1].Type != EventResolved || !evs[1].Alert.Resolved {
		t.Errorf("tail = %+v", evs)
	}
	if evs, seq := h.e.EventsSince(latest + 5); evs != nil || seq != latest {
		t.Errorf("future seq = %v, %d", evs, seq)
	}
}

func TestForget(t *testing.T) {
	h := newHarness(t)
	h.feed(online(10, 99, disk("/", 99)))
	h.feed(online(90, 99, disk("/", 99)))
	h.feed(online(90, 99, disk("/", 99)))
	h.feed(metrics.Snapshot{ServerID: "other", State: metrics.StateOnline, Mem: metrics.Memory{Total: 1, Used: 1}})
	h.events()

	h.e.Forget("s1")
	h.e.Forget("missing")
	h.expectEvents("resolved memory crit", "resolved disk crit")
	if a := h.only(); a.ServerID != "other" {
		t.Errorf("remaining = %+v", a)
	}
	h.cpu(90)
	h.expectEvents()
}

func TestRenameUpdatesActiveAlerts(t *testing.T) {
	e := NewEngine(testThresholds())
	snap := online(10, 99)
	snap.ServerID, snap.At = "s1", t0
	e.Evaluate("old", snap)
	snap.At = t0.Add(time.Second)
	e.Evaluate("new", snap)
	if a := e.Active(); len(a) != 1 || a[0].ServerName != "new" {
		t.Errorf("Active() = %+v", a)
	}
	snap.At = t0.Add(2 * time.Second)
	e.Evaluate("", snap)
	if a := e.Active(); a[0].ServerName != "new" {
		t.Errorf("empty name overwrote: %+v", a)
	}
}

func TestIgnoresSnapshotsWithoutID(t *testing.T) {
	e := NewEngine(testThresholds())
	e.Evaluate("x", online(99, 99))
	if e.LatestSeq() != 0 {
		t.Error("snapshot without ServerID produced events")
	}
}

func TestZeroAtUsesClock(t *testing.T) {
	e := NewEngine(testThresholds())
	e.now = func() time.Time { return t0 }
	e.Evaluate("x", metrics.Snapshot{ServerID: "x", State: metrics.StateOnline, Mem: metrics.Memory{Total: 1, Used: 1}})
	if a := e.Active(); len(a) != 1 || !a[0].Since.Equal(t0) {
		t.Errorf("Active() = %+v", a)
	}
}

func TestDisabledThresholds(t *testing.T) {
	e := NewEngine(Thresholds{})
	for i := range 10 {
		snap := online(100, 100, disk("/", 100))
		snap.ServerID, snap.At = "x", t0.Add(time.Duration(i)*time.Second)
		e.Evaluate("x", snap)
	}
	if n := len(e.Active()); n != 0 {
		t.Errorf("Active() = %d with disabled thresholds", n)
	}
	var zero Engine
	zero.Evaluate("x", metrics.Snapshot{ServerID: "x", State: metrics.StateOffline})
	if len(zero.Active()) != 1 {
		t.Error("zero Engine should work and fire offline with zero grace")
	}
}

func TestTargetLevel(t *testing.T) {
	tests := []struct {
		cur               level
		value, warn, crit float64
		want              level
	}{
		{levelNone, 79.9, 80, 95, levelNone},
		{levelNone, 80, 80, 95, levelWarn},
		{levelNone, 95, 80, 95, levelCrit},
		{levelWarn, 75, 80, 95, levelWarn},
		{levelWarn, 74.99, 80, 95, levelNone},
		{levelCrit, 90, 80, 95, levelCrit},
		{levelCrit, 89.99, 80, 95, levelWarn},
		{levelCrit, 74, 80, 95, levelNone},
		{levelWarn, 2, 4, 10, levelWarn},
		{levelWarn, 1.9, 4, 10, levelNone},
		{levelNone, 100, 0, 0, levelNone},
		{levelNone, 50, 40, 0, levelWarn},
	}
	for _, tt := range tests {
		if got := targetLevel(tt.cur, tt.value, tt.warn, tt.crit); got != tt.want {
			t.Errorf("targetLevel(%d, %v, %v, %v) = %d, want %d", tt.cur, tt.value, tt.warn, tt.crit, got, tt.want)
		}
	}
}

func TestUtilMessage(t *testing.T) {
	tests := []struct {
		key        alertKey
		value, thr float64
		want       string
	}{
		{alertKey{kind: KindCPU}, 93.7, 80, "CPU 93% ≥ 80%"},
		{alertKey{kind: KindCPU}, 94.99, 95, "CPU 94% (threshold 95%)"},
		{alertKey{kind: KindMem}, 85, 85, "Memory 85% ≥ 85%"},
		{alertKey{kind: KindDisk, subject: "/var"}, 97.2, 95, "Disk /var 97% ≥ 95%"},
		{alertKey{kind: KindDisk, subject: "/"}, 80.56, 80.5, "Disk / 80.5% ≥ 80.5%"},
	}
	for _, tt := range tests {
		if got := utilMessage(tt.key, tt.value, tt.thr); got != tt.want {
			t.Errorf("utilMessage = %q, want %q", got, tt.want)
		}
	}
}

func TestThresholdsFromSettings(t *testing.T) {
	set := store.DefaultSettings()
	th := ThresholdsFromSettings(set)
	if th.CPUWarn != set.CPUWarn || th.DiskCrit != set.DiskCrit || th.OfflineGrace != set.OfflineGrace {
		t.Errorf("ThresholdsFromSettings = %+v", th)
	}
	e := NewEngine(th)
	th.CPUWarn = 1
	e.SetThresholds(th)
	if e.Thresholds().CPUWarn != 1 {
		t.Error("SetThresholds not applied")
	}
}

func TestStringers(t *testing.T) {
	if KindMem.String() != "memory" || SeverityCrit.String() != "crit" || EventEscalated.String() != "escalated" {
		t.Error("unexpected String output")
	}
}

func TestConcurrentUse(t *testing.T) {
	e := NewEngine(testThresholds())
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			id := fmt.Sprint("srv", w)
			for i := range 500 {
				snap := online(float64(i%100), float64((i*7)%100), disk("/", float64((i*13)%100)))
				snap.ServerID, snap.At = id, t0.Add(time.Duration(i)*time.Second)
				if i%50 == 0 {
					snap.State = metrics.StateOffline
				}
				e.Evaluate(id, snap)
			}
			e.Forget(id)
		})
	}
	for range 2 {
		wg.Go(func() {
			var seq uint64
			for range 500 {
				var evs []Event
				evs, seq = e.EventsSince(seq)
				for _, ev := range evs {
					e.Ack(ev.Alert.ID)
				}
				_ = e.Active()
				_ = e.Recent(20)
			}
		})
	}
	wg.Wait()
	if n := len(e.Active()); n != 0 {
		t.Errorf("Active() = %d after Forget, want 0", n)
	}
}
