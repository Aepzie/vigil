// Package fuzzy implements subsequence fuzzy matching for the command
// palette with scoring that favors consecutive matches, word starts and
// prefix matches.
package fuzzy

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// Scoring weights. Every matched rune earns scoreMatch plus the boundary
// bonus of its position. A rune directly after the previous match pays no
// gap penalty and earns at least bonusConsecutive, inheriting the bonus
// of the run's first rune so a whole word matched from its start keeps
// its boundary bonus.
const (
	scoreMatch       = 16
	bonusPrefix      = 24
	bonusWordStart   = 16
	bonusCamel       = 12
	bonusConsecutive = 12
	penaltyGapStart  = 3
	penaltyGapExtend = 1
	penaltyLate      = 1
	maxLatePenalty   = 12
	impossible       = -1 << 30
)

// Match is a successful fuzzy match.
type Match struct {
	Score int
	// Positions are rune indices into the target that matched the query.
	Positions []int
}

// Find matches query against target case-insensitively. An empty query
// matches everything with score 0. Among all alignments of the query in
// the target it returns the highest scoring one: consecutive runs, word
// starts (after a space, -, _, /, . or other non-alphanumeric rune, or at
// a camelCase hump) and a match at index 0 score higher, while gaps
// between matched runes and a late first match cost points.
func Find(query, target string) (Match, bool) {
	q := foldRunes(query)
	if len(q) == 0 {
		return Match{}, true
	}
	raw := []rune(target)
	t := foldRunes(target)
	if len(q) > len(t) || !isSubsequence(q, t) {
		return Match{}, false
	}
	n, m := len(q), len(t)
	bonus := boundaryBonuses(raw)
	cells := n * m
	buf := make([]int, 3*cells)
	score, from, carry := buf[:cells], buf[cells:2*cells], buf[2*cells:]
	for j := range m {
		score[j] = impossible
		if t[j] == q[0] {
			score[j] = scoreMatch + bonus[j] - min(j*penaltyLate, maxLatePenalty)
			carry[j] = bonus[j]
		}
	}
	for i := 1; i < n; i++ {
		row, prevRow := i*m, (i-1)*m
		gapBest, gapFrom := impossible, -1
		for j := range m {
			score[row+j] = impossible
			if j >= 2 {
				gapBest -= penaltyGapExtend
				if k := prevRow + j - 2; score[k] > impossible && score[k]-penaltyGapStart > gapBest {
					gapBest, gapFrom = score[k]-penaltyGapStart, j-2
				}
			}
			if j < i || t[j] != q[i] {
				continue
			}
			best, link, runBonus := impossible, -1, bonus[j]
			if gapFrom >= 0 {
				best, link = gapBest+scoreMatch+bonus[j], gapFrom
			}
			if k := prevRow + j - 1; j >= 1 && score[k] > impossible {
				inherited := max(carry[k], bonus[j])
				if c := score[k] + scoreMatch + max(inherited, bonusConsecutive); c >= best {
					best, link, runBonus = c, j-1, inherited
				}
			}
			if link < 0 {
				continue
			}
			score[row+j], from[row+j], carry[row+j] = best, link, runBonus
		}
	}
	last := score[(n-1)*m:]
	end := 0
	for j := range last {
		if last[j] > last[end] {
			end = j
		}
	}
	if last[end] <= impossible/2 {
		return Match{}, false
	}
	positions := make([]int, n)
	for i, j := n-1, end; i >= 0; i-- {
		positions[i] = j
		j = from[i*m+j]
	}
	return Match{Score: last[end], Positions: positions}, true
}

// foldRunes lower-cases s rune by rune so indices stay aligned with the
// original runes.
func foldRunes(s string) []rune {
	runes := []rune(s)
	for i, r := range runes {
		runes[i] = unicode.ToLower(r)
	}
	return runes
}

func isSubsequence(q, t []rune) bool {
	i := 0
	for _, r := range t {
		if i < len(q) && r == q[i] {
			i++
		}
	}
	return i == len(q)
}

// boundaryBonuses scores how much each rune position looks like the
// start of a word.
func boundaryBonuses(runes []rune) []int {
	out := make([]int, len(runes))
	for j, r := range runes {
		if j == 0 {
			out[j] = bonusPrefix
			continue
		}
		prev := runes[j-1]
		switch {
		case !alnum(prev) && alnum(r):
			out[j] = bonusWordStart
		case unicode.IsLower(prev) && unicode.IsUpper(r):
			out[j] = bonusCamel
		case unicode.IsLetter(prev) && unicode.IsDigit(r):
			out[j] = bonusCamel / 2
		}
	}
	return out
}

func alnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Ranked is a match result referencing the target's index.
type Ranked struct {
	Index int
	Match
}

// Rank returns all targets that match query, best score first; ties keep
// the original order.
func Rank(query string, targets []string) []Ranked {
	out := make([]Ranked, 0, len(targets))
	for i, target := range targets {
		if m, ok := Find(query, target); ok {
			out = append(out, Ranked{Index: i, Match: m})
		}
	}
	slices.SortStableFunc(out, func(a, b Ranked) int { return cmp.Compare(b.Score, a.Score) })
	return out
}

// Highlight renders target with base style, and the runes at positions
// with hit style. Contiguous runs of equally styled runes are rendered
// together; out-of-range positions are ignored.
func Highlight(target string, positions []int, base, hit lipgloss.Style) string {
	runes := []rune(target)
	if len(runes) == 0 {
		return ""
	}
	hits := make([]bool, len(runes))
	for _, p := range positions {
		if p >= 0 && p < len(runes) {
			hits[p] = true
		}
	}
	var sb strings.Builder
	start := 0
	for i := 1; i <= len(runes); i++ {
		if i < len(runes) && hits[i] == hits[start] {
			continue
		}
		style := base
		if hits[start] {
			style = hit
		}
		sb.WriteString(style.Render(string(runes[start:i])))
		start = i
	}
	return sb.String()
}
