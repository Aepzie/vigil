package widgets

import (
	"strings"

	"github.com/CraguS/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// Overlay draws the block fg on top of the block bg with its top-left
// corner at column x, row y, preserving bg content (and its styling)
// outside fg. bg's dimensions are preserved: fg is clipped at every edge
// and negative offsets are allowed. The SGR state is reset around the
// inserted part and the bg line's active SGR state is re-emitted after
// it. Wide characters cut by an fg edge are replaced with spaces.
func Overlay(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	for j, fl := range strings.Split(fg, "\n") {
		row := y + j
		if row < 0 || row >= len(bgLines) {
			continue
		}
		bl := bgLines[row]
		bw := ansi.StringWidth(bl)
		x0, x1 := max(x, 0), min(x+ansi.StringWidth(fl), bw)
		if x1 <= x0 {
			continue
		}
		var sb strings.Builder
		left := sliceCells(bl, 0, x0, false)
		sb.WriteString(left)
		if strings.Contains(left, "\x1b") {
			sb.WriteString(theme.Reset)
		}
		mid := sliceCells(fl, x0-x, x1-x, true)
		sb.WriteString(mid)
		if strings.Contains(mid, "\x1b") {
			sb.WriteString(theme.Reset)
		}
		sb.WriteString(sliceCells(bl, x1, bw, true))
		bgLines[row] = sb.String()
	}
	return strings.Join(bgLines, "\n")
}

// sliceCells returns cells [from, to) of the single line s as exactly
// to-from cells. It starts with the SGR state active at from, keeps
// escape sequences inside the range (and trailing ones after the last
// cell when tail is set), replaces wide characters cut by either edge
// with spaces and pads with spaces when s is too short.
func sliceCells(s string, from, to int, tail bool) string {
	if to <= from {
		return ""
	}
	var out strings.Builder
	var active []string
	started := false
	start := func() {
		if started {
			return
		}
		started = true
		for _, seq := range active {
			out.WriteString(seq)
		}
	}
	col := 0
	var state byte
	for len(s) > 0 {
		seq, width, n, next := ansi.DecodeSequence(s, state, nil)
		state = next
		s = s[n:]
		if n == 0 {
			break
		}
		if width == 0 {
			switch {
			case col < from:
				active = trackSGR(active, seq)
			case col < to || tail:
				start()
				out.WriteString(seq)
			}
			continue
		}
		end := col + width
		switch {
		case end <= from || col >= to:
		case col >= from && end <= to:
			start()
			out.WriteString(seq)
		default:
			start()
			out.WriteString(strings.Repeat(" ", min(end, to)-max(col, from)))
		}
		col = end
	}
	if col < to {
		start()
		out.WriteString(strings.Repeat(" ", to-max(col, from)))
	}
	return out.String()
}

// trackSGR folds seq into the list of SGR sequences active since the last
// full reset. Non-SGR sequences are ignored.
func trackSGR(active []string, seq string) []string {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return active
	}
	params := seq[2 : len(seq)-1]
	if params == "" || params == "0" {
		return active[:0]
	}
	if strings.HasPrefix(params, "0;") {
		active = active[:0]
	}
	return append(active, seq)
}
