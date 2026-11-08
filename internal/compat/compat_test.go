package compat

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// sampleFrame mixes every glyph family vigil draws with SGR sequences,
// wide characters and combining marks.
var sampleFrame = strings.Join([]string{
	"\x1b[38;2;38;42;49m╭─ \x1b[1mCPU\x1b[0m ────── 8 cores ─╮\x1b[0m",
	"│ ▁▂▃▄▅▆▇█ ▏▎▍▌▋▊▉ ░▒▓ ▀▐ │",
	"│ ⠀⠁⠉⠛⠿⣿⡇⢸⣀⠤ \x1b[48;2;24;27;32m┈┈┈┈\x1b[m ━━╸━ │",
	"│ ┏━┓┃ ┃┗━┛ ╺┳╸ ╱╲ ═║╔╗ │",
	"│ ◉ ≡ ⚡ ◈ ✱ ● ○ ▲ ▼ › ↑ ↓ ⏎ … · │",
	"│ 界 e\u0301 ⚡\ufe0f 👍🏽 ñ ü \u00a0│",
	"╰──────────────────────╯\x1b]8;;https://example.com/ü\x1b\\link\x1b]8;;\x1b\\",
}, "\n")

func TestApplyPreservesWidthsAndSGR(t *testing.T) {
	for _, m := range []Mode{Auto, Blocks, ASCII, Off} {
		got := Apply(sampleFrame, m)
		in, out := strings.Split(sampleFrame, "\n"), strings.Split(got, "\n")
		if len(in) != len(out) {
			t.Fatalf("%v: line count %d → %d", m, len(in), len(out))
		}
		for i := range in {
			if a, b := ansi.StringWidth(in[i]), ansi.StringWidth(out[i]); a != b {
				t.Errorf("%v line %d: width %d → %d\n%q\n%q", m, i, a, b, ansi.Strip(in[i]), ansi.Strip(out[i]))
			}
			if a, b := sgrPattern.FindAllString(in[i], -1), sgrPattern.FindAllString(out[i], -1); strings.Join(a, "") != strings.Join(b, "") {
				t.Errorf("%v line %d: SGR changed %q → %q", m, i, a, b)
			}
		}
	}
}

func TestApplyOffIsIdentity(t *testing.T) {
	for _, m := range []Mode{Auto, Off} {
		if Apply(sampleFrame, m) != sampleFrame {
			t.Errorf("%v must not change the frame", m)
		}
	}
}

func TestApplyASCIIOnlyASCII(t *testing.T) {
	got := ansi.Strip(Apply(sampleFrame, ASCII))
	for _, r := range got {
		if r > 0x7e || (r < 0x20 && r != '\n') {
			t.Fatalf("non-ASCII rune %q left in %q", r, got)
		}
	}
	if !strings.Contains(Apply(sampleFrame, ASCII), "https://example.com/ü") {
		t.Errorf("escape sequence payloads must be left intact")
	}
}

func TestApplyBlocksKeepsConsoleGlyphs(t *testing.T) {
	got := ansi.Strip(Apply(sampleFrame, Blocks))
	for _, banned := range "╭╮╰╯┈━┃┏┓┗┛╸╺╻⠁⣿▁▂▃▅▆▇▏▎▍▋▊▉" {
		if strings.ContainsRune(got, banned) {
			t.Errorf("Blocks output still contains %q", banned)
		}
	}
	for _, kept := range "█▄▌░▒▓▀▐─│┌┐└┘═║╔╗界" {
		if !strings.ContainsRune(got, kept) {
			t.Errorf("Blocks output lost %q", kept)
		}
	}
}

func TestApplyGlyphs(t *testing.T) {
	tests := []struct {
		mode Mode
		in   string
		want string
	}{
		{Blocks, "╭─╮", "┌─┐"},
		{Blocks, "╰─╯", "└─┘"},
		{Blocks, "┈┈", "··"},
		{Blocks, "▁▂▃▄▅▆▇█", " ▄▄▄▄███"},
		{Blocks, "▏▎▍▌▋▊▉", " ▌▌▌▌██"},
		{Blocks, "⠀⠉⣀⡇⢸⣿⠡", " ▀▄▌▐█░"},
		{Blocks, "┏━┓", "┌─┐"},
		{ASCII, "╭─┬─╮", "+-+-+"},
		{ASCII, "│┃║", "|||"},
		{ASCII, "┈·", ".."},
		{ASCII, "⠀⠉⣀⡇⣿", " '.:#"},
		{ASCII, "▁▄█░▒▓", ".:#.:#"},
		{ASCII, "◉≡◈✱●○▲▼›↑↓⏎…", "@=***o^v>^v<~"},
		{ASCII, "界", "??"},
		{ASCII, "e\u0301", "e"},
		{ASCII, "ü", "?"},
		{ASCII, "a\x1b[31mé\x1b[0m", "a\x1b[31m?\x1b[0m"},
	}
	for _, tc := range tests {
		if got := Apply(tc.in, tc.mode); got != tc.want {
			t.Errorf("Apply(%q, %v) = %q, want %q", tc.in, tc.mode, got, tc.want)
		}
	}
}

func TestReplacementTablesKeepWidth(t *testing.T) {
	for name, table := range map[string]map[rune]string{"blocks": blocksTable, "ascii": asciiTable} {
		for r, repl := range table {
			if ansi.StringWidth(string(r)) != ansi.StringWidth(repl) {
				t.Errorf("%s: %q → %q changes width", name, r, repl)
			}
		}
	}
	for r, repl := range asciiTable {
		for _, c := range repl {
			if c > 0x7e {
				t.Errorf("ascii: %q → %q is not ASCII", r, repl)
			}
		}
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
		env  Env
		want Mode
	}{
		{"explicit blocks", Blocks, Env{Term: "xterm-256color"}, Blocks},
		{"explicit off", Off, Env{Term: "dumb"}, Off},
		{"modern", Auto, Env{Term: "xterm-256color", Lang: "en_US.UTF-8"}, Off},
		{"no locale", Auto, Env{Term: "xterm"}, Off},
		{"utf8 lowercase", Auto, Env{Term: "xterm", Lang: "de_DE.utf8"}, Off},
		{"posix locale", Auto, Env{Term: "xterm", Lang: "C"}, ASCII},
		{"latin1", Auto, Env{Term: "xterm", LCCtype: "en_US.ISO-8859-1"}, ASCII},
		{"lc_all wins", Auto, Env{Term: "xterm", LCAll: "C", Lang: "en_US.UTF-8"}, ASCII},
		{"lc_ctype before lang", Auto, Env{Term: "xterm", LCCtype: "C.UTF-8", Lang: "C"}, Off},
		{"dumb", Auto, Env{Term: "dumb"}, ASCII},
		{"vt100", Auto, Env{Term: "VT100"}, ASCII},
		{"vt220", Auto, Env{Term: "vt220"}, ASCII},
		{"linux vt", Auto, Env{Term: "linux"}, Blocks},
		{"linux vt latin1", Auto, Env{Term: "linux", Lang: "POSIX"}, ASCII},
		{"windows console", Auto, Env{Term: "xterm-256color", WindowsConsole: true}, Blocks},
		{"putty", Auto, Env{Term: "putty-256color"}, Blocks},
		{"cons25", Auto, Env{Term: "cons25"}, Blocks},
	}
	for _, tc := range tests {
		if got := Resolve(tc.mode, tc.env); got != tc.want {
			t.Errorf("%s: Resolve = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestModeCycle(t *testing.T) {
	order := []Mode{Auto, Blocks, ASCII, Off, Auto}
	for i := 0; i+1 < len(order); i++ {
		if got := order[i].Next(); got != order[i+1] {
			t.Errorf("%v.Next() = %v, want %v", order[i], got, order[i+1])
		}
	}
	for _, m := range order {
		if Parse(m.String()) != m {
			t.Errorf("Parse(%q) round trip failed", m.String())
		}
	}
	if Parse("bogus") != Auto {
		t.Errorf("unknown names parse to Auto")
	}
}

func TestSkipEscape(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"\x1b[0mX", 4},
		{"\x1b[38;2;1;2;3mX", 13},
		{"\x1b]8;;url\x07X", 9},
		{"\x1b]8;;url\x1b\\X", 10},
		{"\x1b(BX", 3},
		{"\x1b7X", 2},
		{"\x1b[", 2},
		{"\x1b", 1},
	}
	for _, tc := range tests {
		if got := skipEscape(tc.in, 0); got != tc.want {
			t.Errorf("skipEscape(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func BenchmarkApplyASCII(b *testing.B) {
	frame := strings.Repeat(sampleFrame+"\n", 20)
	b.ReportAllocs()
	for b.Loop() {
		_ = Apply(frame, ASCII)
	}
}
