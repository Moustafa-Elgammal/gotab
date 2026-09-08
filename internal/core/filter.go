package core

import "strings"

// P1.3 — the filter kernel. Reads the Model's parallel slices by row; it never materialises a
// Window, so a full sweep touches only the three fields it actually tests.

// Allows reports whether the window at row passes r.
//
// Checks are ordered cheapest-first: the flag mask is one AND, the Space and app comparisons are
// integer, and BlockedApps (the only string work) runs last on the rows that survived.
func (r Rules) Allows(m *Model, row int) bool {
	f := m.Flags[row]
	if !r.ShowMinimized && f.Has(FlagMinimized) {
		return false
	}
	if !r.ShowHidden && f.Has(FlagHidden) {
		return false
	}
	if r.ActiveAppOnly && r.ActiveApp != 0 && m.Apps[row] != r.ActiveApp {
		return false
	}
	// A zero SpaceID on either side means SkyLight declined to answer, not "some other Space"
	// (api.go SpaceID, PLATFORM-LESSONS §7). Comparing it would empty the switcher wholesale on a
	// machine where the private API is unavailable, which reads as the app being broken.
	// TestFilterUnknownSpace pins both directions.
	if !r.ShowOtherSpace && r.CurrentSpace != 0 && m.Spaces[row] != 0 && m.Spaces[row] != r.CurrentSpace {
		return false
	}
	for _, blocked := range r.BlockedApps {
		if strings.EqualFold(m.AppNames[row], blocked) {
			return false
		}
	}
	return true
}

// Filter appends every row of m that r allows to dst[:0] and returns the result, in storage order —
// presentation order is Order's job (P1.2). Pass back the previous result to stay allocation-free;
// BenchmarkFilter pins that at 0 allocs/op.
func Filter(m *Model, r Rules, dst []int) []int {
	dst = dst[:0]
	for row := range m.IDs {
		if r.Allows(m, row) {
			dst = append(dst, row)
		}
	}
	return dst
}
