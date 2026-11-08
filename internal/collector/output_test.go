package collector

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// withExtraProcs returns fixture output for poll whose procs section
// starts with n additional process records.
func withExtraProcs(t testing.TB, poll, nonce string, n int) string {
	t.Helper()
	var extra strings.Builder
	for i := range n {
		pid := 100000 + i
		fmt.Fprintf(&extra, "%d 1000 %d 12345678 87654321 123456789 987654 worker-%d\n", pid, pid, i)
	}
	header := markerPrefix + nonce + ":" + secProcs + "\n"
	return strings.Replace(fixtureOutput(t, poll, nonce), header, header+extra.String(), 1)
}

func TestReadOutputPassesFixture(t *testing.T) {
	const nonce = "n"
	out, err := readOutput(strings.NewReader(fixtureOutput(t, "poll2", nonce)), nonce)
	if err != nil || out.procsTruncated {
		t.Fatalf("readOutput = %v, truncated %v", err, out.procsTruncated)
	}
	s, err := parseSample(out.text, nonce, time.Now())
	if err != nil {
		t.Fatalf("parseSample: %v", err)
	}
	want, _ := parseSample(fixtureOutput(t, "poll2", nonce), nonce, time.Now())
	if len(s.procs) != len(want.procs) || len(s.disks) != len(want.disks) || s.mem != want.mem {
		t.Errorf("read output differs: procs %d/%d disks %d/%d", len(s.procs), len(want.procs), len(s.disks), len(want.disks))
	}
}

func TestReadOutputCapsProcesses(t *testing.T) {
	const nonce = "n"
	raw := withExtraProcs(t, "poll1", nonce, maxProcRecords+40000)
	if len(raw) <= maxOutput {
		t.Fatalf("test output is only %d bytes, want more than maxOutput", len(raw))
	}
	out, err := readOutput(iotest.HalfReader(strings.NewReader(raw)), nonce)
	if err != nil {
		t.Fatalf("readOutput: %v", err)
	}
	if !out.procsTruncated {
		t.Error("process list not marked truncated")
	}
	s, err := parseSample(out.text, nonce, time.Now())
	if err != nil {
		t.Fatalf("parseSample: %v", err)
	}
	if len(s.procs) != maxProcRecords || s.procs[0].pid != 100000 || s.procs[len(s.procs)-1].pid != 100000+maxProcRecords-1 {
		t.Errorf("kept %d processes (%d..%d), want the first %d", len(s.procs), s.procs[0].pid, s.procs[len(s.procs)-1].pid, maxProcRecords)
	}
	if s.mem.Total == 0 || len(s.disks) != 4 || len(s.cores) != 4 || s.hostname != "web-01" || s.users[1000] != "alice" {
		t.Errorf("other sections lost: mem=%d disks=%d cores=%d host=%q", s.mem.Total, len(s.disks), len(s.cores), s.hostname)
	}
}

func TestReadOutputDropsOverlongRecords(t *testing.T) {
	const nonce = "n"
	header := markerPrefix + nonce + ":" + secProcs + "\n"
	raw := strings.Replace(fixtureOutput(t, "poll1", nonce), header,
		header+"7 0 7 1 2 3 4 "+strings.Repeat("x", maxProcRecord)+"\n"+
			"8 0 8 1 2 3 4 "+strings.Repeat("y", 200<<10)+"\n"+
			"9 0 9 1 2 3 4 ok\n", 1)
	out, err := readOutput(strings.NewReader(raw), nonce)
	if err != nil || out.procsTruncated {
		t.Fatalf("readOutput = %v, truncated %v", err, out.procsTruncated)
	}
	s, err := parseSample(out.text, nonce, time.Now())
	if err != nil {
		t.Fatalf("parseSample: %v", err)
	}
	if s.procs[0].pid != 9 || s.procs[0].comm != "ok" || len(s.procs) != 15 {
		t.Errorf("procs = %d, first %+v; want overlong records dropped", len(s.procs), s.procs[0])
	}
}

func TestReadOutputLimitsOtherSections(t *testing.T) {
	const nonce = "n"
	noise := strings.Repeat(strings.Repeat("z", 1000)+"\n", maxOutput/1000+1)
	if _, err := readOutput(strings.NewReader(noise), nonce); !errors.Is(err, errOutputTooLarge) {
		t.Errorf("noise before markers: err = %v, want errOutputTooLarge", err)
	}
	header := markerPrefix + nonce + ":" + secPasswd + "\n"
	raw := strings.Replace(fixtureOutput(t, "poll1", nonce), header, header+noise, 1)
	if _, err := readOutput(strings.NewReader(raw), nonce); !errors.Is(err, errOutputTooLarge) {
		t.Errorf("large passwd section: err = %v, want errOutputTooLarge", err)
	}
	forged := strings.Repeat(markerPrefix+"other:"+secProcs+"\n"+strings.Repeat("1 0 1 1 1 1 1 x\n", 100), maxOutput/1500)
	if _, err := readOutput(strings.NewReader(forged), nonce); !errors.Is(err, errOutputTooLarge) {
		t.Errorf("procs marker with a foreign nonce: err = %v, want errOutputTooLarge", err)
	}
}

func TestReadOutputReadError(t *testing.T) {
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader("partial\n"), iotest.ErrReader(boom))
	if _, err := readOutput(r, "n"); !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}
