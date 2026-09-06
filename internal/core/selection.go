package core

// Selection methods (task P1.5). Selection.Row indexes into Order.Rows — a *position in the
// presentation list* — while Order.Rows holds Model row indices, so reaching a WindowID is always
// two hops: o.Rows[s.Row] -> m.IDs[...].
//
// Row and ID are two answers to "what is selected" that disagree exactly when the list is rebuilt
// mid-summon. Reconcile is where that disagreement is settled and is the only method that may
// change ID without the user having asked for a move.

// selectionIDAt reads the WindowID at position i of the order, or 0 when the order points nowhere
// real. The bounds check is not removable: Order.Rows holds Model row indices, and Model removal is
// swap-with-last, so an Order that has not been rebuilt since a removal legitimately holds rows
// past the end of the (now shorter) Model. Returning 0 there is what keeps the caller off a panic
// during the window between a removal and the next Rebuild.
func selectionIDAt(o *Order, m *Model, i int) WindowID {
	if i < 0 || i >= len(o.Rows) {
		return 0
	}
	row := o.Rows[i]
	if row < 0 || row >= len(m.IDs) {
		return 0
	}
	return m.IDs[row]
}

// Set selects the entry at position index, clamping into the list rather than rejecting: callers
// are UI code reacting to a click or a restored position, and a silent clamp is the behaviour that
// keeps a cursor on screen.
func (s *Selection) Set(o *Order, m *Model, index int) {
	n := len(o.Rows)
	if n == 0 {
		s.Row, s.ID = -1, 0
		return
	}
	if index < 0 {
		index = 0
	} else if index >= n {
		index = n - 1
	}
	s.Row = index
	s.ID = selectionIDAt(o, m, index)
}

// Move steps the cursor one entry in direction d. With wrap it cycles in both directions; without
// it clamps at both ends.
//
// Allocation-free by construction — no helper takes a slice header and nothing escapes. Pinned by
// BenchmarkSelectionMove, which the task contract requires to report 0 allocs/op.
func (s *Selection) Move(o *Order, m *Model, d Direction, wrap bool) {
	n := len(o.Rows)
	if n == 0 {
		s.Row, s.ID = -1, 0
		return
	}

	// No cursor yet: a forward step enters at the top of the list and a backward step at the
	// bottom, which is what a user pressing shift-tab on a fresh summon expects. Falling through
	// to the arithmetic below would instead land on 0 in both directions.
	if s.Row < 0 {
		if d == Backward {
			s.Set(o, m, n-1)
		} else {
			s.Set(o, m, 0)
		}
		return
	}

	// A stale Row can outrun a shrunken list when the caller moves before reconciling.
	if s.Row >= n {
		s.Row = n - 1
	}

	// Direction is frozen at ±1 (api.go), so one step overshoots by at most one and the ends need
	// a comparison, not a modulo.
	next := s.Row + int(d)
	if wrap {
		if next < 0 {
			next = n - 1
		} else if next >= n {
			next = 0
		}
	} else {
		if next < 0 {
			next = 0
		} else if next >= n {
			next = n - 1
		}
	}

	s.Row = next
	s.ID = selectionIDAt(o, m, next)
}

// Reconcile re-anchors the cursor after the order has been rebuilt underneath it — a window opened
// or closed while the switcher was held open.
//
// The selected ID wins over the selected position: the user is cycling towards a specific window,
// and rows move for reasons that have nothing to do with them (Model removal is swap-with-last, and
// a rebuild may reorder besides). Only when the ID is genuinely absent from the new order does the
// position take over, clamped into the list. It deliberately does not fall back to the front, which
// would throw away how far the user had cycled; TestSelectionReconcileRemovedSelectionHoldsIndex
// pins that.
//
// The search is a linear scan over the order rather than a Model.byID lookup because the answer
// needed is the *position in this order*, which byID cannot give — it would still cost a scan, and
// comparing IDs keeps Reconcile correct against a Model index that a rebuild is midway through.
// Orders are tens of entries; this stays well inside a frame.
func (s *Selection) Reconcile(o *Order, m *Model) {
	n := len(o.Rows)
	if n == 0 {
		s.Row, s.ID = -1, 0
		return
	}

	if s.ID != 0 {
		for i, row := range o.Rows {
			if row >= 0 && row < len(m.IDs) && m.IDs[row] == s.ID {
				s.Row = i
				return
			}
		}
	}

	if s.Row < 0 {
		s.Row = 0
	} else if s.Row >= n {
		s.Row = n - 1
	}
	s.ID = selectionIDAt(o, m, s.Row)
}
