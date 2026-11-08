package screens

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/widgets"
)

func TestServerStatus(t *testing.T) {
	set := store.DefaultSettings()
	online := onlineSnap("x", 10, 2, 20)
	online.Disks = nil
	hot := online
	hot.CPU.Total = 85
	crit := online
	crit.Mem.Used = crit.Mem.Total
	tests := []struct {
		name  string
		snap  metrics.Snapshot
		ok    bool
		label string
		tone  theme.Tone
	}{
		{"no snapshot", metrics.Snapshot{}, false, "unknown", theme.ToneNeutral},
		{"offline", metrics.Snapshot{State: metrics.StateOffline}, true, "offline", theme.ToneBad},
		{"connecting", metrics.Snapshot{State: metrics.StateConnecting}, true, "connecting", theme.ToneInfo},
		{"healthy", online, true, "online", theme.ToneGood},
		{"cpu warn", hot, true, "warn", theme.ToneWarn},
		{"mem crit", crit, true, "crit", theme.ToneBad},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverStatus(tt.snap, tt.ok, set)
			if got.Label != tt.label || got.Tone != tt.tone {
				t.Fatalf("got %+v, want %s/%v", got, tt.label, tt.tone)
			}
		})
	}
}

func TestServerStatusZeroSettingsUseDefaults(t *testing.T) {
	online := onlineSnap("x", 10, 2, 20)
	online.Disks = nil
	if got := serverStatus(online, true, store.Settings{}); got.Label != "online" {
		t.Fatalf("zero settings painted a healthy server %q", got.Label)
	}
}

func TestFitColumns(t *testing.T) {
	cols := []prioColumn[int]{
		{widgets.Column{Width: 10}, 100, nil},
		{widgets.Column{Min: 10, Flex: 1}, 50, nil},
		{widgets.Column{Width: 10}, 10, nil},
		{widgets.Column{Width: 10}, 70, nil},
	}
	tests := []struct {
		width int
		want  []int
	}{
		{100, []int{100, 50, 10, 70}},
		{46, []int{100, 50, 10, 70}},
		{45, []int{100, 50, 70}},
		{22, []int{100, 70}},
		{3, []int{100}},
	}
	for _, tt := range tests {
		var got []int
		for _, c := range fitColumns(cols, tt.width) {
			got = append(got, c.prio)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("width %d: kept %v, want %v", tt.width, got, tt.want)
		}
	}
}

func TestSplitUnit(t *testing.T) {
	tests := []struct{ in, num, unit string }{
		{"12.4Mb/s", "12.4M", "b/s"},
		{"950b/s", "950", "b/s"},
		{"87%", "87%", ""},
		{"3.1G", "3.1G", ""},
	}
	for _, tt := range tests {
		if n, u := splitUnit(tt.in); n != tt.num || u != tt.unit {
			t.Errorf("splitUnit(%q) = %q, %q", tt.in, n, u)
		}
	}
}

func TestWrapWords(t *testing.T) {
	tests := []struct {
		in   string
		w    int
		want []string
	}{
		{"", 10, []string{""}},
		{"add this key", 20, []string{"add this key"}},
		{"add this key to the file", 10, []string{"add this", "key to the", "file"}},
		{"ssh-ed25519 AAAAAAAAAAAAAAAA", 8, []string{"ssh-ed25", "519", "AAAAAAAA", "AAAAAAAA"}},
	}
	for _, tt := range tests {
		got := wrapWords(tt.in, tt.w)
		if !slices.Equal(got, tt.want) {
			t.Errorf("wrapWords(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
		}
		for _, l := range got {
			if ansi.StringWidth(l) > tt.w {
				t.Errorf("line %q wider than %d", l, tt.w)
			}
		}
	}
}

func TestMeanSeries(t *testing.T) {
	nan := math.NaN()
	got := meanSeries([]float64{2, 4, 6}, []float64{8, nan}, nil)
	if len(got) != 3 || got[0] != 2 || got[1] != 6 || got[2] != 6 {
		t.Fatalf("got %v, want [2 6 6]", got)
	}
	if got := meanSeries([]float64{nan}); len(got) != 1 || !math.IsNaN(got[0]) {
		t.Fatalf("all-NaN point = %v, want NaN", got)
	}
	if got := meanSeries(); len(got) != 0 {
		t.Fatalf("no series = %v", got)
	}
}

func TestResample(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7}
	if got := resample(values, 10); !slices.Equal(got, values) {
		t.Fatalf("fitting values changed: %v", got)
	}
	if got, want := resample(values, 3), []float64{1.5, 3.5, 6}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := resample([]float64{math.NaN(), math.NaN(), 1, math.NaN()}, 2); !math.IsNaN(got[0]) || got[1] != 1 {
		t.Fatalf("NaN buckets: %v", got)
	}
	if got := resample(values, 0); got != nil {
		t.Fatalf("zero columns: %v", got)
	}
}

func TestSumSeries(t *testing.T) {
	got := sumSeries([]float64{1, 2, 3}, []float64{10, 20})
	if want := []float64{1, 12, 23}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMatchesFilter(t *testing.T) {
	srv := store.Server{Name: "Web-01", Host: "10.0.0.1", Tags: []string{"prod"}}
	tests := []struct {
		q    string
		want bool
	}{
		{"", true},
		{"web", true},
		{"10.0", true},
		{"PROD", true},
		{"db", false},
	}
	for _, tt := range tests {
		if got := matchesFilter(srv, tt.q); got != tt.want {
			t.Errorf("matchesFilter(%q) = %v", tt.q, got)
		}
	}
}

func TestHostPort(t *testing.T) {
	if got := hostPort("2001:db8::1", 2222); got != "[2001:db8::1]:2222" {
		t.Fatal(got)
	}
	if got := hostPort("web", 22); got != "web:22" {
		t.Fatal(got)
	}
}

func TestStepIndex(t *testing.T) {
	tests := []struct{ cur, delta, n, want int }{
		{0, 1, 3, 1},
		{2, 1, 3, 2},
		{0, -1, 3, 0},
		{-1, 5, 3, 0},
		{1, 0, 0, -1},
	}
	for _, tt := range tests {
		if got := stepIndex(tt.cur, tt.delta, tt.n); got != tt.want {
			t.Errorf("stepIndex(%d,%d,%d) = %d, want %d", tt.cur, tt.delta, tt.n, got, tt.want)
		}
	}
}

func TestTextInputEditing(t *testing.T) {
	tests := []struct {
		name  string
		start string
		keys  []string
		want  string
	}{
		{"type", "", []string{"a", "b"}, "ab"},
		{"backspace", "abc", []string{"backspace"}, "ab"},
		{"insert in middle", "ac", []string{"left", "b"}, "abc"},
		{"delete forward", "abc", []string{"left", "left", "delete"}, "ac"},
		{"ctrl+u clears before cursor", "abc", []string{"ctrl+u"}, ""},
		{"ctrl+w deletes word", "foo bar", []string{"ctrl+w"}, "foo "},
		{"space", "a", []string{"space", "b"}, "a b"},
		{"backspace at start", "", []string{"backspace", "left"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInput("")
			in.SetValue(tt.start)
			for _, k := range tt.keys {
				in.Update(press(k))
			}
			if in.Value() != tt.want {
				t.Fatalf("value %q, want %q", in.Value(), tt.want)
			}
		})
	}
}

func TestTextInputViewWidth(t *testing.T) {
	th := theme.Default()
	long := newInput("placeholder text that is long")
	long.SetValue(strings.Repeat("x", 100))
	empty := newInput("placeholder text that is long")
	wide := newInput("")
	wide.SetValue("日本語のホスト名")
	for _, in := range []textInput{long, empty, wide} {
		for _, w := range []int{1, 2, 5, 20} {
			for _, focused := range []bool{true, false} {
				if got := ansi.StringWidth(in.View(th, w, focused)); got != w {
					t.Errorf("value %q width %d focused=%v: got %d", in.Value(), w, focused, got)
				}
			}
		}
	}
}

func TestTextInputSanitizes(t *testing.T) {
	in := newInput("")
	in.Insert("a\x1b[2Jb\tc\x00")
	if got := in.Value(); got != "ab c" {
		t.Fatalf("got %q", got)
	}
}
