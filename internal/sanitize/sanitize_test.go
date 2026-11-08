package sanitize

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestLine(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"ascii", "web-01 up 3d", "web-01 up 3d"},
		{"latin", "Ünïcødé ☃", "Ünïcødé ☃"},
		{"cjk", "日本語", "日本語"},
		{"emoji", "🚀 ok", "🚀 ok"},
		{"hangul syllable", "한국", "한국"},

		{"sgr color", "\x1b[31mred\x1b[0m", "red"},
		{"clear screen", "a\x1b[2Jb", "ab"},
		{"cursor move", "a\x1b[10;20Hb", "ab"},
		{"osc title bel", "\x1b]0;pwned\x07x", "x"},
		{"osc title st", "\x1b]2;pwned\x1b\\x", "x"},
		{"osc 8 hyperlink", "\x1b]8;;http://evil.example\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"dcs", "a\x1bP1$r\x1b\\b", "ab"},

		{"nul", "a\x00b", "ab"},
		{"bell", "a\x07b", "ab"},
		{"backspace", "abc\x08\x08x", "abcx"},
		{"del", "a\x7fb", "ab"},
		{"vertical tab and form feed", "a\x0bb\x0cc", "abc"},
		{"tab newline cr", "a\tb\nc\rd", "a b c d"},
		{"crlf", "a\r\nb", "a  b"},

		{"c1 next line", "a\u0085b", "ab"},
		{"c1 csi dropped, text kept", "a\u009b31mb", "a31mb"},
		{"c1 osc and st dropped, text kept", "a\u009d0;t\u009cb", "a0;tb"},

		{"rlo override", "abc\u202edef", "abcdef"},
		{"lro and pdf", "\u202dx\u202c", "x"},
		{"isolates", "\u2066a\u2067b\u2068c\u2069", "abc"},
		{"rlm lrm alm", "\u200fa\u200eb\u061c", "ab"},
		{"zero width space", "a\u200bb", "ab"},
		{"zwj", "a\u200db", "ab"},
		{"bom", "\ufeffa", "a"},
		{"soft hyphen", "a\u00adb", "ab"},
		{"private use", "a\ue000b\U000f0000", "ab"},

		{"line separator", "a\u2028b", "ab"},
		{"paragraph separator", "a\u2029b", "ab"},
		{"hangul fillers", "a\u115f\u1160\u3164\uffa0b", "ab"},

		{"leading combining mark", "\u0301e", "e"},
		{"leading enclosing mark", "\u20dda", "a"},
		{"combining after base", "e\u0301", "e\u0301"},
		{"stacked combining after base", "e\u0301\u0302", "e\u0301\u0302"},
		{"enclosing after base", "1\u20dd", "1\u20dd"},
		{"combining after space", "a \u0301b", "a b"},
		{"combining after tab", "a\t\u0301b", "a b"},
		{"combining after dropped control", "a\x00\u0301", "a\u0301"},
		{"combining after escape", "a\x1b[1m\u0301", "a\u0301"},
		{"combining run at start", "\u0301\u0302\u0303x", "x"},
		{"variation selector after emoji", "\u2764\ufe0f", "\u2764\ufe0f"},

		{"invalid byte", "a\xffb", "a\ufffdb"},
		{"truncated sequence", "a\xc3", "a\ufffd"},
		{"overlong encoding", "\xc0\xafx", "\ufffdx"},
		{"surrogate half", "\xed\xa0\x80x", "\ufffdx"},
		{"combining after replacement", "\xff\u0301", "\ufffd\u0301"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Line(tt.in); got != tt.want {
				t.Errorf("Line(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLines(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"keeps newlines", "a\nb\n", "a\nb\n"},
		{"cr and tab", "a\r\nb\tc", "a \nb c"},
		{"separators dropped", "a\u2028b\u2029c", "abc"},
		{"combining at line start", "a\n\u0301b", "a\nb"},
		{"escape across lines", "\x1b[1ma\nb\x1b[0m", "a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Lines(tt.in); got != tt.want {
				t.Errorf("Lines(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestOutputIsPrintable(t *testing.T) {
	inputs := []string{
		"\x1b[31m\x00\u0085\u202e\u2028\u3164\u0301\xff",
		"\x1b]52;c;aGVsbG8=\x07clipboard",
		"\x1b[?1049h\x1b[?25lhidden",
		string([]byte{0x1b, 0x5b, 0x33, 0x31}),
		"\x9b\x9d\x90\x98\x9e\x9f",
	}
	for _, in := range inputs {
		for _, got := range []string{Line(in), Lines(in)} {
			if !utf8.ValidString(got) {
				t.Errorf("sanitized %q is not valid UTF-8: %q", in, got)
			}
			for _, r := range got {
				if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Co, unicode.Zl, unicode.Zp)) {
					t.Errorf("sanitized %q still contains %U: %q", in, r, got)
				}
			}
		}
	}
}
