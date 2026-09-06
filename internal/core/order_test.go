package core

import (
	"testing"
)

// orderModel builds a Model directly from its slices rather than through Model.Upsert: an ordering
// test should fail only when ordering is wrong, never because the model's mutation path changed.
// Rows are in storage order, which is exactly what Rebuild must fall back to for equal Focus.
func orderModel(focuses ...FocusSeq) *Model {
	m := &Model{}
	for i, f := range focuses {
		m.IDs = append(m.IDs, WindowID(i+1))
		m.Focuses = append(m.Focuses, f)
	}
	return m
}

func orderRows(t *testing.T, o *Order, want ...int) {
	t.Helper()
	if len(o.Rows) != len(want) {
		t.Fatalf("rows = %v, want %v", o.Rows, want)
	}
	for i := range want {
		if o.Rows[i] != want[i] {
			t.Fatalf("rows = %v, want %v", o.Rows, want)
		}
	}
}

func TestOrderRebuild(t *testing.T) {
	tests := []struct {
		name     string
		focuses  []FocusSeq
		wantRows []int
	}{
		{"empty", nil, nil},
		{"single", []FocusSeq{7}, []int{0}},
		{"descending focus", []FocusSeq{1, 2, 3}, []int{2, 1, 0}},
		{"already ordered", []FocusSeq{3, 2, 1}, []int{0, 1, 2}},
		{"interleaved", []FocusSeq{5, 1, 9, 3}, []int{2, 0, 3, 1}},
		// Never-focused windows sort last but keep storage order among themselves.
		{"never focused last", []FocusSeq{0, 4, 0, 2}, []int{1, 3, 0, 2}},
		{"all never focused", []FocusSeq{0, 0, 0}, []int{0, 1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOrder(8)
			o.Rebuild(orderModel(tt.focuses...))
			orderRows(t, o, tt.wantRows...)
			if o.Len() != len(tt.wantRows) {
				t.Errorf("Len() = %d, want %d", o.Len(), len(tt.wantRows))
			}
		})
	}
}

// Equal Focus must survive repeated Rebuilds in the same relative order. An unstable sort passes a
// single Rebuild and only shows up as windows swapping places between summons.
func TestOrderRebuildStableAcrossRepeats(t *testing.T) {
	m := orderModel(0, 0, 5, 0, 5, 0)
	want := []int{2, 4, 0, 1, 3, 5}

	o := NewOrder(len(m.IDs))
	for i := 0; i < 10; i++ {
		o.Rebuild(m)
		orderRows(t, o, want...)
	}
}

// Rebuild is a structural repair, so it discards whatever Promote did.
func TestOrderRebuildDiscardsPromotion(t *testing.T) {
	m := orderModel(3, 2, 1)
	o := NewOrder(3)
	o.Rebuild(m)
	if !o.Promote(2) {
		t.Fatal("Promote(2) = false, want true")
	}
	orderRows(t, o, 2, 0, 1)

	o.Rebuild(m)
	orderRows(t, o, 0, 1, 2)
}

// A warm Order must not allocate on Rebuild; growth is allowed only on the first pass past capacity.
func TestOrderRebuildDoesNotAllocateWhenWarm(t *testing.T) {
	m := orderModel(4, 0, 9, 2, 0, 7, 1, 3)
	o := NewOrder(len(m.IDs))
	o.Rebuild(m)

	if got := testing.AllocsPerRun(100, func() { o.Rebuild(m) }); got != 0 {
		t.Errorf("Rebuild allocs = %v, want 0", got)
	}
}

// NewOrder's capacity is what makes the first Rebuild allocation-free too.
func TestOrderNewOrderPreallocates(t *testing.T) {
	o := NewOrder(16)
	if o.Len() != 0 {
		t.Errorf("Len() = %d, want 0", o.Len())
	}
	if got := cap(o.Rows); got < 16 {
		t.Errorf("cap(Rows) = %d, want >= 16", got)
	}

	// A negative capacity is a caller bug, not a panic.
	if n := NewOrder(-1); n.Len() != 0 {
		t.Errorf("NewOrder(-1).Len() = %d, want 0", n.Len())
	}
}

func TestOrderPromote(t *testing.T) {
	tests := []struct {
		name     string
		promote  int
		wantOK   bool
		wantRows []int
	}{
		{"front is a no-op", 0, true, []int{0, 1, 2, 3}},
		{"middle", 2, true, []int{2, 0, 1, 3}},
		{"last", 3, true, []int{3, 0, 1, 2}},
		{"absent row", 9, false, []int{0, 1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOrder(4)
			o.Rebuild(orderModel(4, 3, 2, 1))
			orderRows(t, o, 0, 1, 2, 3)

			if got := o.Promote(tt.promote); got != tt.wantOK {
				t.Errorf("Promote(%d) = %v, want %v", tt.promote, got, tt.wantOK)
			}
			orderRows(t, o, tt.wantRows...)
		})
	}
}

// Cycling is Promote applied repeatedly; nothing may be lost or duplicated along the way.
func TestOrderPromoteKeepsPermutation(t *testing.T) {
	o := NewOrder(5)
	o.Rebuild(orderModel(5, 4, 3, 2, 1))

	for _, row := range []int{3, 1, 4, 1, 0, 2, 2} {
		if !o.Promote(row) {
			t.Fatalf("Promote(%d) = false, want true", row)
		}
		if o.Rows[0] != row {
			t.Fatalf("after Promote(%d) rows = %v", row, o.Rows)
		}
	}
	seen := make(map[int]bool, o.Len())
	for _, r := range o.Rows {
		if seen[r] {
			t.Fatalf("row %d duplicated: %v", r, o.Rows)
		}
		seen[r] = true
	}
	if len(seen) != 5 {
		t.Fatalf("rows = %v, want a permutation of 0..4", o.Rows)
	}
}

func TestOrderPromoteDoesNotAllocate(t *testing.T) {
	o := NewOrder(8)
	o.Rebuild(orderModel(8, 7, 6, 5, 4, 3, 2, 1))

	row := 0
	if got := testing.AllocsPerRun(100, func() {
		o.Promote(row)
		row = (row + 1) % o.Len()
	}); got != 0 {
		t.Errorf("Promote allocs = %v, want 0", got)
	}
}

// Promote must not reach into the Model — Focus is Model.Touch's to hand out.
func TestOrderPromoteLeavesModelAlone(t *testing.T) {
	m := orderModel(3, 2, 1)
	o := NewOrder(3)
	o.Rebuild(m)
	o.Promote(2)

	for i, want := range []FocusSeq{3, 2, 1} {
		if m.Focuses[i] != want {
			t.Errorf("Focuses[%d] = %d, want %d", i, m.Focuses[i], want)
		}
	}
}

func TestOrderIndexOf(t *testing.T) {
	o := NewOrder(3)
	o.Rebuild(orderModel(1, 3, 2))
	orderRows(t, o, 1, 2, 0)

	for row, want := range map[int]int{1: 0, 2: 1, 0: 2} {
		got, ok := o.IndexOf(row)
		if !ok || got != want {
			t.Errorf("IndexOf(%d) = %d, %v; want %d, true", row, got, ok, want)
		}
	}

	if got, ok := o.IndexOf(7); ok || got != -1 {
		t.Errorf("IndexOf(7) = %d, %v; want -1, false", got, ok)
	}

	empty := NewOrder(0)
	if got, ok := empty.IndexOf(0); ok || got != -1 {
		t.Errorf("empty IndexOf(0) = %d, %v; want -1, false", got, ok)
	}
}

func BenchmarkOrderRebuild(b *testing.B) {
	m := &Model{}
	for i := 0; i < 64; i++ {
		m.IDs = append(m.IDs, WindowID(i+1))
		// Half the windows have never been focused, which is the tie-heavy case the stable sort
		// has to walk.
		if i%2 == 0 {
			m.Focuses = append(m.Focuses, FocusSeq(64-i))
		} else {
			m.Focuses = append(m.Focuses, 0)
		}
	}
	o := NewOrder(len(m.IDs))
	o.Rebuild(m)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o.Rebuild(m)
	}
}

func BenchmarkOrderPromote(b *testing.B) {
	m := &Model{}
	for i := 0; i < 64; i++ {
		m.IDs = append(m.IDs, WindowID(i+1))
		m.Focuses = append(m.Focuses, FocusSeq(64-i))
	}
	o := NewOrder(len(m.IDs))
	o.Rebuild(m)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o.Promote(i % 64)
	}
}
