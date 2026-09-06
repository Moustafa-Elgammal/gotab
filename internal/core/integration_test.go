package core

import "testing"

// The seam between P1.1, P1.2 and P1.5. Each was built and tested independently against the frozen
// api.go, and each deliberately avoided the others' constructors so a failure would localise. That
// left the interaction itself uncovered, and it is where the interesting bug lives: Model.Remove is
// swap-with-last, so an Order built before a removal holds row indices that no longer mean what
// they meant, and Selection has to survive that.

func integrationModel(t *testing.T, ids ...WindowID) *Model {
	t.Helper()
	m := NewModel(len(ids))
	for _, id := range ids {
		m.Upsert(Window{ID: id, App: AppID(id)})
	}
	// Focus in argument order, so the last id given is most-recently-used.
	for _, id := range ids {
		if !m.Touch(id) {
			t.Fatalf("Touch(%d) on a window just upserted", id)
		}
	}
	return m
}

func integrationIDs(m *Model, o *Order) []WindowID {
	out := make([]WindowID, 0, o.Len())
	for _, row := range o.Rows {
		out = append(out, m.IDs[row])
	}
	return out
}

func integrationEqual(t *testing.T, got []WindowID, want ...WindowID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestIntegrationSelectionSurvivesRemovalOfAnotherWindow(t *testing.T) {
	m := integrationModel(t, 10, 20, 30, 40, 50)
	o := NewOrder(m.Len())
	o.Rebuild(m)
	integrationEqual(t, integrationIDs(m, o), 50, 40, 30, 20, 10)

	var s Selection
	s.Set(o, m, 2) // window 30
	if s.ID != 30 {
		t.Fatalf("selected ID = %d, want 30", s.ID)
	}

	// Removing the *last* row is the case that relocates nothing; removing a middle row is the one
	// that moves an unrelated window into the vacated slot.
	if !m.Remove(10) {
		t.Fatal("Remove(10)")
	}
	o.Rebuild(m)
	s.Reconcile(o, m)

	if s.ID != 30 {
		t.Fatalf("selection followed the wrong window: ID = %d, want 30 to be untouched", s.ID)
	}
	if got := m.IDs[o.Rows[s.Row]]; got != 30 {
		t.Fatalf("Row %d points at window %d, want 30", s.Row, got)
	}
}

func TestIntegrationSelectionAfterSelectedWindowCloses(t *testing.T) {
	m := integrationModel(t, 10, 20, 30, 40, 50)
	o := NewOrder(m.Len())
	o.Rebuild(m)

	var s Selection
	s.Set(o, m, 2) // window 30, MRU order [50 40 30 20 10]

	// The window under the cursor closes while the switcher is open. Reconcile cannot follow the
	// ID any more, so it holds the position: index 2 of the new order.
	if !m.Remove(30) {
		t.Fatal("Remove(30)")
	}
	o.Rebuild(m)
	s.Reconcile(o, m)

	integrationEqual(t, integrationIDs(m, o), 50, 40, 20, 10)
	if s.Row != 2 {
		t.Fatalf("Row = %d, want 2 (hold the position when the window is gone)", s.Row)
	}
	if s.ID != 20 {
		t.Fatalf("ID = %d, want 20 — the entry that now occupies index 2", s.ID)
	}
}

func TestIntegrationSelectionAfterLastWindowCloses(t *testing.T) {
	m := integrationModel(t, 10, 20)
	o := NewOrder(m.Len())
	o.Rebuild(m)

	var s Selection
	s.Set(o, m, 1) // window 10, the tail of [20 10]

	if !m.Remove(10) {
		t.Fatal("Remove(10)")
	}
	o.Rebuild(m)
	s.Reconcile(o, m)

	// Clamped, not wrapped to the front: losing the user's place mid-cycle is the failure mode.
	if s.Row != 0 || s.ID != 20 {
		t.Fatalf("Row/ID = %d/%d, want 0/20", s.Row, s.ID)
	}
}

func TestIntegrationEmptyingTheModel(t *testing.T) {
	m := integrationModel(t, 10)
	o := NewOrder(m.Len())
	o.Rebuild(m)

	var s Selection
	s.Set(o, m, 0)

	m.Remove(10)
	o.Rebuild(m)
	s.Reconcile(o, m)

	if o.Len() != 0 {
		t.Fatalf("Len = %d, want 0", o.Len())
	}
	if s.Row != -1 || s.ID != 0 {
		t.Fatalf("Row/ID = %d/%d, want -1/0 on an empty list", s.Row, s.ID)
	}
	// Every method must stay safe once the list is empty.
	s.Move(o, m, Forward, true)
	s.Move(o, m, Backward, false)
	if s.Row != -1 {
		t.Fatalf("Row = %d after Move on an empty list, want -1", s.Row)
	}
}

// Promote is the attention decision: focusing a window moves it to the front without a Rebuild,
// and the cursor must still point at the window the user was looking at.
func TestIntegrationPromoteKeepsSelectionOnItsWindow(t *testing.T) {
	m := integrationModel(t, 10, 20, 30)
	o := NewOrder(m.Len())
	o.Rebuild(m)
	integrationEqual(t, integrationIDs(m, o), 30, 20, 10)

	var s Selection
	s.Set(o, m, 0) // window 30

	row, ok := m.Row(10)
	if !ok {
		t.Fatal("Row(10)")
	}
	o.Promote(row)
	integrationEqual(t, integrationIDs(m, o), 10, 30, 20)

	s.Reconcile(o, m)
	if s.ID != 30 || m.IDs[o.Rows[s.Row]] != 30 {
		t.Fatalf("selection = row %d id %d, want to still be on window 30", s.Row, s.ID)
	}
}
