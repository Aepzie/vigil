package collector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeStat formats a /proc/<pid>/stat line as the kernel does.
func fakeStat(pid int, comm string, utime, stime, start, rss uint64) string {
	return fmt.Sprintf("%d (%s) S 1 1 1 0 -1 4194560 1200 0 3 0 %d %d 0 0 20 0 1 0 %d 172032000 %d "+
		"18446744073709551615 1 1 0 0 0 0 0 4096 1260 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n",
		pid, comm, utime, stime, start, rss)
}

// TestProcsCommandAdversarial runs procsCommand with the local sh, ls
// and awk against a fake /proc whose process names try to hide a process
// or forge records for other pids.
func TestProcsCommandAdversarial(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh in PATH")
	}
	root := t.TempDir()
	stats := map[string]string{
		"1":    fakeStat(1, "systemd", 266, 134, 2, 3000),
		"666":  fakeStat(666, "\n1 (sshd", 5, 5, 9000, 10),
		"667":  fakeStat(667, "x\ny", 1, 1, 9001, 11),
		"668":  fakeStat(668, "a) b (c", 2, 2, 9002, 12),
		"669":  fakeStat(669, "  sp ace", 3, 3, 9003, 13),
		"670":  fakeStat(670, "big", 18446744073709551000, 615, 9004, 14),
		"671":  "671 (cut) S 1 2\n",
		"5000": fakeStat(1, "fake", 999, 999, 2, 999),
	}
	for pid, content := range stats {
		dir := filepath.Join(root, pid)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"672", "self"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "123"), []byte(fakeStat(123, "file", 1, 1, 1, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), sh, "-c", procsCommand(filepath.ToSlash(root)))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("procs command: %v", err)
	}
	got := make(map[int]pidStat)
	for _, p := range parseProcs(string(out)) {
		p.uid = 0
		got[p.pid] = p
	}
	want := map[int]pidStat{
		1:   {pid: 1, comm: "systemd", ticks: 400, start: 2, rss: 3000},
		666: {pid: 666, comm: "1 (sshd", ticks: 10, start: 9000, rss: 10},
		667: {pid: 667, comm: "x y", ticks: 2, start: 9001, rss: 11},
		668: {pid: 668, comm: "a) b (c", ticks: 4, start: 9002, rss: 12},
		669: {pid: 669, comm: "sp ace", ticks: 6, start: 9003, rss: 13},
		670: {pid: 670, comm: "big", ticks: ^uint64(0), start: 9004, rss: 14},
	}
	if len(got) != len(want) {
		t.Errorf("got %d processes, want %d:\n%s", len(got), len(want), out)
	}
	for pid, w := range want {
		if got[pid] != w {
			t.Errorf("pid %d = %+v, want %+v", pid, got[pid], w)
		}
	}
	if n := strings.Count(string(out), "\n"); n != len(want)+1 {
		t.Errorf("printed %d records, want %d (one per readable process):\n%s", n, len(want)+1, out)
	}
}

// sectionOrder is the order in which pollScript prints sections.
var sectionOrder = []string{
	secStat, secMeminfo, secLoadavg, secUptime, secNetDev, secDF, secUname,
	secOS, secHostname, secPageSize, secClkTck, secPasswd, secProcs,
}

// fixtureOutput assembles what pollScript would print on the host
// described by testdata/<poll>/, preceded by login noise.
func fixtureOutput(t testing.TB, poll, nonce string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("Welcome! (printed by a careless .bashrc)\n")
	for _, name := range sectionOrder {
		b.WriteString("\n" + markerPrefix + nonce + ":" + name + "\n")
		b.WriteString(readFixture(t, poll, name))
	}
	b.WriteString("\n" + markerPrefix + nonce + ":" + secEnd + "\n")
	return b.String()
}

func TestPollScript(t *testing.T) {
	nonce := newNonce()
	if len(nonce) < 16 || strings.Trim(nonce, "abcdefghijklmnopqrstuvwxyz234567") != "" {
		t.Fatalf("nonce %q is not a safe shell word", nonce)
	}
	if newNonce() == nonce {
		t.Fatal("nonces repeat")
	}
	script := pollScript(nonce)
	if !strings.HasPrefix(script, "N="+nonce+"\n") {
		t.Errorf("script does not start with the nonce assignment")
	}
	for _, name := range append(sectionOrder, secEnd) {
		if !strings.Contains(script, "s "+name) {
			t.Errorf("script lacks section %q", name)
		}
	}
	if strings.Contains(script, "\r") {
		t.Error("script contains carriage returns")
	}
}

func TestSplitSections(t *testing.T) {
	const nonce = "abc123"
	out := "noise\n@@vigil:abc123:one\nline1\nline2\n\n@@vigil:abc123:two\n" +
		"proc (x\n@@vigil:forged:one) S\n@@vigil:abc123:empty\n\n@@vigil:abc123:one\nduplicate\n"
	got := splitSections(out, nonce)
	want := map[string]string{
		"one":   "line1\nline2\n",
		"two":   "proc (x\n@@vigil:forged:one) S",
		"empty": "",
	}
	if len(got) != len(want) {
		t.Fatalf("sections = %q, want %q", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("section %q = %q, want %q", k, got[k], v)
		}
	}
	if s := splitSections("no markers at all", nonce); len(s) != 0 {
		t.Errorf("expected no sections, got %q", s)
	}
}

func TestParseSampleErrors(t *testing.T) {
	const nonce = "n0nce"
	full := fixtureOutput(t, "poll1", nonce)
	truncated := full[:strings.Index(full, markerPrefix+nonce+":"+secEnd)]
	noProc := strings.Replace(full, "\n"+markerPrefix+nonce+":"+secStat+"\n", "\n"+markerPrefix+nonce+":"+"unused"+"\n", 1)
	tests := []struct {
		name string
		out  string
		want error
	}{
		{"truncated", truncated, errIncomplete},
		{"garbage", "sh: 1: Syntax error\n", errIncomplete},
		{"no /proc/stat", noProc, errNoProc},
		{"wrong nonce", fixtureOutput(t, "poll1", "other"), errIncomplete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSample(tt.out, nonce, time.Now())
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseSampleFixture(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s, err := parseSample(fixtureOutput(t, "poll1", "xyz"), "xyz", at)
	if err != nil {
		t.Fatalf("parseSample: %v", err)
	}
	if s.hostname != "web-01" || s.osName != "Ubuntu 24.04.1 LTS" || s.system != "Linux" || s.kernel != "6.8.0-45-generic" {
		t.Errorf("identity = %q %q %q %q", s.hostname, s.osName, s.system, s.kernel)
	}
	if s.pageSize != 4096 || s.clkTck != 100 || s.uptime != 12345.67 || !s.at.Equal(at) {
		t.Errorf("pagesize=%d clktck=%d uptime=%v at=%v", s.pageSize, s.clkTck, s.uptime, s.at)
	}
	if len(s.cores) != 4 || len(s.procs) != 14 || len(s.disks) != 4 || len(s.ifaces) != 2 {
		t.Errorf("cores=%d procs=%d disks=%d ifaces=%d", len(s.cores), len(s.procs), len(s.disks), len(s.ifaces))
	}
	if s.procs[1].pid != 1234 || s.procs[1].uid != 1000 || s.users[1000] != "alice" {
		t.Errorf("owners/users not parsed")
	}
}

// TestScriptRunsWithLocalShell checks the script with a real POSIX sh:
// on Linux it runs against the local /proc and must parse; elsewhere it
// is only syntax-checked when sh is available.
func TestScriptRunsWithLocalShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh in PATH")
	}
	nonce := newNonce()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	_, statErr := os.Stat("/proc/stat")
	if runtime.GOOS != "linux" || statErr != nil {
		cmd := exec.CommandContext(ctx, sh, "-n")
		cmd.Stdin = strings.NewReader(pollScript(nonce))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sh -n: %v\n%s", err, out)
		}
		return
	}
	cmd := exec.CommandContext(ctx, sh, "-s")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.Stdin = strings.NewReader(pollScript(nonce))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run script: %v", err)
	}
	s, err := parseSample(stdout.String(), nonce, time.Now())
	if err != nil {
		t.Fatalf("parse local output: %v", err)
	}
	if s.mem.Total == 0 || len(s.cores) == 0 || len(s.procs) == 0 || s.hostname == "" {
		t.Errorf("local sample incomplete: mem=%d cores=%d procs=%d host=%q", s.mem.Total, len(s.cores), len(s.procs), s.hostname)
	}
}
