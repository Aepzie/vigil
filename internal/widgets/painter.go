package widgets

import (
	"math"
	"strings"

	"github.com/CraguS/vigil/internal/theme"
)

// painter writes cells with raw SGR sequences, emitting a color change
// only when it differs from the current state so that runs of equally
// styled cells cost a single escape sequence.
type painter struct {
	b      strings.Builder
	fg, bg theme.RGB
	hasFG  bool
	hasBG  bool
	cache  map[theme.RGB][2]string
}

func newPainter(capacity int) *painter {
	p := &painter{cache: make(map[theme.RGB][2]string, 16)}
	p.b.Grow(capacity)
	return p
}

// seq returns the cached foreground and background sequences of c.
func (p *painter) seq(c theme.RGB) (fg, bg string) {
	s, ok := p.cache[c]
	if !ok {
		s = [2]string{c.FG(), c.BG()}
		p.cache[c] = s
	}
	return s[0], s[1]
}

func (p *painter) reset() {
	if p.hasFG || p.hasBG {
		p.b.WriteString(theme.Reset)
	}
	p.hasFG, p.hasBG = false, false
}

func (p *painter) setFG(c theme.RGB) {
	if p.hasFG && p.fg == c {
		return
	}
	fg, _ := p.seq(c)
	p.b.WriteString(fg)
	p.fg, p.hasFG = c, true
}

func (p *painter) setBG(c theme.RGB) {
	if p.hasBG && p.bg == c {
		return
	}
	_, bg := p.seq(c)
	p.b.WriteString(bg)
	p.bg, p.hasBG = c, true
}

// dropBG clears an active background while keeping the foreground.
func (p *painter) dropBG() {
	if !p.hasBG {
		return
	}
	fg, had := p.fg, p.hasFG
	p.reset()
	if had {
		p.setFG(fg)
	}
}

// fgCell writes r in foreground c on the default background.
func (p *painter) fgCell(r rune, c theme.RGB) {
	p.dropBG()
	p.setFG(c)
	p.b.WriteRune(r)
}

// fgStr writes s in foreground c on the default background.
func (p *painter) fgStr(s string, c theme.RGB) {
	p.dropBG()
	p.setFG(c)
	p.b.WriteString(s)
}

// duoCell writes r in foreground fg on background bg.
func (p *painter) duoCell(r rune, fg, bg theme.RGB) {
	p.setFG(fg)
	p.setBG(bg)
	p.b.WriteRune(r)
}

// space writes an unstyled blank cell. The foreground is irrelevant for
// a space, so only an active background forces a reset.
func (p *painter) space() {
	p.dropBG()
	p.b.WriteByte(' ')
}

// spaces writes n unstyled blank cells.
func (p *painter) spaces(n int) {
	if n <= 0 {
		return
	}
	p.dropBG()
	for range n {
		p.b.WriteByte(' ')
	}
}

// newline closes any open style and starts a new line.
func (p *painter) newline() {
	p.reset()
	p.b.WriteByte('\n')
}

func (p *painter) String() string {
	p.reset()
	return p.b.String()
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func clamp01(v float64) float64 {
	switch {
	case !(v > 0):
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
