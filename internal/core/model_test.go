package core

import "testing"

// modelBenchSink keeps the benchmarked results observable so the compiler cannot delete the call
// under test. Test-only: package core itself has no package-level mutable state (doc.go).
var modelBenchSink Window

func modelWin(id WindowID, title string) Window {
	return Window{ID: id, App: AppID(id) * 10, Title: title, AppName: "App" + title}
}

// modelCheckInvariants asserts the two things every mutation must maintain: all slices are the same
// length, and byID resolves each window to the row that actually holds it.
func modelCheckInvariants(t *testing.T, m *Model) {
	t.Helper()
	n := m.Len()
	lengths := map[string]int{
		"Apps":     len(m.Apps),
		"Groups":   len(m.Groups),
		"Spaces":   len(m.Spaces),
		"Focuses":  len(m.Focuses),
		"Flags":    len(m.Flags),
		"Titles":   len(m.Titles),
		"AppNames": len(m.AppNames),
	}
	for name, got := range lengths {
		if got != n {
			t.Errorf("len(%s) = %d, want %d", name, got, n)
		}
	}
	if len(m.byID) != n {
		t.Errorf("len(byID) = %d, want %d", len(m.byID), n)
	}
	for row := 0; row < n; row++ {
		id := m.IDs[row]
		gotRow, ok := m.Row(id)
		if !ok {
			t.Errorf("row %d holds window %d but byID does not know it", row, id)
			continue
		}
		if gotRow != row {
			t.Errorf("byID[%d] = %d, want %d", id, gotRow, row)
		}
	}
}

func TestModelNewModel(t *testing.T) {
	for _, capacity := range []int{-1, 0, 8} {
		m := NewModel(capacity)
		if m.Len() != 0 {
			t.Errorf("NewModel(%d).Len() = %d, want 0", capacity, m.Len())
		}
		if _, ok := m.Row(1); ok {
			t.Errorf("NewModel(%d) resolved a window in an empty model", capacity)
		}
		modelCheckInvariants(t, m)
	}
}

func TestModelUpsertInsertAndUpdate(t *testing.T) {
	m := NewModel(4)

	row := m.Upsert(Window{ID: 7, App: 100, Group: 3, Space: 2, Focus: 9, Flags: FlagOnScreen, Title: "one", AppName: "Term"})
	if row != 0 {
		t.Fatalf("first Upsert row = %d, want 0", row)
	}
	if m.Len() != 1 {
		t.Fatalf("Len = %d, want 1", m.Len())
	}

	want := Window{ID: 7, App: 100, Group: 3, Space: 2, Focus: 9, Flags: FlagOnScreen, Title: "one", AppName: "Term"}
	if got := m.At(0); got != want {
		t.Errorf("At(0) = %+v, want %+v", got, want)
	}

	// Same ID updates in place: no new row, every non-Focus field overwritten.
	row = m.Upsert(Window{ID: 7, App: 101, Group: 0, Space: 5, Focus: 12, Flags: FlagMinimized, Title: "two", AppName: "Edit"})
	if row != 0 {
		t.Fatalf("update Upsert row = %d, want 0", row)
	}
	if m.Len() != 1 {
		t.Fatalf("Len after update = %d, want 1", m.Len())
	}
	want = Window{ID: 7, App: 101, Group: 0, Space: 5, Focus: 12, Flags: FlagMinimized, Title: "two", AppName: "Edit"}
	if got := m.At(0); got != want {
		t.Errorf("At(0) after update = %+v, want %+v", got, want)
	}

	modelCheckInvariants(t, m)
}

// A refresh that carries no Focus must not cost the window its MRU position (PLATFORM-LESSONS §3):
// title changes arrive constantly and are not attention decisions.
func TestModelUpsertZeroFocusPreserves(t *testing.T) {
	m := NewModel(2)
	m.Upsert(modelWin(1, "a"))
	m.Touch(1)
	before := m.At(0).Focus
	if before == 0 {
		t.Fatal("Touch left Focus at 0")
	}

	m.Upsert(Window{ID: 1, Title: "a-renamed"}) // Focus zero
	if got := m.At(0).Focus; got != before {
		t.Errorf("Focus = %d after zero-Focus Upsert, want preserved %d", got, before)
	}
	if got := m.At(0).Title; got != "a-renamed" {
		t.Errorf("Title = %q, want %q", got, "a-renamed")
	}

	m.Upsert(Window{ID: 1, Focus: 42, Title: "a-renamed"}) // non-zero Focus wins
	if got := m.At(0).Focus; got != 42 {
		t.Errorf("Focus = %d after explicit Focus Upsert, want 42", got)
	}

	// Insertion is not an update: a brand-new window keeps the zero it was given.
	m.Upsert(Window{ID: 2, Title: "b"})
	row, _ := m.Row(2)
	if got := m.At(row).Focus; got != 0 {
		t.Errorf("new window Focus = %d, want 0", got)
	}
	modelCheckInvariants(t, m)
}

func TestModelRow(t *testing.T) {
	m := NewModel(3)
	m.Upsert(modelWin(10, "a"))
	m.Upsert(modelWin(20, "b"))

	for _, tc := range []struct {
		id      WindowID
		wantRow int
		wantOK  bool
	}{
		{10, 0, true},
		{20, 1, true},
		{30, 0, false},
		{0, 0, false},
	} {
		row, ok := m.Row(tc.id)
		if ok != tc.wantOK || (ok && row != tc.wantRow) {
			t.Errorf("Row(%d) = (%d, %v), want (%d, %v)", tc.id, row, ok, tc.wantRow, tc.wantOK)
		}
	}
}

// The contract's named case: removing a middle row moves the last row into the hole, and the moved
// window must still resolve. A stale byID entry here is a switch to the wrong window.
func TestModelRemoveMiddleFixesMovedRow(t *testing.T) {
	m := NewModel(4)
	m.Upsert(modelWin(1, "a"))
	m.Upsert(modelWin(2, "b"))
	m.Upsert(modelWin(3, "c"))
	m.Upsert(modelWin(4, "d"))

	if !m.Remove(2) {
		t.Fatal("Remove(2) = false, want true")
	}
	if m.Len() != 3 {
		t.Fatalf("Len = %d, want 3", m.Len())
	}
	if _, ok := m.Row(2); ok {
		t.Error("removed window 2 still resolves")
	}

	// Window 4 was last, so it now occupies row 1 — and must carry its own data, not window 2's.
	row, ok := m.Row(4)
	if !ok {
		t.Fatal("moved window 4 no longer resolves")
	}
	if row != 1 {
		t.Errorf("Row(4) = %d, want 1", row)
	}
	if got := m.At(row); got.ID != 4 || got.Title != "d" {
		t.Errorf("At(%d) = %+v, want window 4 titled \"d\"", row, got)
	}
	modelCheckInvariants(t, m)
}

func TestModelRemove(t *testing.T) {
	m := NewModel(4)
	m.Upsert(modelWin(1, "a"))
	m.Upsert(modelWin(2, "b"))
	m.Upsert(modelWin(3, "c"))

	if m.Remove(99) {
		t.Error("Remove(99) = true for an unknown window")
	}
	if m.Len() != 3 {
		t.Errorf("Len = %d after failed Remove, want 3", m.Len())
	}

	// Removing the last row takes the no-swap path.
	if !m.Remove(3) {
		t.Fatal("Remove(3) = false")
	}
	modelCheckInvariants(t, m)

	if !m.Remove(1) || !m.Remove(2) {
		t.Fatal("draining the model failed")
	}
	if m.Len() != 0 {
		t.Errorf("Len = %d after draining, want 0", m.Len())
	}
	modelCheckInvariants(t, m)

	// The drained model is still usable, and rows restart at 0.
	if row := m.Upsert(modelWin(5, "e")); row != 0 {
		t.Errorf("Upsert into drained model row = %d, want 0", row)
	}
	modelCheckInvariants(t, m)
}

func TestModelTouch(t *testing.T) {
	m := NewModel(3)
	m.Upsert(modelWin(1, "a"))
	m.Upsert(modelWin(2, "b"))

	if m.Touch(99) {
		t.Error("Touch(99) = true for an unknown window")
	}

	if !m.Touch(1) || !m.Touch(2) || !m.Touch(1) {
		t.Fatal("Touch of a known window returned false")
	}

	r1, _ := m.Row(1)
	r2, _ := m.Row(2)
	f1, f2 := m.At(r1).Focus, m.At(r2).Focus
	if f1 == 0 || f2 == 0 {
		t.Errorf("Touch left a Focus at 0: window1=%d window2=%d", f1, f2)
	}
	if f1 <= f2 {
		t.Errorf("Focus(1)=%d Focus(2)=%d, want the most recently touched window to be higher", f1, f2)
	}
}

// An unknown window must not burn a sequence number, or the counter drifts on every stale event
// from the platform layer.
func TestModelTouchUnknownConsumesNoSeq(t *testing.T) {
	m := NewModel(2)
	m.Upsert(modelWin(1, "a"))
	m.Touch(1)
	first := m.At(0).Focus

	for range 5 {
		m.Touch(404)
	}
	m.Touch(1)
	if got := m.At(0).Focus; got != first+1 {
		t.Errorf("Focus = %d after 5 failed Touches, want %d", got, first+1)
	}
}

func TestModelReset(t *testing.T) {
	m := NewModel(4)
	m.Upsert(modelWin(1, "a"))
	m.Upsert(modelWin(2, "b"))
	m.Touch(1)
	m.Touch(2)
	beforeCap := cap(m.IDs)
	beforeFocus := m.At(1).Focus

	m.Reset()
	if m.Len() != 0 {
		t.Errorf("Len after Reset = %d, want 0", m.Len())
	}
	if _, ok := m.Row(1); ok {
		t.Error("window 1 still resolves after Reset")
	}
	modelCheckInvariants(t, m)

	if cap(m.IDs) != beforeCap {
		t.Errorf("cap(IDs) = %d after Reset, want the storage kept at %d", cap(m.IDs), beforeCap)
	}

	// nextFocus survives a Reset: FocusSeq is monotonic for the process, not per summon.
	m.Upsert(modelWin(3, "c"))
	m.Touch(3)
	if got := m.At(0).Focus; got <= beforeFocus {
		t.Errorf("Focus after Reset = %d, want > %d", got, beforeFocus)
	}
}

// The zero Model is a valid empty model; the lazy byID init in Upsert is what makes it one.
func TestModelZeroValueUsable(t *testing.T) {
	var m Model
	if m.Len() != 0 {
		t.Fatalf("zero Model Len = %d, want 0", m.Len())
	}
	if _, ok := m.Row(1); ok {
		t.Error("zero Model resolved a window")
	}
	if m.Remove(1) || m.Touch(1) {
		t.Error("zero Model reported a mutation of a window it does not have")
	}
	m.Reset()
	if row := m.Upsert(modelWin(1, "a")); row != 0 {
		t.Fatalf("Upsert into zero Model row = %d, want 0", row)
	}
	modelCheckInvariants(t, &m)
}

func modelBenchFixture(n int) *Model {
	m := NewModel(n)
	for i := range n {
		m.Upsert(modelWin(WindowID(i+1), "window title"))
	}
	return m
}

func BenchmarkModelRow(b *testing.B) {
	m := modelBenchFixture(64)
	b.ReportAllocs()
	b.ResetTimer()
	row := 0
	for i := range b.N {
		r, _ := m.Row(WindowID(i%64 + 1))
		row += r
	}
	modelBenchSink = m.At(row % m.Len())
}

func BenchmarkModelAt(b *testing.B) {
	m := modelBenchFixture(64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		modelBenchSink = m.At(i % 64)
	}
}

func BenchmarkModelTouch(b *testing.B) {
	m := modelBenchFixture(64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		m.Touch(WindowID(i%64 + 1))
	}
}

func BenchmarkModelUpsertExisting(b *testing.B) {
	m := modelBenchFixture(64)
	w := modelWin(1, "renamed")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		w.ID = WindowID(i%64 + 1)
		modelBenchSink.App = AppID(m.Upsert(w))
	}
}
