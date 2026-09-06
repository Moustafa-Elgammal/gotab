package core

import (
	"cmp"
	"slices"
	"unicode"
	"unicode/utf8"
)

// Scoring weights. The task contract fixes their relative ranking — an exact prefix outranks a word
// boundary, which outranks a consecutive run — so these magnitudes are the ranking, not tuning
// knobs. TestSearchScoreOrdering pins each of the three relationships; change a number and it fails.
const (
	// scoreRuneMatch is what one matched rune is worth before any structural bonus.
	scoreRuneMatch = 16
	// scoreExactPrefix is awarded once, for the whole query sitting consecutively at rune 0.
	scoreExactPrefix = 64
	// scoreWordBoundary is per rune that opens a word: after a separator, or a camelCase hump.
	scoreWordBoundary = 16
	// scoreConsecutive is per rune that directly follows the previous matched rune.
	scoreConsecutive = 8
	// scoreLeadingCap bounds the 1-per-rune charge for runes skipped before the first match. Capped
	// below scoreConsecutive so an early match can only ever break a tie between otherwise equal
	// matches, never outrank a structural bonus.
	scoreLeadingCap = 4
)

// Score matches query against text as a case-insensitive subsequence and ranks the match. ok is
// false when text does not contain query as a subsequence, in which case score is 0.
//
// Positions are rune indices into text, never byte offsets: titles are user text and full of
// multi-byte characters, so byte offsets would make the UI highlight the wrong characters.
// TestSearchNonASCIIPositions pins that.
//
// pos aliases positions' array — Score appends into positions[:0] so a caller can recycle one
// buffer per row and allocate nothing. A caller that keeps results from more than one call must
// therefore hand each call its own buffer.
//
// The match is found greedily left to right, then tightened by walking back from the last matched
// rune, which is what lands "ab" on the trailing pair of "a_ab" instead of straddling the gap. It is
// deliberately not an exhaustive search for the best-scoring window: once an early window is
// feasible a later, tighter one is not considered. Deterministic beats optimal here, because the
// ranking must not move under an unchanged query.
func Score(query, text string, positions []int) (score int, pos []int, ok bool) {
	pos = positions[:0]
	if query == "" {
		return 0, pos, true
	}

	// Forward pass: earliest rune of text at which query is exhausted.
	endByte, endRune := -1, -1
	qi, ri := 0, 0
	for bi, r := range text {
		qr, qsize := utf8.DecodeRuneInString(query[qi:])
		if searchFoldEqual(r, qr) {
			qi += qsize
			if qi == len(query) {
				endByte, endRune = bi, ri
				break
			}
		}
		ri++
	}
	if endByte < 0 {
		return 0, pos, false
	}

	// Backward pass: match query in reverse from endByte, which pulls the whole match as far right
	// as it goes and so maximises consecutive runs. Scoring happens here rather than in a third
	// sweep because the preceding rune, needed for the boundary test, is already in hand.
	qj := len(query)
	bj, rj := endByte, endRune
	later := -2 // rune index of the previously accepted match, which is always to the right
	startRune := 0
	for {
		r, _ := utf8.DecodeRuneInString(text[bj:])
		qr, qsize := utf8.DecodeLastRuneInString(query[:qj])
		if searchFoldEqual(r, qr) {
			qj -= qsize
			score += scoreRuneMatch
			if later == rj+1 {
				score += scoreConsecutive
			}
			if bj == 0 {
				score += scoreWordBoundary
			} else if prev, _ := utf8.DecodeLastRuneInString(text[:bj]); searchIsBoundary(prev, r) {
				score += scoreWordBoundary
			}
			pos = append(pos, rj)
			later = rj
			if qj == 0 {
				startRune = rj
				break
			}
		}
		if bj == 0 {
			// Unreachable: the forward pass already proved the subsequence exists, so qj reaches 0
			// first. Present so a future change to the forward pass fails loudly instead of
			// decoding off the front of text forever.
			return 0, positions[:0], false
		}
		_, size := utf8.DecodeLastRuneInString(text[:bj])
		bj -= size
		rj--
	}
	slices.Reverse(pos)

	if startRune == 0 && len(pos) == endRune+1 {
		score += scoreExactPrefix
	}
	score -= min(startRune, scoreLeadingCap)
	return score, pos, true
}

// Search ranks every row of m against query, best first, and appends the hits into dst[:0]. Rows
// that do not match are dropped; an empty query matches everything at score 0, in Model order.
//
// Each row is scored against its title and its app name and keeps the better of the two. The title
// wins an exact tie because that is the text the user is reading in the list.
//
// Ties in score break by lower row. That is the whole point: an unchanged query must produce the
// same order every summon, or the selection walks out from under the user.
//
// Every Match recycles the Positions buffer already parked in dst's backing array, so a caller that
// keeps one dst alive across summons allocates nothing after the first call.
func Search(m *Model, query string, dst []Match) []Match {
	out := dst[:0]

	if query == "" {
		for row := range m.IDs {
			out = append(out, Match{Row: row, Positions: searchScratch(out)[:0]})
		}
		return out
	}

	for row := range m.IDs {
		scratch := searchScratch(out)
		appScore, _, appOK := Score(query, m.AppNames[row], scratch)
		score, pos, ok := Score(query, m.Titles[row], scratch)
		if appOK && (!ok || appScore > score) {
			// Scoring the title clobbered the app name's positions, so re-derive them. Cheaper than
			// carrying a second buffer per row: app names are short and rarely win.
			score, pos, ok = Score(query, m.AppNames[row], scratch)
		}
		if !ok {
			continue
		}
		out = append(out, Match{Row: row, Score: score, Positions: pos})
	}

	slices.SortFunc(out, func(a, b Match) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.Row, b.Row)
	})
	return out
}

// searchScratch returns the Positions buffer already sitting in the slot append is about to fill, so
// a reused dst recycles its rune buffers instead of allocating one per row per summon. nil when the
// slot does not exist yet, which is the first-call case.
func searchScratch(out []Match) []int {
	n := len(out)
	if n >= cap(out) {
		return nil
	}
	return out[:n+1][n].Positions
}

// searchFoldEqual compares two runes case-insensitively.
func searchFoldEqual(a, b rune) bool {
	return a == b || unicode.ToLower(a) == unicode.ToLower(b)
}

// searchIsBoundary reports whether cur opens a word, given the rune immediately before it: prev is a
// separator or punctuation, or cur is a camelCase hump. It reads the original text rather than the
// folded runes, because case is the only signal a hump has.
func searchIsBoundary(prev, cur rune) bool {
	if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
		return true
	}
	return !unicode.IsUpper(prev) && unicode.IsUpper(cur)
}
