package compat

import "github.com/charmbracelet/x/ansi"

// Braille dot bits grouped by the half and column of the cell they sit in.
const (
	brailleTop   = 0x01 | 0x02 | 0x08 | 0x10
	brailleBot   = 0x04 | 0x20 | 0x40 | 0x80
	brailleLeft  = 0x01 | 0x02 | 0x04 | 0x40
	brailleRight = 0x08 | 0x10 | 0x20 | 0x80
)

// blocksTable maps glyphs missing from CP437/WGL4 console fonts to ones
// those fonts have. asciiTable maps every glyph vigil draws to ASCII.
// Both are built once and every replacement has the width of its glyph.
var (
	blocksTable = buildTable(blocksGlyphs(), blocksBraille)
	asciiTable  = buildTable(asciiGlyphs(), asciiBraille)
)

func buildTable(glyphs map[rune]string, braille func(mask int) string) map[rune]string {
	for mask := range 256 {
		glyphs[rune(0x2800+mask)] = braille(mask)
	}
	for r, repl := range glyphs {
		glyphs[r] = fitWidth(repl, ansi.StringWidth(string(r)))
	}
	return glyphs
}

func dotCount(mask int) int {
	n := 0
	for ; mask != 0; mask &= mask - 1 {
		n++
	}
	return n
}

// blocksBraille approximates a braille cell by half blocks when the dots
// sit in one half, and by shades of increasing density otherwise.
func blocksBraille(mask int) string {
	n := dotCount(mask)
	switch {
	case n == 0:
		return " "
	case mask&brailleBot == 0:
		return "▀"
	case mask&brailleTop == 0:
		return "▄"
	case mask&brailleRight == 0:
		return "▌"
	case mask&brailleLeft == 0:
		return "▐"
	case n <= 2:
		return "░"
	case n <= 4:
		return "▒"
	case n <= 6:
		return "▓"
	default:
		return "█"
	}
}

// asciiBraille maps dots in the upper half to a quote, the lower half to
// a period and both halves to a colon; dense cells become '#'.
func asciiBraille(mask int) string {
	switch {
	case mask == 0:
		return " "
	case dotCount(mask) >= 6:
		return "#"
	case mask&brailleBot == 0:
		return "'"
	case mask&brailleTop == 0:
		return "."
	default:
		return ":"
	}
}

// boxShapes lists the box-drawing glyphs by the light shape they reduce
// to. Rounded corners, heavy and mixed weights, dashes and half lines
// all collapse to the plain light set every console font has.
var boxShapes = map[rune]string{
	'─': "━╴╶╸╺╼╾",
	'│': "┃╵╷╹╻╽╿┆┇┊┋╎╏",
	'┌': "┍┎┏╭",
	'┐': "┑┒┓╮",
	'└': "┕┖┗╰",
	'┘': "┙┚┛╯",
	'├': "┝┞┟┠┡┢┣",
	'┤': "┥┦┧┨┩┪┫",
	'┬': "┭┮┯┰┱┲┳",
	'┴': "┵┶┷┸┹┺┻",
	'┼': "┽┾┿╀╁╂╃╄╅╆╇╈╉╊╋",
	'·': "┄┅┈┉╌╍",
}

func blocksGlyphs() map[rune]string {
	m := map[rune]string{
		'▁': " ", '▂': "▄", '▃': "▄", '▄': "▄", '▅': "▄", '▆': "█", '▇': "█",
		'▏': " ", '▎': "▌", '▍': "▌", '▋': "▌", '▊': "█", '▉': "█",
		'▔': "▀", '▕': "▐",
		'▖': "▄", '▗': "▄", '▘': "▀", '▝': "▀",
		'▙': "█", '▛': "█", '▜': "█", '▟': "█", '▚': "▒", '▞': "▒",
		'╱': "/", '╲': "\\", '╳': "X",
		'◉': "●", '◎': "○", '◈': "◊", '◆': "♦", '◇': "◊",
		'✱': "*", '✲': "*", '✳': "*", '✶': "*", '★': "*", '☆': "*",
		'⚡': "!", '⚠': "!", '✓': "√", '✔': "√", '✗': "x", '✘': "x",
		'▶': "►", '▸': "►", '◀': "◄", '◂': "◄",
		'△': "▲", '▴': "▲", '▽': "▼", '▾': "▼",
		'⏎': "<", '↵': "<",
	}
	for light, heavy := range boxShapes {
		for _, r := range heavy {
			m[r] = string(light)
		}
	}
	return m
}

func asciiGlyphs() map[rune]string {
	m := map[rune]string{
		'─': "-", '│': "|", '·': ".",
		'═': "=", '║': "|",
		'╱': "/", '╲': "\\", '╳': "X",
		'▁': ".", '▂': ".", '▃': ":", '▄': ":", '▅': ":", '▆': "#", '▇': "#", '█': "#",
		'▏': ".", '▎': ".", '▍': ":", '▌': ":", '▋': ":", '▊': "#", '▉': "#",
		'▀': "\"", '▐': ":", '▔': "'", '▕': "|",
		'░': ".", '▒': ":", '▓': "#",
		'▖': ".", '▗': ".", '▘': "'", '▝': "'",
		'▙': "#", '▛': "#", '▜': "#", '▟': "#", '▚': ":", '▞': ":",
		'●': "*", '○': "o", '◉': "@", '◎': "@", '◈': "*", '◆': "*", '◇': "o",
		'♦': "*", '◊': "o", '■': "#", '□': "o", '▪': "*", '▫': ".",
		'•': "*", '∙': ".", '◦': "o",
		'✱': "*", '✲': "*", '✳': "*", '✶': "*", '★': "*", '☆': "*",
		'≡': "=", '⚡': "!", '⚠': "!", '✓': "v", '✔': "v", '√': "v",
		'✗': "x", '✘': "x", '✕': "x", '✖': "x",
		'▲': "^", '△': "^", '▴': "^", '▼': "v", '▽': "v", '▾': "v",
		'►': ">", '▶': ">", '▸': ">", '◄': "<", '◀': "<", '◂': "<",
		'›': ">", '‹': "<", '»': ">", '«': "<",
		'↑': "^", '↓': "v", '→': ">", '←': "<", '↔': "-", '↕': "|",
		'⏎': "<", '↵': "<",
		'…': "~", '⋯': "~",
		'°': "o", '×': "x", '÷': "/", '±': "+", '≈': "~",
		'≤': "<", '≥': ">", '≠': "#",
		'–': "-", '—': "-", '‘': "'", '’': "'", '“': "\"", '”': "\"",
		'µ': "u", '\u00a0': " ",
	}
	for light, heavy := range boxShapes {
		ascii := "+"
		switch light {
		case '─':
			ascii = "-"
		case '│':
			ascii = "|"
		case '·':
			ascii = "."
		}
		m[light] = ascii
		for _, r := range heavy {
			m[r] = ascii
		}
	}
	for r := rune(0x2500); r <= 0x257F; r++ {
		if _, ok := m[r]; !ok {
			m[r] = "+"
		}
	}
	return m
}
