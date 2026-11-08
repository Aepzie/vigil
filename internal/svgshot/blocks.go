package svgshot

import "unicode/utf8"

// eighths is the sub-cell resolution of block geometry.
const eighths = 8

// box is a rectangle inside one cell in eighths of the cell size.
type box struct{ x, y, w, h int }

// blockShape is the exact geometry of a block element glyph, drawn in the
// foreground color with the given opacity.
type blockShape struct {
	boxes   []box
	opacity float64
}

var (
	upperLeft  = box{0, 0, 4, 4}
	upperRight = box{4, 0, 4, 4}
	lowerLeft  = box{0, 4, 4, 4}
	lowerRight = box{4, 4, 4, 4}
)

// quadrants maps U+2596..U+259F to their filled quadrants.
var quadrants = [...][]box{
	{lowerLeft},
	{lowerRight},
	{upperLeft},
	{upperLeft, lowerLeft, lowerRight},
	{upperLeft, lowerRight},
	{upperLeft, upperRight, lowerLeft},
	{upperLeft, upperRight, lowerRight},
	{upperRight},
	{upperRight, lowerLeft},
	{upperRight, lowerLeft, lowerRight},
}

// blockGeometry returns the shape of a block element glyph (U+2580 to
// U+259F), or false for every other grapheme.
func blockGeometry(glyph string) (blockShape, bool) {
	r, size := utf8.DecodeRuneInString(glyph)
	if size != len(glyph) || r < 0x2580 || r > 0x259f {
		return blockShape{}, false
	}
	one := func(b box) (blockShape, bool) { return blockShape{boxes: []box{b}, opacity: 1}, true }
	switch {
	case r == 0x2580:
		return one(box{0, 0, 8, 4})
	case r <= 0x2588:
		n := int(r - 0x2580)
		return one(box{0, eighths - n, 8, n})
	case r <= 0x258f:
		return one(box{0, 0, int(0x2590 - r), 8})
	case r == 0x2590:
		return one(box{4, 0, 4, 8})
	case r <= 0x2593:
		return blockShape{boxes: []box{{0, 0, 8, 8}}, opacity: float64(r-0x2590) / 4}, true
	case r == 0x2594:
		return one(box{0, 0, 8, 1})
	case r == 0x2595:
		return one(box{7, 0, 1, 8})
	default:
		return blockShape{boxes: quadrants[r-0x2596], opacity: 1}, true
	}
}
