package core

// Tab groups (P1.6). One group shows as one entry in the switcher, so something has to decide which
// member stands for it. That decision lives here and nowhere else.
//
// Election reads only Model.Groups and Model.Focuses. It never consults Rules and never filters —
// P1.3 owns visibility, and a representative that changed with the filter would make the same group
// jump between rows as the user types.

// Representative returns the row of the window that stands for group g.
//
// The winner is the member with the highest Focus; equal Focus breaks to the lowest row. Ties are
// real, not defensive: FocusSeq is zero for a window that has never been focused, so a freshly
// discovered group has every member at zero. TestTabGroupRepresentativeTiedFocus pins it.
//
// ok is false for a group with no members and for g == 0, which is the "not tabbed" sentinel rather
// than a group id (see TabGroupID in api.go).
func Representative(m *Model, g TabGroupID) (row int, ok bool) {
	if g == 0 {
		return 0, false
	}
	best := -1
	for i, gid := range m.Groups {
		if gid != g {
			continue
		}
		// Strictly-greater, scanning rows in order, is what makes the lowest row win a tie.
		if best < 0 || m.Focuses[i] > m.Focuses[best] {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// IsRepresentative reports whether row is the entry the switcher should show. An untabbed window
// (Group == 0) is always its own representative, so this is the single predicate a filter pass can
// use for both tabbed and untabbed windows without special-casing.
//
// Panics on an out-of-range row, like any slice index: rows are produced inside core and a bad one
// is a bug worth surfacing, not a silent false.
func IsRepresentative(m *Model, row int) bool {
	g := m.Groups[row]
	if g == 0 {
		return true
	}
	rep, ok := Representative(m, g)
	return ok && rep == row
}

// GroupRows appends the rows of every member of g to dst[:0] and returns the result, so a caller
// that keeps one buffer across summons allocates nothing after the first call.
//
// g == 0 yields no rows. Untabbed windows are not a group, and returning all of them here would
// hand a caller asking about "the tab group" every window in the model.
func GroupRows(m *Model, g TabGroupID, dst []int) []int {
	dst = dst[:0]
	if g == 0 {
		return dst
	}
	for i, gid := range m.Groups {
		if gid == g {
			dst = append(dst, i)
		}
	}
	return dst
}
