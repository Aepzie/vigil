package widgets

import (
	"math"

	"github.com/CraguS/vigil/internal/theme"
)

// lowerBlocks indexes the lower eighth blocks by filled eighths (0..8).
var lowerBlocks = [9]rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

const (
	gridRune = '┈'
	eps      = 1e-9
	// minFill is the smallest cell fraction worth drawing (half an eighth).
	minFill = 1.0 / 16
)

// AreaChart renders stacked bands of ▁▂▃▄▅▆▇█ with 1/8-cell vertical
// resolution, a light-at-top to darker-at-base gradient per band, dotted
// ┈ grid lines, a nice-scaled Y axis and a time X axis. Output is exactly
// Width×Height cells.
func AreaChart(t *theme.Theme, o ChartOpts) string {
	w := max0(o.Width)
	frame := newChartFrame(o, stackedMax(o.Series, w))
	if frame.plotW < w {
		frame = newChartFrame(o, stackedMax(o.Series, frame.plotW))
	}
	if frame.plotW <= 0 || frame.plotH <= 0 {
		return frame.render(t, o.Step, 1, func(*painter, int) {})
	}
	a := newAreaPlot(o.Series, frame)
	return frame.render(t, o.Step, 1, a.drawRow(t))
}

// stackedMax is the largest stacked sum over the newest cols columns.
func stackedMax(series []Series, cols int) float64 {
	peak := 0.0
	for c := range cols {
		sum := 0.0
		for _, s := range series {
			v, _ := sampleAt(s.Values, c, cols)
			sum += positive(v)
		}
		peak = max(peak, sum)
	}
	return peak
}

// areaPlot holds per-column band boundaries in cell units and per-band,
// per-row gradient colors. Each band is brightest at its highest visible
// point and fades toward bandDarken at the chart floor, so the shade only
// depends on the row and equal cells in a row share one SGR sequence.
type areaPlot struct {
	frame chartFrame
	bands int
	// cum[c*(bands+1)+k] is the base height of band k in column c; the
	// entry for k == bands is the total height.
	cum   []float64
	shade [][]theme.RGB
}

func newAreaPlot(series []Series, f chartFrame) *areaPlot {
	a := &areaPlot{frame: f, bands: len(series)}
	stride := a.bands + 1
	a.cum = make([]float64, f.plotW*stride)
	scale := float64(f.plotH) / f.top
	bandTop := make([]float64, a.bands)
	for c := range f.plotW {
		base := c * stride
		for k, s := range series {
			v, _ := sampleAt(s.Values, c, f.plotW)
			lo := a.cum[base+k]
			hi := math.Min(lo+positive(v)*scale, float64(f.plotH))
			a.cum[base+k+1] = hi
			if hi-lo > eps {
				bandTop[k] = max(bandTop[k], hi)
			}
		}
	}
	a.shade = make([][]theme.RGB, a.bands)
	for k, s := range series {
		dark := s.Color.Darken(bandDarken)
		a.shade[k] = make([]theme.RGB, f.plotH)
		for row := range f.plotH {
			center := float64(f.plotH-row) - 0.5
			fade := 1.0
			if bandTop[k] > eps {
				fade = clamp01((bandTop[k] - center) / bandTop[k])
			}
			a.shade[k][row] = s.Color.Lerp(dark, fade)
		}
	}
	return a
}

// bandAt returns the band containing height h in column c, scanning from
// band from upward.
func (a *areaPlot) bandAt(c int, h float64, from int) int {
	base := c * (a.bands + 1)
	for k := from; k < a.bands; k++ {
		if a.cum[base+k+1] > h+eps {
			return k
		}
	}
	return a.bands - 1
}

func (a *areaPlot) drawRow(t *theme.Theme) func(p *painter, row int) {
	return func(p *painter, row int) {
		f := a.frame
		lo := float64(f.plotH - row - 1)
		hi := lo + 1
		for c := range f.plotW {
			a.drawCell(p, t, c, row, lo, hi)
		}
	}
}

func (a *areaPlot) drawCell(p *painter, t *theme.Theme, c, row int, lo, hi float64) {
	empty := func() {
		if a.frame.gridRow[row] {
			p.fgCell(gridRune, t.P.Border)
			return
		}
		p.space()
	}
	if a.bands == 0 {
		empty()
		return
	}
	stride := a.bands + 1
	total := a.cum[c*stride+a.bands]
	fill := math.Min(total, hi) - lo
	if fill < minFill {
		empty()
		return
	}
	bottom := a.bandAt(c, lo, 0)
	topBand := a.bandAt(c, lo+fill-eps*2, bottom)
	if bottom == topBand {
		eighths := int(math.Round(fill * 8))
		if eighths == 0 {
			empty()
			return
		}
		p.fgCell(lowerBlocks[eighths], a.shade[bottom][row])
		return
	}
	boundary := a.cum[c*stride+bottom+1]
	upper := a.bandAt(c, boundary, bottom+1)
	if total < hi-eps {
		lowerPart := boundary - lo
		dominant := bottom
		if fill-lowerPart > lowerPart {
			dominant = upper
		}
		p.fgCell(lowerBlocks[max(int(math.Round(fill*8)), 1)], a.shade[dominant][row])
		return
	}
	eighths := int(math.Round((boundary - lo) * 8))
	switch eighths {
	case 0:
		p.fgCell('█', a.shade[upper][row])
	case 8:
		p.fgCell('█', a.shade[bottom][row])
	default:
		p.duoCell(lowerBlocks[eighths], a.shade[bottom][row], a.shade[upper][row])
	}
}
