package fuzzy

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFindMatches(t *testing.T) {
	tests := []struct {
		query, target string
		ok            bool
		positions     []int
	}{
		{"", "anything", true, nil},
		{"", "", true, nil},
		{"a", "", false, nil},
		{"abc", "ab", false, nil},
		{"xyz", "abc", false, nil},
		{"cba", "abc", false, nil},
		{"cpu", "CPU usage", true, []int{0, 1, 2}},
		{"CPU", "cpu usage", true, []int{0, 1, 2}},
		{"mu", "memory usage", true, []int{0, 7}},
		{"gp", "go to processes", true, []int{0, 6}},
		{"np", "net/proc", true, []int{0, 4}},
		{"sd", "showDisks", true, []int{0, 4}},
		{"dsk", "show disks", true, []int{5, 7, 8}},
		{"tab", "toggle alert bar", true, []int{0, 7, 13}},
		{"ünï", "Ünïcode", true, []int{0, 1, 2}},
		{"界", "世界", true, []int{1}},
		{"ll", "hello", true, []int{2, 3}},
	}
	for _, tc := range tests {
		m, ok := Find(tc.query, tc.target)
		if ok != tc.ok {
			t.Errorf("Find(%q, %q) ok = %v, want %v", tc.query, tc.target, ok, tc.ok)
			continue
		}
		if ok && fmt.Sprint(m.Positions) != fmt.Sprint(tc.positions) {
			t.Errorf("Find(%q, %q) positions = %v, want %v", tc.query, tc.target, m.Positions, tc.positions)
		}
	}
}

func TestFindScoring(t *testing.T) {
	better := []struct {
		query, a, b string
	}{
		{"proc", "processes", "pause recording of clock"},
		{"proc", "processes", "show processes"},
		{"net", "network", "internet"},
		{"ds", "disk stats", "dashes"},
		{"ds", "diskStats", "dumbass"},
		{"cfg", "config", "scaffolding"},
		{"log", "logs", "blog"},
		{"al", "alerts", "global"},
		{"sp", "system-panel", "suspend"},
		{"theme", "theme: toggle", "the menu editor"},
	}
	for _, tc := range better {
		a, okA := Find(tc.query, tc.a)
		b, okB := Find(tc.query, tc.b)
		if !okA || !okB {
			t.Fatalf("%q should match both %q and %q", tc.query, tc.a, tc.b)
		}
		if a.Score <= b.Score {
			t.Errorf("%q: %q (%d) should beat %q (%d)", tc.query, tc.a, a.Score, tc.b, b.Score)
		}
	}
}

func TestFindPicksBestAlignment(t *testing.T) {
	m, _ := Find("ab", "a-xab")
	if fmt.Sprint(m.Positions) != "[3 4]" && fmt.Sprint(m.Positions) != "[0 4]" {
		t.Fatalf("unexpected positions %v", m.Positions)
	}
	m, _ = Find("ab", "xxa xab")
	if fmt.Sprint(m.Positions) != "[5 6]" {
		t.Errorf("consecutive run should win: %v", m.Positions)
	}
}

func TestRank(t *testing.T) {
	targets := []string{"blog", "logs", "zzz", "Log level", "catalog", "logs"}
	got := Rank("log", targets)
	indices := make([]int, len(got))
	for i, r := range got {
		indices[i] = r.Index
	}
	if len(got) != 5 {
		t.Fatalf("want 5 matches, got %v", indices)
	}
	if got[0].Index != 1 || got[1].Index != 3 || got[2].Index != 5 {
		t.Errorf("prefix matches first, ties stable: %v", indices)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("not sorted by score: %v", got)
		}
	}
	all := Rank("", targets)
	if len(all) != len(targets) {
		t.Fatalf("empty query keeps all targets")
	}
	for i, r := range all {
		if r.Index != i {
			t.Errorf("empty query keeps the original order: %v", all)
		}
	}
	if len(Rank("q", nil)) != 0 {
		t.Errorf("no targets, no results")
	}
}

func TestHighlight(t *testing.T) {
	base := lipgloss.NewStyle()
	hit := lipgloss.NewStyle().Bold(true)
	tests := []struct {
		target    string
		positions []int
	}{
		{"", []int{0}},
		{"processes", []int{0, 1, 2, 3}},
		{"go to processes", []int{0, 6, 14, -1, 99}},
		{"世界 ok", []int{1, 3}},
		{"none", nil},
	}
	for _, tc := range tests {
		got := Highlight(tc.target, tc.positions, base, hit)
		if ansi.Strip(got) != tc.target {
			t.Errorf("Highlight(%q) text = %q", tc.target, ansi.Strip(got))
		}
	}
	got := Highlight("abcd", []int{0, 1}, base, hit)
	want := hit.Render("ab") + base.Render("cd")
	if got != want {
		t.Errorf("runs should render together: %q want %q", got, want)
	}
	if strings.Count(Highlight("abcdef", []int{1, 2, 3}, base, hit), "\x1b[1m") != 1 {
		t.Errorf("a contiguous hit run needs one bold sequence")
	}
}

func BenchmarkRank(b *testing.B) {
	targets := make([]string, 200)
	for i := range targets {
		targets[i] = fmt.Sprintf("command number %d: toggle the process view %d", i, i*7)
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = Rank("tgpv", targets)
	}
}
