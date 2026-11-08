package demo

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/collector"
	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/store"
)

var _ collector.Source = (*Source)(nil)

var fixedNow = time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC)

func seededStore(t testing.TB) *store.Store {
	t.Helper()
	st := store.NewMemory()
	if err := SeedServers(st); err != nil {
		t.Fatalf("SeedServers: %v", err)
	}
	return st
}

func newFixed(st *store.Store, eng *alerts.Engine, seed int64) *Source {
	src := New(st, eng, seed)
	src.now = func() time.Time { return fixedNow }
	return src
}

func byName(t testing.TB, st *store.Store, name string) store.Server {
	t.Helper()
	for _, srv := range st.Servers() {
		if srv.Name == name {
			return srv
		}
	}
	t.Fatalf("server %q not found", name)
	return store.Server{}
}

func TestSeedServers(t *testing.T) {
	st := seededStore(t)
	servers := st.Servers()
	if len(servers) != len(sampleServers) {
		t.Fatalf("seeded %d servers, want %d", len(servers), len(sampleServers))
	}
	for _, srv := range servers {
		if _, ok := knownProfiles[srv.Name]; !ok {
			t.Errorf("seeded server %q has no profile", srv.Name)
		}
		if len(srv.Tags) == 0 {
			t.Errorf("%s has no tags", srv.Name)
		}
	}
	v := st.Version()
	if err := SeedServers(st); err != nil || st.Version() != v {
		t.Errorf("second SeedServers changed the store: %v", err)
	}
	if err := SeedServers(nil); err == nil {
		t.Error("SeedServers(nil) succeeded")
	}
	if sampleServers[0].Tags[0] != "prod" {
		t.Error("SeedServers mutated the sample catalog")
	}
}

func TestDeterministic(t *testing.T) {
	st := seededStore(t)
	a, b, c := newFixed(st, nil, 42), newFixed(st, nil, 42), newFixed(st, nil, 7)
	a.Prime(120)
	b.Prime(120)
	c.Prime(120)

	differs := false
	for _, srv := range st.Servers() {
		sa, _ := a.Snapshot(srv.ID)
		sb, _ := b.Snapshot(srv.ID)
		sc, _ := c.Snapshot(srv.ID)
		if !reflect.DeepEqual(sa, sb) {
			t.Errorf("%s: snapshots differ for the same seed", srv.Name)
		}
		ha, _ := a.History(srv.ID)
		hb, _ := b.History(srv.ID)
		if !reflect.DeepEqual(ha, hb) {
			t.Errorf("%s: histories differ for the same seed", srv.Name)
		}
		if !reflect.DeepEqual(sa, sc) {
			differs = true
		}
	}
	if !differs {
		t.Error("different seeds produced identical output")
	}
}

func TestSnapshotShapes(t *testing.T) {
	st := seededStore(t)
	src := newFixed(st, nil, 1)
	src.Prime(300)

	for _, srv := range st.Servers() {
		snap, ok := src.Snapshot(srv.ID)
		if !ok {
			t.Fatalf("%s: no snapshot", srv.Name)
		}
		if snap.ServerID != srv.ID || !snap.At.Equal(fixedNow) || snap.Hostname == "" || snap.OS == "" || snap.Kernel == "" {
			t.Errorf("%s: header fields %+v", srv.Name, snap)
		}
		if snap.CPUCount < 2 || snap.CPUCount > 32 {
			t.Errorf("%s: CPUCount %d", srv.Name, snap.CPUCount)
		}
		hist, ok := src.History(srv.ID)
		if !ok || len(hist.CPU) != 300 || len(hist.Latency) != 300 || hist.Step != time.Second {
			t.Errorf("%s: history len %d step %v", srv.Name, len(hist.CPU), hist.Step)
		}

		if srv.Name == "backup-nas" {
			if snap.State != metrics.StateOffline || snap.Err != "connection timed out" || snap.LastSeen.IsZero() || !snap.LastSeen.Before(snap.At) {
				t.Errorf("backup-nas: %+v", snap)
			}
			continue
		}
		if snap.State != metrics.StateOnline {
			t.Errorf("%s: state %v", srv.Name, snap.State)
		}
		if len(snap.CPU.Cores) != snap.CPUCount {
			t.Errorf("%s: %d cores, CPUCount %d", srv.Name, len(snap.CPU.Cores), snap.CPUCount)
		}
		if c := snap.CPU; c.Total < 0 || c.Total > 100 || c.User < 0 || c.User+c.System+c.IOWait+c.Steal > c.Total+1e-9 {
			t.Errorf("%s: cpu %+v", srv.Name, c)
		}
		for _, core := range snap.CPU.Cores {
			if core < 0 || core > 100 {
				t.Errorf("%s: core %v", srv.Name, core)
			}
		}
		if m := snap.Mem; m.Total < 4*gib || m.Total > 128*gib || m.Used > m.Total || m.Used+m.Available != m.Total || m.SwapUsed > m.SwapTotal {
			t.Errorf("%s: mem %+v", srv.Name, m)
		}
		if len(snap.Disks) == 0 {
			t.Errorf("%s: no disks", srv.Name)
		}
		for _, d := range snap.Disks {
			if d.Used > d.Total || d.Mount == "" || d.FSType == "" {
				t.Errorf("%s: disk %+v", srv.Name, d)
			}
		}
		if snap.Latency < 200*time.Microsecond || snap.Latency > 160*time.Millisecond {
			t.Errorf("%s: latency %v", srv.Name, snap.Latency)
		}
		if snap.Net.RxBps <= 0 || snap.Net.TxBps <= 0 || snap.Net.RxBytes == 0 {
			t.Errorf("%s: net %+v", srv.Name, snap.Net)
		}
		if snap.Uptime < 24*time.Hour || snap.Load.One <= 0 {
			t.Errorf("%s: uptime %v load %+v", srv.Name, snap.Uptime, snap.Load)
		}
		if len(snap.Procs) < 4 || !slices.IsSortedFunc(snap.Procs, func(a, b metrics.Process) int {
			switch {
			case a.CPU > b.CPU:
				return -1
			case a.CPU < b.CPU:
				return 1
			}
			return 0
		}) {
			t.Errorf("%s: procs not sorted by CPU: %+v", srv.Name, snap.Procs)
		}
		for _, cpu := range hist.CPU {
			if cpu < 0 || cpu > 100 {
				t.Fatalf("%s: history cpu %v", srv.Name, cpu)
			}
		}
	}

	hot, _ := src.History(byName(t, st, "ci-runner").ID)
	for _, cpu := range hot.CPU {
		if cpu < 80 || cpu >= 95 {
			t.Fatalf("ci-runner cpu %v outside the warn band", cpu)
		}
	}
	replica, _ := src.Snapshot(byName(t, st, "db-replica").ID)
	if pct, mount := replica.MaxDiskPct(); mount != "/data" || pct < 95 || pct > 97 {
		t.Errorf("db-replica max disk %v%% on %q", pct, mount)
	}
}

func TestValuesEvolveSmoothly(t *testing.T) {
	st := seededStore(t)
	src := newFixed(st, nil, 3)
	src.Prime(600)
	hist, _ := src.History(byName(t, st, "db-primary").ID)
	maxJump, distinct := 0.0, map[float64]bool{}
	for i := 1; i < len(hist.Mem); i++ {
		maxJump = max(maxJump, abs(hist.Mem[i]-hist.Mem[i-1]))
		distinct[hist.CPU[i]] = true
	}
	if maxJump > 2 {
		t.Errorf("memory jumps by %.2f points between samples", maxJump)
	}
	if len(distinct) < 500 {
		t.Errorf("cpu has only %d distinct values", len(distinct))
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestPrimeTriggersDemoAlerts(t *testing.T) {
	st := seededStore(t)
	eng := alerts.NewEngine(alerts.ThresholdsFromSettings(st.Settings()))
	src := newFixed(st, eng, 99)
	src.Prime(300)

	type want struct {
		kind alerts.Kind
		sev  alerts.Severity
	}
	found := map[string]want{}
	for _, a := range eng.Active() {
		found[a.ServerName] = want{a.Kind, a.Severity}
	}
	expect := map[string]want{
		"backup-nas": {alerts.KindOffline, alerts.SeverityCrit},
		"db-replica": {alerts.KindDisk, alerts.SeverityCrit},
		"ci-runner":  {alerts.KindCPU, alerts.SeverityWarn},
	}
	for name, w := range expect {
		if got, ok := found[name]; !ok || got != w {
			t.Errorf("%s: alert %+v (present %v), want %+v; active: %+v", name, got, ok, w, eng.Active())
		}
	}
}

func TestPrimeIsFast(t *testing.T) {
	st := seededStore(t)
	eng := alerts.NewEngine(alerts.ThresholdsFromSettings(st.Settings()))
	src := New(st, eng, 1)
	start := time.Now()
	src.Prime(300)
	elapsed := time.Since(start)
	t.Logf("Prime(300) for %d servers took %v", len(st.Servers()), elapsed)
	if elapsed > 500*time.Millisecond {
		t.Errorf("Prime(300) took %v", elapsed)
	}
}

func BenchmarkPrime300(b *testing.B) {
	st := seededStore(b)
	for b.Loop() {
		eng := alerts.NewEngine(alerts.ThresholdsFromSettings(st.Settings()))
		New(st, eng, 1).Prime(300)
	}
}

func TestPrimeCapsAtCapacity(t *testing.T) {
	st := seededStore(t)
	src := newFixed(st, nil, 1)
	src.Prime(0)
	if _, ok := src.Snapshot(byName(t, st, "web-01").ID); ok {
		t.Error("Prime(0) generated samples")
	}
	src.Prime(5000)
	hist, _ := src.History(byName(t, st, "web-01").ID)
	if len(hist.CPU) != HistoryCapacity {
		t.Errorf("history len %d, want %d", len(hist.CPU), HistoryCapacity)
	}
}

func TestFollowsStoreChanges(t *testing.T) {
	st := seededStore(t)
	eng := alerts.NewEngine(alerts.ThresholdsFromSettings(st.Settings()))
	src := newFixed(st, eng, 5)
	src.Prime(30)

	added, err := st.Add(store.Server{Name: "edge-07", Host: "edge-07.example.internal", User: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := src.Snapshot(added.ID); ok {
		t.Error("snapshot before the next sample")
	}
	src.Prime(1)
	snap, ok := src.Snapshot(added.ID)
	if !ok || snap.State != metrics.StateOnline || snap.Hostname != "edge-07" {
		t.Errorf("new server snapshot %+v, %v", snap, ok)
	}

	nas := byName(t, st, "backup-nas")
	nas.Name = "nas-old"
	if err := st.Update(nas); err != nil {
		t.Fatal(err)
	}
	src.Prime(1)
	if a := eng.Active(); !slices.ContainsFunc(a, func(a alerts.Alert) bool { return a.ServerName == "nas-old" }) {
		t.Errorf("rename not propagated to alerts: %+v", a)
	}

	if err := st.Delete(nas.ID); err != nil {
		t.Fatal(err)
	}
	src.Prime(1)
	if _, ok := src.Snapshot(nas.ID); ok {
		t.Error("deleted server still has a snapshot")
	}
	if _, ok := src.History(nas.ID); ok {
		t.Error("deleted server still has history")
	}
	if slices.ContainsFunc(eng.Active(), func(a alerts.Alert) bool { return a.ServerID == nas.ID }) {
		t.Error("alerts of deleted server still active")
	}
}

func TestSnapshotReturnsCopy(t *testing.T) {
	st := seededStore(t)
	src := newFixed(st, nil, 1)
	src.Prime(1)
	id := byName(t, st, "web-01").ID
	snap, _ := src.Snapshot(id)
	snap.CPU.Cores[0] = -1
	snap.Disks[0].Mount = "x"
	snap.Procs[0].Command = "x"
	again, _ := src.Snapshot(id)
	if again.CPU.Cores[0] == -1 || again.Disks[0].Mount == "x" || again.Procs[0].Command == "x" {
		t.Error("Snapshot returned shared slices")
	}
	if _, ok := src.Snapshot("missing"); ok {
		t.Error("Snapshot(missing) ok")
	}
	if h, ok := src.History("missing"); ok || h.Step != Step {
		t.Error("History(missing) ok")
	}
}

func TestNilStore(t *testing.T) {
	src := New(nil, nil, 1)
	src.Prime(10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src.Run(ctx)
}

func TestRunAndConcurrentReads(t *testing.T) {
	st := seededStore(t)
	eng := alerts.NewEngine(alerts.ThresholdsFromSettings(st.Settings()))
	src := New(st, eng, 11)
	ids := make([]string, 0)
	for _, srv := range st.Servers() {
		ids = append(ids, srv.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { src.Run(ctx) })
	wg.Go(func() {
		for ctx.Err() == nil {
			for _, id := range ids {
				_, _ = src.Snapshot(id)
				_, _ = src.History(id)
			}
			_ = eng.Active()
			src.PollNow(ids[0])
		}
	})
	wg.Go(func() {
		for i := range 5 {
			srv, err := st.Add(store.Server{Name: "tmp-" + string(rune('a'+i)), Host: "tmp.example.internal", User: "ops"})
			if err != nil {
				t.Errorf("Add: %v", err)
				return
			}
			time.Sleep(50 * time.Millisecond)
			_ = st.Delete(srv.ID)
		}
	})
	wg.Wait()

	hist, ok := src.History(ids[0])
	if !ok || len(hist.CPU) < 2 {
		t.Errorf("Run produced %d samples, want at least 2", len(hist.CPU))
	}
}
