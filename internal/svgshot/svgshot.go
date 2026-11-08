// Package svgshot turns an ANSI terminal frame, as printed by
// `vigil render`, into a standalone SVG "terminal screenshot".
//
// Every element is positioned by its cell column and row, so the result
// lines up regardless of the viewer's font metrics: text runs are
// stretched to their exact cell width with textLength, wide graphemes
// take two cells, and block elements (█ ▀ ▄ ▌ ▐, eighth blocks, shades
// and quadrants) are drawn as rectangles so bars and charts are seamless.
package svgshot

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Aepzie/vigil/internal/theme"
)

// FontFamily is the monospace font stack of the terminal text.
const FontFamily = `"JetBrains Mono","Cascadia Mono","DejaVu Sans Mono",Menlo,Consolas,monospace`

// Default geometry.
const (
	DefaultFontSize = 14
	// cellWidthEm and cellHeightEm give 8.4×17 px cells at 14 px.
	cellWidthEm  = 0.6
	cellHeightEm = 17.0 / 14.0

	defaultPadding = 16
	titleBarHeight = 32
	cornerRadius   = 10
	dotRadius      = 6
	dotSpacing     = 20
	titleFontSize  = 13
)

// Options configures Render. The zero value renders with the default
// theme, 14 px text and 8.4×17 px cells.
type Options struct {
	// Title is shown centered in the window title bar and used as the
	// SVG <title>. Empty means no title text.
	Title string
	// Theme supplies the window and default colors; nil means
	// theme.Default().
	Theme *theme.Theme
	// FontSize is the text size in px (default 14).
	FontSize float64
	// CellWidth and CellHeight are the cell size in px (default
	// 0.6×FontSize by FontSize×17/14).
	CellWidth, CellHeight float64
	// Padding is the space between the window edge and the cells in px
	// (default 16).
	Padding float64
}

func (o Options) withDefaults() Options {
	if o.Theme == nil {
		o.Theme = theme.Default()
	}
	if o.FontSize <= 0 {
		o.FontSize = DefaultFontSize
	}
	if o.CellWidth <= 0 {
		o.CellWidth = o.FontSize * cellWidthEm
	}
	if o.CellHeight <= 0 {
		o.CellHeight = math.Round(o.FontSize * cellHeightEm)
	}
	if o.Padding <= 0 {
		o.Padding = defaultPadding
	}
	return o
}

// ErrEmpty is returned for a frame without any cells.
var ErrEmpty = errors.New("svgshot: empty frame")

// Render converts an ANSI frame into an SVG document. Lines are separated
// by \n; SGR sequences (truecolor, 256 and 16 colors, bold, faint,
// italic, underline, strikethrough, reverse and their resets) are
// honored, everything else is ignored.
func Render(frame string, opts Options) ([]byte, error) {
	opts = opts.withDefaults()
	p := &opts.Theme.P
	g := parseFrame(frame, p)
	if g.cols == 0 {
		return nil, ErrEmpty
	}
	r := renderer{opts: opts, p: p, g: g, classes: map[textStyle]string{}}
	return r.render(), nil
}

// renderer holds the state of one Render call.
type renderer struct {
	opts    Options
	p       *theme.Palette
	g       grid
	classes map[textStyle]string
}

// rect is a filled rectangle in eighths of a cell (x in cell widths/8,
// y in cell heights/8).
type rect struct {
	x, y, w, h int
	fill       theme.RGB
	opacity    float64
}

// textStyle is everything that distinguishes two text runs.
type textStyle struct {
	fg        theme.RGB
	bold      bool
	faint     bool
	italic    bool
	underline bool
	strike    bool
}

// textRun is a horizontal span of same-styled cells.
type textRun struct {
	row, col, cells int
	text            string
	st              textStyle
}

// colors returns the effective foreground and background of a style.
func (r *renderer) colors(st style) (fg, bg theme.RGB) {
	fg, bg = r.p.Text, r.p.Bg
	if st.fgSet {
		fg = st.fg
	}
	if st.bgSet {
		bg = st.bg
	}
	if st.reverse {
		fg, bg = bg, fg
	}
	return fg, bg
}

func (r *renderer) render() []byte {
	o := r.opts
	cols, rows := r.g.cols, len(r.g.rows)
	width := 2*o.Padding + float64(cols)*o.CellWidth
	height := titleBarHeight + 2*o.Padding + float64(rows)*o.CellHeight

	bgRects, blockRects, runs := r.layout()
	css := r.styleSheet(runs)

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %[1]s %[2]s" role="img"`, num(width), num(height))
	if o.Title != "" {
		fmt.Fprintf(&b, ` aria-label="%s"`, escape(o.Title))
	}
	b.WriteString(">\n")
	if o.Title != "" {
		fmt.Fprintf(&b, "<title>%s</title>\n", escape(o.Title))
	}
	fmt.Fprintf(&b, "<style>%s</style>\n", css)
	r.writeWindow(&b, width, height)

	b.WriteString(`<g shape-rendering="crispEdges">` + "\n")
	for _, rc := range mergeRects(bgRects) {
		r.writeRect(&b, rc)
	}
	for _, rc := range mergeRects(blockRects) {
		r.writeRect(&b, rc)
	}
	b.WriteString("</g>\n")

	b.WriteString(`<g class="t">` + "\n")
	for _, run := range runs {
		r.writeText(&b, run)
	}
	b.WriteString("</g>\n")

	fmt.Fprintf(&b, `<rect x=".5" y=".5" width="%s" height="%s" rx="%s" fill="none" stroke="%s"/>`+"\n",
		num(width-1), num(height-1), num(cornerRadius-0.5), r.p.Border)
	b.WriteString("</svg>\n")
	return b.Bytes()
}

// writeWindow draws the window background, title bar, dots and title.
func (r *renderer) writeWindow(b *bytes.Buffer, width, height float64) {
	p := r.p
	fmt.Fprintf(b, `<rect width="%s" height="%s" rx="%d" fill="%s"/>`+"\n", num(width), num(height), cornerRadius, p.Bg)
	fmt.Fprintf(b, `<path d="M0 %d V%d A%d %d 0 0 1 %d 0 H%s A%d %d 0 0 1 %s %d V%d Z" fill="%s"/>`+"\n",
		titleBarHeight, cornerRadius, cornerRadius, cornerRadius, cornerRadius,
		num(width-cornerRadius), cornerRadius, cornerRadius, num(width), cornerRadius, titleBarHeight, p.Surface)
	fmt.Fprintf(b, `<rect y="%d" width="%s" height="1" fill="%s"/>`+"\n", titleBarHeight, num(width), p.Border)
	for i := range 3 {
		fmt.Fprintf(b, `<circle cx="%d" cy="%d" r="%d" fill="%s" stroke="%s"/>`+"\n",
			dotSpacing*(i+1)-2, titleBarHeight/2, dotRadius, p.Border, p.Faint)
	}
	if r.opts.Title != "" {
		fmt.Fprintf(b, `<text x="%s" y="%d" text-anchor="middle" font-family='%s' font-size="%d" fill="%s">%s</text>`+"\n",
			num(width/2), titleBarHeight/2+titleFontSize*7/20, FontFamily, titleFontSize, p.Muted, escape(r.opts.Title))
	}
}

// layout converts the grid into background rects, block rects and text
// runs, in row-major order.
func (r *renderer) layout() (bgRects, blockRects []rect, runs []textRun) {
	for y, row := range r.g.rows {
		var cur *textRun
		pendingSpaces := 0
		flush := func() {
			if cur != nil {
				runs = append(runs, *cur)
			}
			cur, pendingSpaces = nil, 0
		}
		for x := 0; x < len(row); x++ {
			c := row[x]
			if c.cont {
				continue
			}
			fg, bg := r.colors(c.st)
			width := max(c.width, 1)
			if bg != r.p.Bg {
				bgRects = append(bgRects, rect{x: x * eighths, y: y * eighths, w: width * eighths, h: eighths, fill: bg, opacity: 1})
			}
			if shape, ok := blockGeometry(c.glyph); ok {
				flush()
				for _, bx := range shape.boxes {
					blockRects = append(blockRects, rect{
						x: x*eighths + bx.x, y: y*eighths + bx.y, w: bx.w, h: bx.h,
						fill: fg, opacity: shape.opacity,
					})
				}
				continue
			}
			st := textStyle{fg: fg, bold: c.st.bold, faint: c.st.faint, italic: c.st.italic, underline: c.st.underline, strike: c.st.strike}
			blank := strings.TrimSpace(c.glyph) == ""
			if blank && !st.underline && !st.strike {
				if cur != nil {
					pendingSpaces++
				}
				continue
			}
			glyph := c.glyph
			if blank {
				glyph = " "
			}
			if cur != nil && cur.st == st && width == 1 {
				cur.text += strings.Repeat(" ", pendingSpaces) + glyph
				cur.cells += pendingSpaces + 1
				pendingSpaces = 0
				continue
			}
			flush()
			cur = &textRun{row: y, col: x, cells: width, text: glyph, st: st}
			if width > 1 {
				flush()
			}
		}
		flush()
	}
	return bgRects, blockRects, runs
}

// mergeRects joins horizontally adjacent rects of the same row band and
// then vertically stacked rects of the same column span.
func mergeRects(in []rect) []rect {
	var horiz []rect
	for _, rc := range in {
		if n := len(horiz); n > 0 {
			last := &horiz[n-1]
			if last.y == rc.y && last.h == rc.h && last.fill == rc.fill && last.opacity == rc.opacity && last.x+last.w == rc.x {
				last.w += rc.w
				continue
			}
		}
		horiz = append(horiz, rc)
	}
	type key struct {
		x, w    int
		fill    theme.RGB
		opacity float64
	}
	open := map[key]int{}
	var out []rect
	for _, rc := range horiz {
		k := key{rc.x, rc.w, rc.fill, rc.opacity}
		if i, ok := open[k]; ok && out[i].y+out[i].h == rc.y {
			out[i].h += rc.h
			continue
		}
		open[k] = len(out)
		out = append(out, rc)
	}
	return out
}

func (r *renderer) writeRect(b *bytes.Buffer, rc rect) {
	o := r.opts
	x := o.Padding + float64(rc.x)*o.CellWidth/eighths
	y := titleBarHeight + o.Padding + float64(rc.y)*o.CellHeight/eighths
	w := float64(rc.w) * o.CellWidth / eighths
	h := float64(rc.h) * o.CellHeight / eighths
	fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"`, num(x), num(y), num(w), num(h), rc.fill)
	if rc.opacity < 1 {
		fmt.Fprintf(b, ` fill-opacity="%s"`, num(rc.opacity))
	}
	b.WriteString("/>\n")
}

func (r *renderer) writeText(b *bytes.Buffer, run textRun) {
	o := r.opts
	x := o.Padding + float64(run.col)*o.CellWidth
	y := titleBarHeight + o.Padding + float64(run.row)*o.CellHeight + r.baseline()
	fmt.Fprintf(b, `<text x="%s" y="%s" textLength="%s" lengthAdjust="spacingAndGlyphs" xml:space="preserve" class="%s">%s</text>`+"\n",
		num(x), num(y), num(float64(run.cells)*o.CellWidth), r.classes[run.st], escape(run.text))
}

// baseline is the text baseline offset within a cell: the cap height
// (about 0.7em) centered vertically.
func (r *renderer) baseline() float64 {
	return (r.opts.CellHeight + 0.7*r.opts.FontSize) / 2
}

// styleSheet assigns a class list to every text style used by runs and
// returns the CSS.
func (r *renderer) styleSheet(runs []textRun) string {
	colorClass := map[theme.RGB]string{}
	var css strings.Builder
	fmt.Fprintf(&css, ".t{font-family:%s;font-size:%spx}", FontFamily, num(r.opts.FontSize))
	css.WriteString(".b{font-weight:700}.i{font-style:italic}.f{opacity:.6}")
	css.WriteString(".u{text-decoration:underline}.s{text-decoration:line-through}.u.s{text-decoration:underline line-through}")
	for _, run := range runs {
		if _, ok := r.classes[run.st]; ok {
			continue
		}
		cc, ok := colorClass[run.st.fg]
		if !ok {
			cc = "c" + strconv.Itoa(len(colorClass))
			colorClass[run.st.fg] = cc
			fmt.Fprintf(&css, ".%s{fill:%s}", cc, run.st.fg)
		}
		names := []string{cc}
		for _, m := range []struct {
			on   bool
			name string
		}{{run.st.bold, "b"}, {run.st.italic, "i"}, {run.st.faint, "f"}, {run.st.underline, "u"}, {run.st.strike, "s"}} {
			if m.on {
				names = append(names, m.name)
			}
		}
		r.classes[run.st] = strings.Join(names, " ")
	}
	return css.String()
}

// num formats a coordinate with at most two decimals.
func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// escape makes s safe for XML text and attribute values. Characters that
// XML 1.0 does not allow are replaced with U+FFFD.
func escape(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c == '"':
			b.WriteString("&quot;")
		case c == '\'':
			b.WriteString("&apos;")
		case !xmlChar(c):
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// xmlChar reports whether c is allowed in XML 1.0 documents.
func xmlChar(c rune) bool {
	return c == 0x09 || c == 0x0a || c == 0x0d ||
		c >= 0x20 && c <= 0xd7ff ||
		c >= 0xe000 && c <= 0xfffd ||
		c >= 0x10000 && c <= 0x10ffff
}
