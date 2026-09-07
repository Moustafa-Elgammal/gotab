package core

// Model storage. The struct-of-arrays layout and the byID invariant are documented in api.go; this
// file only implements the mutations that maintain them.

// NewModel returns a Model sized for capacity windows without further allocation. capacity is a
// hint: the model grows past it normally. Values <= 0 are treated as 0 rather than rejected, so a
// caller that has no estimate can pass 0.
func NewModel(capacity int) *Model {
	if capacity < 0 {
		capacity = 0
	}
	return &Model{
		IDs:      make([]WindowID, 0, capacity),
		Apps:     make([]AppID, 0, capacity),
		Groups:   make([]TabGroupID, 0, capacity),
		Spaces:   make([]SpaceID, 0, capacity),
		Focuses:  make([]FocusSeq, 0, capacity),
		Flags:    make([]WindowFlags, 0, capacity),
		Titles:   make([]string, 0, capacity),
		AppNames: make([]string, 0, capacity),
		byID:     make(map[WindowID]int, capacity),
	}
}

// Len is the number of rows. Every slice in the Model has this length.
func (m *Model) Len() int { return len(m.IDs) }

// Row resolves a window to its storage row. The row is only valid until the next Remove: removal
// moves the last row into the hole, so a caller must not hold a row across mutations.
func (m *Model) Row(id WindowID) (row int, ok bool) {
	row, ok = m.byID[id]
	return row, ok
}

// At materialises row as a Window value. Alloc-free — the two strings are header copies sharing the
// model's backing data. Callers outside core use this; kernels inside core read the slices instead.
// Panics on an out-of-range row, like any slice index.
func (m *Model) At(row int) Window {
	return Window{
		ID:      m.IDs[row],
		App:     m.Apps[row],
		Group:   m.Groups[row],
		Space:   m.Spaces[row],
		Focus:   m.Focuses[row],
		Flags:   m.Flags[row],
		Title:   m.Titles[row],
		AppName: m.AppNames[row],
	}
}

// Upsert inserts w or updates the existing row with the same ID, returning that row.
//
// A zero w.Focus on an existing window preserves the stored FocusSeq: refreshing a window whose
// title or flags changed must not cost it its MRU position (PLATFORM-LESSONS §3). Pass a non-zero
// Focus only to restore a known ordering; the normal way to promote a window is Touch.
func (m *Model) Upsert(w Window) (row int) {
	if row, ok := m.byID[w.ID]; ok {
		focus := w.Focus
		if focus == 0 {
			focus = m.Focuses[row]
		}
		m.Apps[row] = w.App
		m.Groups[row] = w.Group
		m.Spaces[row] = w.Space
		m.Focuses[row] = focus
		m.Flags[row] = w.Flags
		m.Titles[row] = w.Title
		m.AppNames[row] = w.AppName
		return row
	}

	// Upsert is the only writer of byID, so initialising it here is what makes the zero Model a
	// usable empty model; every read path already tolerates a nil map.
	if m.byID == nil {
		m.byID = make(map[WindowID]int)
	}

	row = len(m.IDs)
	m.IDs = append(m.IDs, w.ID)
	m.Apps = append(m.Apps, w.App)
	m.Groups = append(m.Groups, w.Group)
	m.Spaces = append(m.Spaces, w.Space)
	m.Focuses = append(m.Focuses, w.Focus)
	m.Flags = append(m.Flags, w.Flags)
	m.Titles = append(m.Titles, w.Title)
	m.AppNames = append(m.AppNames, w.AppName)
	m.byID[w.ID] = row
	return row
}

// Remove drops the window, reporting whether it was present. The last row is swapped into the hole
// and its byID entry rewritten to the new row — a stale index there is a switch to the wrong
// window, which is why TestModelRemoveMiddleFixesMovedRow removes from the middle and re-resolves
// the moved window rather than only checking Len.
func (m *Model) Remove(id WindowID) bool {
	row, ok := m.byID[id]
	if !ok {
		return false
	}
	last := len(m.IDs) - 1
	if row != last {
		m.IDs[row] = m.IDs[last]
		m.Apps[row] = m.Apps[last]
		m.Groups[row] = m.Groups[last]
		m.Spaces[row] = m.Spaces[last]
		m.Focuses[row] = m.Focuses[last]
		m.Flags[row] = m.Flags[last]
		m.Titles[row] = m.Titles[last]
		m.AppNames[row] = m.AppNames[last]
		m.byID[m.IDs[row]] = row
	}

	// Truncating leaves the vacated slot live in the backing array, so the two strings would stay
	// reachable for as long as the model does. Window titles are user text of unbounded size and a
	// long session churns thousands of them; drop the references explicitly.
	m.Titles[last] = ""
	m.AppNames[last] = ""

	m.IDs = m.IDs[:last]
	m.Apps = m.Apps[:last]
	m.Groups = m.Groups[:last]
	m.Spaces = m.Spaces[:last]
	m.Focuses = m.Focuses[:last]
	m.Flags = m.Flags[:last]
	m.Titles = m.Titles[:last]
	m.AppNames = m.AppNames[:last]
	delete(m.byID, id)
	return true
}

// Touch records that the window was focused, giving it the highest FocusSeq so far. It reports
// whether the window exists; an unknown window consumes no sequence number.
//
// The counter is incremented before it is handed out so a live window never holds Focus == 0, which
// is the value Upsert reads as "preserve" and the ordering kernel reads as "never focused".
func (m *Model) Touch(id WindowID) bool {
	row, ok := m.byID[id]
	if !ok {
		return false
	}
	m.nextFocus++
	m.Focuses[row] = m.nextFocus
	return true
}

// Reset empties the model but keeps the slice and map storage: it is reused across summons, and
// re-allocating it there would put a GC-visible allocation on the latency path
// (ARCHITECTURE.md#threading — draw first, bookkeep after).
//
// nextFocus deliberately survives. It is monotonic for the process lifetime (api.go), so FocusSeq
// values recorded before a Reset stay comparable with ones handed out after it.
func (m *Model) Reset() {
	// Same reason as in Remove: the retained backing arrays would otherwise pin every title.
	clear(m.Titles)
	clear(m.AppNames)

	m.IDs = m.IDs[:0]
	m.Apps = m.Apps[:0]
	m.Groups = m.Groups[:0]
	m.Spaces = m.Spaces[:0]
	m.Focuses = m.Focuses[:0]
	m.Flags = m.Flags[:0]
	m.Titles = m.Titles[:0]
	m.AppNames = m.AppNames[:0]
	clear(m.byID)
}
