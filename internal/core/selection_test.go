package core

import "testing"

// These tests build Model and Order from their frozen exported fields instead of going through
// P1.1's and P1.2's constructors. P1.5 is about the cursor surviving a rebuild, not about how the
// rebuild is produced, and driving the shapes directly keeps this file compiling and meaningful
// independently of the sibling tasks.

// selectionModel builds a Model whose row i holds window ids[i]. byID is left nil on purpose:
// nothing in Selection may depend on P1.1's index being populated or current.
func selectionModel(ids ...WindowID) *Model {
	m := &Model{
		IDs:      make([]WindowID, len(ids)),
		Apps:     make([]AppID, len(ids)),
		Groups:   make([]TabGroupID, len(ids)),
		Spaces:   make([]SpaceID, len(ids)),
		Focuses:  make([]FocusSeq, len(ids)),
		Flags:    make([]WindowFlags, len(ids)),
		Titles:   make([]string, len(ids)),
		AppNames: make([]string, len(ids)),
	}
	copy(m.IDs, ids)
	return m
}

// selectionOrder presents the model rows in the given order.
func selectionOrder(rows ...int) *Order { return &Order{Rows: rows} }

// selectionIdentityOrder is the order a rebuild produces when nothing reorders: storage order.
func selectionIdentityOrder(m *Model) *Order {
	rows := make([]int, len(m.IDs))
	for i := range rows {
		rows[i] = i
	}
	return &Order{Rows: rows}
}

// selectionDrop removes a window the way Model documents removal: swap-with-last, so the row that
// held the removed window is taken over by what used to be the last row. That relocation is exactly
// what Reconcile has to be immune to.
func selectionDrop(m *Model, id WindowID) {
	for i, got := range m.IDs {
		if got != id {
			continue
		}
		last := len(m.IDs) - 1
		m.IDs[i], m.IDs = m.IDs[last], m.IDs[:last]
		m.Apps[i], m.Apps = m.Apps[last], m.Apps[:last]
		m.Groups[i], m.Groups = m.Groups[last], m.Groups[:last]
		m.Spaces[i], m.Spaces = m.Spaces[last], m.Spaces[:last]
		m.Focuses[i], m.Focuses = m.Focuses[last], m.Focuses[:last]
		m.Flags[i], m.Flags = m.Flags[last], m.Flags[:last]
		m.Titles[i], m.Titles = m.Titles[last], m.Titles[:last]
		m.AppNames[i], m.AppNames = m.AppNames[last], m.AppNames[:last]
		return
	}
}

func selectionWant(t *testing.T, s *Selection, wantRow int, wantID WindowID) {
	t.Helper()
	if s.Row != wantRow || s.ID != wantID {
		t.Fatalf("selection = {Row:%d ID:%d}, want {Row:%d ID:%d}", s.Row, s.ID, wantRow, wantID)
	}
}

func TestSelectionSet(t *testing.T) {
	m := selectionModel(10, 20, 30)
	o := selectionIdentityOrder(m)

	tests := []struct {
		name    string
		index   int
		wantRow int
		wantID  WindowID
	}{
		{"first", 0, 0, 10},
		{"middle", 1, 1, 20},
		{"last", 2, 2, 30},
		{"clamps below", -5, 0, 10},
		{"clamps above", 99, 2, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Selection
			s.Set(o, m, tt.index)
			selectionWant(t, &s, tt.wantRow, tt.wantID)
		})
	}
}

// The order is a permutation, not the identity, so a bug that indexes the Model with the Selection
// row instead of going through Order.Rows shows up as a wrong ID rather than passing by accident.
func TestSelectionSetFollowsOrderNotStorage(t *testing.T) {
	m := selectionModel(10, 20, 30)
	o := selectionOrder(2, 0, 1)

	var s Selection
	s.Set(o, m, 0)
	selectionWant(t, &s, 0, 30)

	s.Set(o, m, 2)
	selectionWant(t, &s, 2, 20)
}

func TestSelectionMoveClamps(t *testing.T) {
	m := selectionModel(10, 20, 30)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 0)
	s.Move(o, m, Backward, false)
	selectionWant(t, &s, 0, 10)

	s.Move(o, m, Forward, false)
	selectionWant(t, &s, 1, 20)

	s.Set(o, m, 2)
	s.Move(o, m, Forward, false)
	selectionWant(t, &s, 2, 30)
}

func TestSelectionMoveWraps(t *testing.T) {
	m := selectionModel(10, 20, 30)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 2)
	s.Move(o, m, Forward, true)
	selectionWant(t, &s, 0, 10)

	s.Move(o, m, Backward, true)
	selectionWant(t, &s, 2, 30)
}

func TestSelectionMoveWrapsSingleEntry(t *testing.T) {
	m := selectionModel(10)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 0)
	s.Move(o, m, Forward, true)
	selectionWant(t, &s, 0, 10)
	s.Move(o, m, Backward, true)
	selectionWant(t, &s, 0, 10)
}

// Entering the list with no cursor: forward starts at the top, backward at the bottom. Plain
// arithmetic would land on the top in both directions.
func TestSelectionMoveFromNoSelection(t *testing.T) {
	m := selectionModel(10, 20, 30)
	o := selectionIdentityOrder(m)

	for _, wrap := range []bool{false, true} {
		s := Selection{Row: -1}
		s.Move(o, m, Forward, wrap)
		selectionWant(t, &s, 0, 10)

		s = Selection{Row: -1}
		s.Move(o, m, Backward, wrap)
		selectionWant(t, &s, 2, 30)
	}
}

// A Row left over from a longer list, moved before anything reconciled it.
func TestSelectionMoveFromStaleRow(t *testing.T) {
	m := selectionModel(10, 20)
	o := selectionIdentityOrder(m)

	s := Selection{Row: 7, ID: 99}
	s.Move(o, m, Forward, false)
	selectionWant(t, &s, 1, 20)
}

func TestSelectionEmptyOrder(t *testing.T) {
	m := selectionModel()
	o := selectionIdentityOrder(m)

	s := Selection{Row: 3, ID: 42}
	s.Set(o, m, 0)
	selectionWant(t, &s, -1, 0)

	s = Selection{Row: 3, ID: 42}
	s.Move(o, m, Forward, true)
	selectionWant(t, &s, -1, 0)

	s = Selection{Row: 3, ID: 42}
	s.Move(o, m, Backward, false)
	selectionWant(t, &s, -1, 0)

	s = Selection{Row: 3, ID: 42}
	s.Reconcile(o, m)
	selectionWant(t, &s, -1, 0)
}

// The selected window is still there but the rebuild moved it. The ID, not the position, decides.
func TestSelectionReconcileFollowsTheWindow(t *testing.T) {
	m := selectionModel(10, 20, 30, 40)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 1)
	selectionWant(t, &s, 1, 20)

	o.Rows = []int{3, 2, 1, 0}
	s.Reconcile(o, m)
	selectionWant(t, &s, 2, 20)
}

// The core case: the selected window closes while the switcher is open. The cursor holds its index
// and takes over whatever now occupies it — it must not jump to the front, which would discard how
// far the user had cycled.
func TestSelectionReconcileRemovedSelectionHoldsIndex(t *testing.T) {
	m := selectionModel(10, 20, 30, 40, 50)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 2)
	selectionWant(t, &s, 2, 30)

	// Swap-with-last puts window 50 into row 2, so index 2 of the rebuilt order is now window 50.
	selectionDrop(m, 30)
	o = selectionIdentityOrder(m)
	s.Reconcile(o, m)
	selectionWant(t, &s, 2, 50)
}

// Same removal, but the selection was on the last entry, so the held index no longer exists.
func TestSelectionReconcileRemovedSelectionClampsToEnd(t *testing.T) {
	m := selectionModel(10, 20, 30)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 2)
	selectionWant(t, &s, 2, 30)

	selectionDrop(m, 30)
	o = selectionIdentityOrder(m)
	s.Reconcile(o, m)
	selectionWant(t, &s, 1, 20)
}

// A window still in the Model but dropped from the order — filtered out, or moved to another Space
// mid-summon — is "gone" as far as the cursor is concerned.
func TestSelectionReconcileWindowFilteredOut(t *testing.T) {
	m := selectionModel(10, 20, 30)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 1)

	o.Rows = []int{0, 2}
	s.Reconcile(o, m)
	selectionWant(t, &s, 1, 30)
}

// Reconciling a fresh Selection (zero value, Row 0 / ID 0) must adopt the head rather than sit on a
// position with no ID behind it.
func TestSelectionReconcileFromZeroValue(t *testing.T) {
	m := selectionModel(10, 20)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Reconcile(o, m)
	selectionWant(t, &s, 0, 10)
}

// An Order left pointing at rows the Model no longer has — a removal with no rebuild yet.
func TestSelectionReconcileStaleOrderRows(t *testing.T) {
	m := selectionModel(10, 20)
	o := selectionOrder(0, 1, 9)

	s := Selection{Row: 2, ID: 30}
	s.Reconcile(o, m)
	selectionWant(t, &s, 2, 0)
}

func BenchmarkSelectionMove(b *testing.B) {
	m := selectionModel(10, 20, 30, 40, 50, 60, 70, 80)
	o := selectionIdentityOrder(m)

	var s Selection
	s.Set(o, m, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Move(o, m, Forward, true)
	}
}

func BenchmarkSelectionReconcile(b *testing.B) {
	m := selectionModel(10, 20, 30, 40, 50, 60, 70, 80)
	o := selectionIdentityOrder(m)

	var s Selection
	// The last entry: worst case for the linear scan.
	s.Set(o, m, len(o.Rows)-1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Reconcile(o, m)
	}
}
