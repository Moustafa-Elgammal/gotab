package core

// MRU ordering. Two operations may change the order and nothing else may: Promote (the user made an
// attention decision) and Rebuild (a window appeared or vanished). docs/PLATFORM-LESSONS.md §3 is the
// reason — shipped switchers have driven reorders off title and geometry notifications, which move
// the list out from under a user mid-cycle.

// NewOrder returns an Order sized to hold capacity rows without allocating. capacity is a hint, not
// a limit: Rebuild grows Rows when the Model outgrows it, and only that growth allocates.
func NewOrder(capacity int) *Order {
	if capacity < 0 {
		capacity = 0
	}
	return &Order{Rows: make([]int, 0, capacity)}
}

// Len is the number of rows in the presentation order.
func (o *Order) Len() int { return len(o.Rows) }

// Rebuild replaces the order with every row of m, most recently focused first. Structural repair
// only — call it when a window appears or vanishes, never when a title or frame changes.
//
// Windows that have never been focused all share Focus 0, so ties are common rather than exotic;
// the sort is stable and they keep Model storage order, which is what stops them shuffling between
// summons. TestOrderRebuildStableAcrossRepeats pins that.
func (o *Order) Rebuild(m *Model) {
	rows := o.Rows[:0]
	for i := range m.IDs {
		rows = append(rows, i)
	}
	sortByFocus(m, rows)
	o.Rows = rows
}

// RebuildFrom replaces the order with exactly the rows in `rows`, most recently focused first — the
// output of the filter kernel (P1.3), so the presentation order holds only the windows the Rules
// allow. Same structural-repair-only rule as Rebuild: call it when membership or a filter input (a
// window minimized, the current Space changed) changed, never on a title or frame change. `rows` must
// be valid indices into m; RebuildFrom trusts them and does not copy `rows` beyond appending it.
func (o *Order) RebuildFrom(m *Model, rows []int) {
	dst := append(o.Rows[:0], rows...)
	sortByFocus(m, dst)
	o.Rows = dst
}

// sortByFocus stable-sorts rows in place, most recently focused first. Insertion sort rather than
// sort.Stable: sort.Stable takes an interface, and boxing a sorter that carries both slices escapes
// to the heap — the one allocation this path may not do. n is the number of open windows (tens), so
// the quadratic term never approaches the cost of the allocation it avoids. Strict < keeps it stable,
// so Focus-0 ties keep their incoming order.
func sortByFocus(m *Model, rows []int) {
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		focus := m.Focuses[row]
		j := i - 1
		for j >= 0 && m.Focuses[rows[j]] < focus {
			rows[j+1] = rows[j]
			j--
		}
		rows[j+1] = row
	}
}

// Promote moves row to the front and shifts everything that was ahead of it down one. It reports
// whether row was in the order; a row already at the front is a no-op that still reports true.
//
// It does not touch the Model: FocusSeq is Model.Touch's to hand out, and Rebuild reads it. A
// Promote alone therefore survives only until the next Rebuild, which is the intended lifetime for
// an order the user is cycling through.
func (o *Order) Promote(row int) bool {
	i, ok := o.IndexOf(row)
	if !ok {
		return false
	}
	// copy is memmove, so the overlapping forward shift is defined.
	copy(o.Rows[1:i+1], o.Rows[:i])
	o.Rows[0] = row
	return true
}

// IndexOf returns row's position in the presentation order, or -1 and false when the row is absent.
//
// Linear scan, no index map: Rows holds tens of entries, and every removal in the Model renumbers
// rows, so a map would have to be rebuilt exactly as often as the scan would have run.
func (o *Order) IndexOf(row int) (int, bool) {
	for i, r := range o.Rows {
		if r == row {
			return i, true
		}
	}
	return -1, false
}
