package widgets

import (
	"strings"

	"github.com/CraguS/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// bigGlyphs are 3-row seven-segment style glyphs drawn with heavy box
// characters. All rows of a glyph have the same width.
var bigGlyphs = map[rune][3]string{
	'0': {"┏━┓", "┃ ┃", "┗━┛"},
	'1': {"╺┓ ", " ┃ ", "╺┻╸"},
	'2': {"╺━┓", "┏━┛", "┗━╸"},
	'3': {"╺━┓", "╺━┫", "╺━┛"},
	'4': {"╻ ╻", "┗━┫", "  ╹"},
	'5': {"┏━╸", "┗━┓", "╺━┛"},
	'6': {"┏━╸", "┣━┓", "┗━┛"},
	'7': {"╺━┓", "  ┃", "  ╹"},
	'8': {"┏━┓", "┣━┫", "┗━┛"},
	'9': {"┏━┓", "┗━┫", "╺━┛"},
	'.': {" ", " ", "╻"},
	',': {" ", " ", "┃"},
	'-': {"   ", "╺━╸", "   "},
	'%': {"╻ ╱", " ╱ ", "╱ ╹"},
	'/': {"  ╱", " ╱ ", "╱  "},
	'k': {"╻ ", "┃╱", "┃╲"},
	'K': {"┃ ╱", "┣━ ", "┃ ╲"},
	'M': {"┏┳┓", "┃╹┃", "╹ ╹"},
	'G': {"┏━╸", "┃╺┓", "┗━┛"},
	'T': {"╺┳╸", " ┃ ", " ╹ "},
	'B': {"┏┓ ", "┣┻┓", "┗━┛"},
	's': {"   ", "┏━╸", "╺━┛"},
	' ': {" ", " ", " "},
}

// bigAliases maps runes onto glyphs that render them.
var bigAliases = map[rune]rune{
	'm': 'M', 'g': 'G', 't': 'T', 'b': 'B', 'S': 's',
}

// bigGap is the blank column between two wide glyphs.
const bigGap = 1

// bigShade is how much the bottom row of a big number is darkened.
const bigShade = 0.3

// glyphFor returns the glyph of r, or a single blank column for
// unsupported runes.
func glyphFor(r rune) [3]string {
	if alias, ok := bigAliases[r]; ok {
		r = alias
	}
	if g, ok := bigGlyphs[r]; ok {
		return g
	}
	return bigGlyphs[' ']
}

// narrowGlyph reports whether g is a one-column glyph that sits tight
// against its neighbors without a gap.
func narrowGlyph(g [3]string) bool {
	return ansi.StringWidth(g[0]) == 1
}

// BigNumber renders text as 3-line tall 7-segment digits drawn with heavy
// box characters (┏━┓ ┃ ┃ ┗━┛). Supported runes: 0-9 . , - % k K M G T B
// / s and space (m g t b S are aliases); others render as a blank column.
// Wide glyphs are separated by one blank column; '.', ',' and space are a
// single column wide. The bottom row is slightly darker for depth. Output
// lines all have width BigNumberWidth(text).
func BigNumber(text string, c theme.RGB) string {
	w := BigNumberWidth(text)
	if w == 0 {
		return Blank(0, 3)
	}
	rows := [3]strings.Builder{}
	var prev [3]string
	for i, r := range []rune(text) {
		g := glyphFor(r)
		if i > 0 && !narrowGlyph(g) && !narrowGlyph(prev) {
			for row := range rows {
				rows[row].WriteString(strings.Repeat(" ", bigGap))
			}
		}
		for row := range rows {
			rows[row].WriteString(g[row])
		}
		prev = g
	}
	colors := [3]theme.RGB{c, c, c.Darken(bigShade)}
	lines := make([]string, 3)
	for row := range rows {
		lines[row] = colors[row].FG() + rows[row].String() + theme.Reset
	}
	return strings.Join(lines, "\n")
}

// BigNumberWidth is the cell width BigNumber will produce for text.
func BigNumberWidth(text string) int {
	w := 0
	var prev [3]string
	for i, r := range []rune(text) {
		g := glyphFor(r)
		if i > 0 && !narrowGlyph(g) && !narrowGlyph(prev) {
			w += bigGap
		}
		w += ansi.StringWidth(g[0])
		prev = g
	}
	return w
}
