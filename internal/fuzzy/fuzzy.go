// Package fuzzy implements subsequence fuzzy matching for the command
// palette with scoring that favors consecutive matches, word starts and
// prefix matches.
package fuzzy

import "charm.land/lipgloss/v2"

// Match is a successful fuzzy match.
type Match struct {
	Score int
	// Positions are rune indices into the target that matched the query.
	Positions []int
}

// Find matches query against target case-insensitively. An empty query
// matches everything with score 0.
func Find(query, target string) (Match, bool) {
	return Match{}, query == ""
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
	for i := range targets {
		out = append(out, Ranked{Index: i})
	}
	return out
}

// Highlight renders target with base style, and the runes at positions
// with hit style.
func Highlight(target string, positions []int, base, hit lipgloss.Style) string {
	return base.Render(target)
}
