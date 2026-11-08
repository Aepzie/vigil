package svgshot

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/Aepzie/vigil/internal/theme"
)

// element is a decoded SVG element with its attributes and text.
type element struct {
	name  string
	attrs map[string]string
	text  string
}

// decode parses doc with encoding/xml, failing the test on malformed
// XML, and returns every element in document order.
func decode(t *testing.T, doc []byte) []element {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var out []element
	var stack []int
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("malformed XML: %v\n%s", err, doc)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			e := element{name: tok.Name.Local, attrs: map[string]string{}}
			for _, a := range tok.Attr {
				e.attrs[a.Name.Local] = a.Value
			}
			stack = append(stack, len(out))
			out = append(out, e)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				out[stack[len(stack)-1]].text += string(tok)
			}
		}
	}
	if len(stack) != 0 {
		t.Fatalf("unclosed elements in\n%s", doc)
	}
	return out
}

func render(t *testing.T, frame string, opts Options) ([]byte, []element) {
	t.Helper()
	doc, err := Render(frame, opts)
	if err != nil {
		t.Fatal(err)
	}
	return doc, decode(t, doc)
}

// cellTexts returns the terminal text runs (not the window title).
func cellTexts(elems []element) []element {
	var out []element
	for _, e := range elems {
		if e.name == "text" && e.attrs["textLength"] != "" {
			out = append(out, e)
		}
	}
	return out
}

// cellRects returns the background and block rects (the ones inside
// the crispEdges group are the only rects with an x attribute besides
// the outline).
func cellRects(elems []element) []element {
	var out []element
	for _, e := range elems {
		if e.name == "rect" && e.attrs["x"] != "" && e.attrs["stroke"] == "" {
			out = append(out, e)
		}
	}
	return out
}

func px(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return v
}

func near(a, b float64) bool { return a-b < 0.011 && b-a < 0.011 }

func TestWellFormed(t *testing.T) {
	th := theme.Default()
	frame := strings.Join([]string{
		th.P.Accent.FG() + theme.Bold + "◆ vigil" + theme.Reset + " \x1b[38;5;196mred\x1b[39m \x1b[4munder\x1b[24m",
		th.P.Good.BG() + "  " + "\x1b[49m ▁▂▃▄▅▆▇█ ▏▎▍▌▋▊▉ ░▒▓ ▖▗▘▙▚▛▜▝▞▟ ▀▐▔▕",
		"╭─┬─╮ ⣿⡇⠁ 界 🙂 \x1b[7mrev\x1b[27m \x1b[2;3;9mfaint\x1b[m",
		"\x1b]0;title\x07\x1b[?25l\x1b[2Jcontrol\x00chars\tand tabs",
	}, "\n")
	doc, elems := render(t, frame, Options{Title: "vigil — overview"})
	if !bytes.HasPrefix(doc, []byte("<svg ")) {
		t.Fatalf("document does not start with <svg: %.40q", doc)
	}
	if elems[0].name != "svg" || elems[0].attrs["viewBox"] == "" {
		t.Fatalf("root = %+v", elems[0])
	}
	for _, e := range cellTexts(elems) {
		if e.attrs["space"] != "preserve" {
			t.Errorf("text %q lacks xml:space=preserve", e.text)
		}
		if strings.ContainsAny(e.text, "█▄▀▌▐▁▇▏▉░▒▓▖▟") {
			t.Errorf("block element rendered as text: %q", e.text)
		}
	}
}

func TestEmptyFrame(t *testing.T) {
	if _, err := Render("", Options{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
}

func TestGeometry(t *testing.T) {
	doc, elems := render(t, "ab\ncd\nef", Options{})
	root := elems[0]
	wantW := 2*defaultPadding + 2*8.4
	wantH := titleBarHeight + 2*defaultPadding + 3*17.0
	if !near(px(t, root.attrs["width"]), wantW) || !near(px(t, root.attrs["height"]), wantH) {
		t.Fatalf("size = %s×%s, want %v×%v\n%s", root.attrs["width"], root.attrs["height"], wantW, wantH, doc)
	}
}

func TestWideGlyphPosition(t *testing.T) {
	_, elems := render(t, "a界b🙂c", Options{})
	texts := cellTexts(elems)
	want := []struct {
		text  string
		col   int
		cells int
	}{{"a", 0, 1}, {"界", 1, 2}, {"b", 3, 1}, {"🙂", 4, 2}, {"c", 6, 1}}
	if len(texts) != len(want) {
		t.Fatalf("got %d runs, want %d: %+v", len(texts), len(want), texts)
	}
	for i, w := range want {
		e := texts[i]
		x := px(t, e.attrs["x"])
		if e.text != w.text || !near(x, defaultPadding+float64(w.col)*8.4) || !near(px(t, e.attrs["textLength"]), float64(w.cells)*8.4) {
			t.Errorf("run %d = %q at x=%s len=%s, want %q at column %d over %d cells",
				i, e.text, e.attrs["x"], e.attrs["textLength"], w.text, w.col, w.cells)
		}
	}
}

func TestRunsMergeAndTrim(t *testing.T) {
	_, elems := render(t, "  ab  cd  \x1b[1mbold\x1b[22m", Options{})
	texts := cellTexts(elems)
	if len(texts) != 2 {
		t.Fatalf("runs = %+v", texts)
	}
	if texts[0].text != "ab  cd" || !near(px(t, texts[0].attrs["x"]), defaultPadding+2*8.4) || !near(px(t, texts[0].attrs["textLength"]), 6*8.4) {
		t.Errorf("first run = %+v", texts[0])
	}
	if texts[1].text != "bold" || !strings.Contains(texts[1].attrs["class"], "b") {
		t.Errorf("second run = %+v", texts[1])
	}
}

func TestEscaping(t *testing.T) {
	title := `a & b <"x">`
	doc, elems := render(t, `<a href='x'>&amp;</a>`+"\x01", Options{Title: title})
	if bytes.Contains(doc, []byte("<a href")) || !bytes.Contains(doc, []byte("&amp;amp;")) {
		t.Fatalf("frame text not escaped:\n%s", doc)
	}
	texts := cellTexts(elems)
	if len(texts) != 1 || texts[0].text != `<a href='x'>&amp;</a>` {
		t.Fatalf("decoded runs = %+v", texts)
	}
	var gotTitle bool
	for _, e := range elems {
		if e.name == "title" && e.text == title {
			gotTitle = true
		}
	}
	if !gotTitle || elems[0].attrs["aria-label"] != title {
		t.Fatalf("title not round-tripped:\n%s", doc)
	}
	if got := escape("ok\x00\x1b\ufffe"); got != "ok\ufffd\ufffd\ufffd" {
		t.Fatalf("escape of invalid XML characters = %q", got)
	}
}

func TestColors(t *testing.T) {
	th := theme.Default()
	frame := "\x1b[38;2;1;2;3mA\x1b[48;2;4;5;6mB\x1b[0m" +
		"\x1b[38;5;196mC\x1b[38:2::7:8:9mD\x1b[31mE\x1b[39mF\x1b[7mG"
	doc, elems := render(t, frame, Options{Theme: th})
	css := ""
	for _, e := range elems {
		if e.name == "style" {
			css = e.text
		}
	}
	classOf := map[string]string{}
	for _, e := range cellTexts(elems) {
		classOf[e.text] = e.attrs["class"]
	}
	fill := func(text string) string {
		cls := strings.Fields(classOf[text])
		if len(cls) == 0 {
			t.Fatalf("no run %q in\n%s", text, doc)
		}
		_, rest, ok := strings.Cut(css, "."+cls[0]+"{fill:")
		if !ok {
			t.Fatalf("no rule for class %q in %s", cls[0], css)
		}
		return rest[:7]
	}
	tests := []struct{ text, want string }{
		{"AB", "#010203"},
		{"C", "#ff0000"},
		{"D", "#070809"},
		{"E", th.P.Bad.String()},
		{"F", th.P.Text.String()},
		{"G", th.P.Bg.String()},
	}
	for _, tt := range tests {
		if got := fill(tt.text); got != tt.want {
			t.Errorf("fill of %q = %s, want %s", tt.text, got, tt.want)
		}
	}

	fills := map[string]bool{}
	for _, e := range cellRects(elems) {
		fills[e.attrs["fill"]] = true
	}
	if !fills["#040506"] || !fills[th.P.Text.String()] {
		t.Errorf("background rects = %v, want #040506 and the reversed %s", fills, th.P.Text)
	}
	if fills[th.P.Bg.String()] {
		t.Errorf("default background drawn as a rect: %v", fills)
	}
}

func TestBlockElementsAreRects(t *testing.T) {
	th := theme.Default()
	frame := th.P.Good.FG() + "██" + th.P.Warn.FG() + "▌" + theme.Reset + "\n" +
		th.P.Good.FG() + "██" + theme.Reset + "▄"
	doc, elems := render(t, frame, Options{})
	if texts := cellTexts(elems); len(texts) != 0 {
		t.Fatalf("block glyphs rendered as text: %+v", texts)
	}
	rects := cellRects(elems)
	if len(rects) != 3 {
		t.Fatalf("want 3 merged rects, got %d:\n%s", len(rects), doc)
	}
	top := float64(defaultPadding + titleBarHeight)
	check := func(e element, x, y, w, h float64, fill string) {
		t.Helper()
		got := [4]float64{px(t, e.attrs["x"]), px(t, e.attrs["y"]), px(t, e.attrs["width"]), px(t, e.attrs["height"])}
		want := [4]float64{x, y, w, h}
		for i := range got {
			if !near(got[i], want[i]) {
				t.Errorf("rect %v, want %v", got, want)
				break
			}
		}
		if e.attrs["fill"] != fill {
			t.Errorf("fill %s, want %s", e.attrs["fill"], fill)
		}
	}
	check(rects[0], defaultPadding, top, 2*8.4, 2*17, th.P.Good.String())
	check(rects[1], defaultPadding+2*8.4, top, 8.4/2, 17, th.P.Warn.String())
	check(rects[2], defaultPadding+2*8.4, top+17+17.0/2, 8.4, 17.0/2, th.P.Text.String())
}

func TestShadeOpacity(t *testing.T) {
	_, elems := render(t, "░▒▓", Options{})
	rects := cellRects(elems)
	want := []string{"0.25", "0.5", "0.75"}
	if len(rects) != len(want) {
		t.Fatalf("rects = %+v", rects)
	}
	for i, e := range rects {
		if e.attrs["fill-opacity"] != want[i] {
			t.Errorf("shade %d opacity = %q, want %s", i, e.attrs["fill-opacity"], want[i])
		}
	}
}

func TestBlockGeometry(t *testing.T) {
	tests := []struct {
		glyph string
		want  []box
	}{
		{"█", []box{{0, 0, 8, 8}}},
		{"▀", []box{{0, 0, 8, 4}}},
		{"▄", []box{{0, 4, 8, 4}}},
		{"▁", []box{{0, 7, 8, 1}}},
		{"▇", []box{{0, 1, 8, 7}}},
		{"▏", []box{{0, 0, 1, 8}}},
		{"▉", []box{{0, 0, 7, 8}}},
		{"▌", []box{{0, 0, 4, 8}}},
		{"▐", []box{{4, 0, 4, 8}}},
		{"▚", []box{upperLeft, lowerRight}},
	}
	for _, tt := range tests {
		got, ok := blockGeometry(tt.glyph)
		if !ok || len(got.boxes) != len(tt.want) {
			t.Errorf("%s: %+v, %v", tt.glyph, got, ok)
			continue
		}
		for i := range got.boxes {
			if got.boxes[i] != tt.want[i] {
				t.Errorf("%s: box %d = %+v, want %+v", tt.glyph, i, got.boxes[i], tt.want[i])
			}
		}
	}
	for _, g := range []string{"a", "─", "⣿", "█̈", ""} {
		if _, ok := blockGeometry(g); ok {
			t.Errorf("%q treated as a block element", g)
		}
	}
}
