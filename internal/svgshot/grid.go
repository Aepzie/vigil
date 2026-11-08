package svgshot

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/theme"
)

// tabWidth is the distance between tab stops.
const tabWidth = 8

// style is the SGR state of a cell. Colors are resolved to RGB when the
// sequence is parsed; fgSet/bgSet are false for the terminal defaults.
type style struct {
	fg, bg       theme.RGB
	fgSet, bgSet bool
	bold         bool
	faint        bool
	italic       bool
	underline    bool
	strike       bool
	reverse      bool
}

// cell is one terminal cell. A wide grapheme is stored in its first cell
// with width 2; the cell to its right has cont set and no glyph.
type cell struct {
	glyph string
	width int
	cont  bool
	st    style
}

// grid is the parsed frame: rows of cells, all padded to cols.
type grid struct {
	rows [][]cell
	cols int
}

// parseFrame decodes an ANSI frame into a cell grid. Only SGR sequences
// are interpreted; other escape sequences and control characters except
// \n, \r and \t are ignored. One trailing newline is dropped.
func parseFrame(frame string, p *theme.Palette) grid {
	frame = strings.TrimSuffix(strings.TrimSuffix(frame, "\n"), "\r")
	g := grid{rows: [][]cell{nil}}
	var st style
	row, col := 0, 0
	var state byte
	for len(frame) > 0 {
		seq, width, n, next := ansi.DecodeSequence(frame, state, nil)
		state = next
		frame = frame[n:]
		if n == 0 {
			break
		}
		switch {
		case width > 0:
			g.put(row, col, seq, width, st)
			col += width
		case seq == "\n":
			row++
			col = 0
			g.rows = append(g.rows, nil)
		case seq == "\r":
			col = 0
		case seq == "\t":
			end := (col/tabWidth + 1) * tabWidth
			for ; col < end; col++ {
				g.put(row, col, " ", 1, st)
			}
		case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
			st = applySGR(st, seq[2:len(seq)-1], p)
		}
	}
	for _, r := range g.rows {
		g.cols = max(g.cols, len(r))
	}
	for i := range g.rows {
		for len(g.rows[i]) < g.cols {
			g.rows[i] = append(g.rows[i], cell{})
		}
	}
	return g
}

// put writes a grapheme of the given width at (row, col), clearing any
// wide glyph it partially overwrites.
func (g *grid) put(row, col int, glyph string, width int, st style) {
	r := g.rows[row]
	for len(r) < col+width {
		r = append(r, cell{})
	}
	if r[col].cont && col > 0 {
		r[col-1] = cell{glyph: " ", width: 1, st: r[col-1].st}
	}
	if end := col + width; end < len(r) && r[end].cont {
		r[end] = cell{glyph: " ", width: 1, st: r[end].st}
	}
	r[col] = cell{glyph: glyph, width: width, st: st}
	for i := 1; i < width; i++ {
		r[col+i] = cell{cont: true, st: st}
	}
	g.rows[row] = r
}

// applySGR returns st updated by the parameters of one SGR sequence
// (the text between "ESC [" and "m"). Private sequences are ignored.
func applySGR(st style, params string, p *theme.Palette) style {
	if params != "" && strings.ContainsAny(params[:1], "<=>?") {
		return st
	}
	tokens := strings.Split(params, ";")
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		head, sub, hasSub := strings.Cut(tok, ":")
		code := 0
		if head != "" {
			v, err := strconv.Atoi(head)
			if err != nil {
				continue
			}
			code = v
		}
		switch {
		case code == 0:
			st = style{}
		case code == 1:
			st.bold = true
		case code == 2:
			st.faint = true
		case code == 3:
			st.italic = true
		case code == 4:
			st.underline = !hasSub || sub != "0"
		case code == 7:
			st.reverse = true
		case code == 9:
			st.strike = true
		case code == 21:
			st.underline = true
		case code == 22:
			st.bold, st.faint = false, false
		case code == 23:
			st.italic = false
		case code == 24:
			st.underline = false
		case code == 27:
			st.reverse = false
		case code == 29:
			st.strike = false
		case code >= 30 && code <= 37:
			st.fg, st.fgSet = ansi256(code-30, p), true
		case code >= 90 && code <= 97:
			st.fg, st.fgSet = ansi256(code-90+8, p), true
		case code == 39:
			st.fgSet = false
		case code >= 40 && code <= 47:
			st.bg, st.bgSet = ansi256(code-40, p), true
		case code >= 100 && code <= 107:
			st.bg, st.bgSet = ansi256(code-100+8, p), true
		case code == 49:
			st.bgSet = false
		case code == 38 || code == 48 || code == 58:
			var c theme.RGB
			var ok bool
			if hasSub {
				c, ok = extendedColor(strings.Split(sub, ":"), p)
			} else {
				var used int
				c, ok, used = extendedColorArgs(tokens[i+1:], p)
				i += used
			}
			if !ok {
				continue
			}
			switch code {
			case 38:
				st.fg, st.fgSet = c, true
			case 48:
				st.bg, st.bgSet = c, true
			}
		}
	}
	return st
}

// extendedColorArgs parses the arguments following a semicolon form 38,
// 48 or 58 ("5;n" or "2;r;g;b") and reports how many tokens it used.
func extendedColorArgs(args []string, p *theme.Palette) (theme.RGB, bool, int) {
	if len(args) == 0 {
		return theme.RGB{}, false, 0
	}
	switch args[0] {
	case "5":
		if len(args) < 2 {
			return theme.RGB{}, false, len(args)
		}
		c, ok := extendedColor(args[:2], p)
		return c, ok, 2
	case "2":
		if len(args) < 4 {
			return theme.RGB{}, false, len(args)
		}
		c, ok := extendedColor(args[:4], p)
		return c, ok, 4
	}
	return theme.RGB{}, false, 1
}

// extendedColor parses colon sub-parameters: "5:n", "2:r:g:b" or
// "2:colorspace:r:g:b".
func extendedColor(parts []string, p *theme.Palette) (theme.RGB, bool) {
	nums := make([]int, 0, len(parts))
	for _, s := range parts {
		v, err := strconv.Atoi(s)
		if err != nil {
			v = -1
		}
		nums = append(nums, v)
	}
	switch {
	case len(nums) == 2 && nums[0] == 5 && nums[1] >= 0 && nums[1] <= 255:
		return ansi256(nums[1], p), true
	case len(nums) == 4 && nums[0] == 2:
		return rgb(nums[1], nums[2], nums[3])
	case len(nums) >= 5 && nums[0] == 2:
		return rgb(nums[2], nums[3], nums[4])
	}
	return theme.RGB{}, false
}

func rgb(r, g, b int) (theme.RGB, bool) {
	valid := func(v int) bool { return v >= 0 && v <= 255 }
	if !valid(r) || !valid(g) || !valid(b) {
		return theme.RGB{}, false
	}
	return theme.RGB{R: uint8(r), G: uint8(g), B: uint8(b)}, true
}

// ansi256 maps a 256-color index to RGB. The 16 base colors come from
// the theme palette so legacy-colored output matches the dashboard; the
// cube and grayscale ramp follow the xterm formulas.
func ansi256(n int, p *theme.Palette) theme.RGB {
	base := [16]theme.RGB{
		p.Black, p.Bad, p.Good, p.Warn, p.Blue, p.Accent, p.Accent2, p.Muted,
		p.Faint, p.Bad, p.Good, p.Warn, p.Blue, p.Accent, p.Teal, p.Text,
	}
	switch {
	case n < 16:
		return base[n]
	case n < 232:
		n -= 16
		level := func(v int) uint8 {
			if v == 0 {
				return 0
			}
			return uint8(55 + 40*v)
		}
		return theme.RGB{R: level(n / 36), G: level(n / 6 % 6), B: level(n % 6)}
	default:
		v := uint8(8 + 10*(n-232))
		return theme.RGB{R: v, G: v, B: v}
	}
}
