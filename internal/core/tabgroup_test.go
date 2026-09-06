package core

import (
	"testing"
	"unsafe"
)

// tabGroupModel builds a Model with only the two columns election reads. The other columns stay
// empty on purpose: if a future change makes Representative depend on IDs, Flags or Titles, these
// tests must fail rather than quietly pass on data that the tab-group kernel is not entitled to.
func tabGroupModel(groups []TabGroupID, focuses []FocusSeq) *Model {
	if len(groups) != len(focuses) {
		panic("tabGroupModel: columns must be index-aligned")
	}
	return &Model{Groups: groups, Focuses: focuses}
}

func TestTabGroupRepresentative(t *testing.T) {
	tests := []struct {
		name    string
		groups  []TabGroupID
		focuses []FocusSeq
		group   TabGroupID
		wantRow int
		wantOK  bool
	}{
		{
			name:    "highest focus wins",
			groups:  []TabGroupID{7, 7, 7},
			focuses: []FocusSeq{3, 9, 5},
			group:   7,
			wantRow: 1,
			wantOK:  true,
		},
		{
			name:    "winner is not the last row scanned",
			groups:  []TabGroupID{7, 7, 7},
			focuses: []FocusSeq{9, 5, 3},
			group:   7,
			wantRow: 0,
			wantOK:  true,
		},
		{
			name:    "members interleaved with other groups and untabbed windows",
			groups:  []TabGroupID{0, 4, 7, 0, 7, 4},
			focuses: []FocusSeq{99, 50, 2, 98, 6, 51},
			group:   7,
			wantRow: 4,
			wantOK:  true,
		},
		{
			name:    "single member",
			groups:  []TabGroupID{0, 7, 0},
			focuses: []FocusSeq{1, 0, 2},
			group:   7,
			wantRow: 1,
			wantOK:  true,
		},
		{
			name:    "group zero is not a group",
			groups:  []TabGroupID{0, 0, 7},
			focuses: []FocusSeq{1, 2, 3},
			group:   0,
			wantOK:  false,
		},
		{
			name:    "unknown group has no members",
			groups:  []TabGroupID{7, 7},
			focuses: []FocusSeq{1, 2},
			group:   8,
			wantOK:  false,
		},
		{
			name:   "empty model",
			group:  7,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tabGroupModel(tc.groups, tc.focuses)
			row, ok := Representative(m, tc.group)
			if ok != tc.wantOK {
				t.Fatalf("Representative(%d) ok = %v, want %v", tc.group, ok, tc.wantOK)
			}
			if ok && row != tc.wantRow {
				t.Errorf("Representative(%d) row = %d, want %d", tc.group, row, tc.wantRow)
			}
		})
	}
}

// Acceptance: a group whose members have equal Focus. Every window in a group starts at FocusSeq
// zero, so this is the state a group is discovered in, not an edge case.
func TestTabGroupRepresentativeTiedFocus(t *testing.T) {
	tests := []struct {
		name    string
		focuses []FocusSeq
		wantRow int
	}{
		{name: "all never focused", focuses: []FocusSeq{0, 0, 0}, wantRow: 0},
		{name: "all equal and non-zero", focuses: []FocusSeq{4, 4, 4}, wantRow: 0},
		{name: "tie at the top, loser first", focuses: []FocusSeq{1, 6, 6}, wantRow: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groups := make([]TabGroupID, len(tc.focuses))
			for i := range groups {
				groups[i] = 7
			}
			m := tabGroupModel(groups, tc.focuses)

			row, ok := Representative(m, 7)
			if !ok {
				t.Fatalf("Representative(7) ok = false, want true")
			}
			if row != tc.wantRow {
				t.Errorf("Representative(7) row = %d, want %d (lowest tied row)", row, tc.wantRow)
			}

			// Determinism is the whole point of the tie-break; a map-order implementation would
			// pass a single call and fail here.
			for i := 0; i < 32; i++ {
				if again, _ := Representative(m, 7); again != row {
					t.Fatalf("Representative(7) row = %d on repeat, want stable %d", again, row)
				}
			}
		})
	}
}

// Acceptance: focusing a window outside the group must not re-elect that group's representative.
func TestTabGroupRepresentativeIgnoresOutsideFocus(t *testing.T) {
	// Rows 1 and 3 are group 7; rows 0, 2 and 4 are an untabbed window and a second group.
	groups := []TabGroupID{0, 7, 4, 7, 0}
	focuses := []FocusSeq{1, 5, 2, 3, 4}
	m := tabGroupModel(groups, focuses)

	before, ok := Representative(m, 7)
	if !ok || before != 1 {
		t.Fatalf("Representative(7) = %d, %v; want 1, true", before, ok)
	}

	// Every non-member gets focused in turn, each time becoming the most recently focused window
	// in the whole model. Nothing about group 7 changed, so its representative must not move.
	next := FocusSeq(100)
	for _, row := range []int{0, 2, 4} {
		focuses[row] = next
		next++
		if got, ok := Representative(m, 7); !ok || got != before {
			t.Fatalf("after focusing outside row %d: Representative(7) = %d, %v; want %d, true",
				row, got, ok, before)
		}
	}

	// The contrast case: focusing a member does re-elect, which is what makes the check above a
	// real constraint rather than a function that ignores Focus entirely.
	focuses[3] = next
	if got, ok := Representative(m, 7); !ok || got != 3 {
		t.Fatalf("after focusing member row 3: Representative(7) = %d, %v; want 3, true", got, ok)
	}
}

func TestTabGroupIsRepresentative(t *testing.T) {
	// Rows 0 and 3 are untabbed. Group 7 is rows 1 and 4 (row 4 wins on focus); group 4 is row 2.
	groups := []TabGroupID{0, 7, 4, 0, 7}
	focuses := []FocusSeq{0, 2, 9, 0, 8}
	m := tabGroupModel(groups, focuses)

	want := []bool{true, false, true, true, true}
	for row, w := range want {
		if got := IsRepresentative(m, row); got != w {
			t.Errorf("IsRepresentative(row %d) = %v, want %v", row, got, w)
		}
	}

	// Exactly one representative per group, and one per untabbed window, is the property the
	// switcher's "one entry per group" rule rests on.
	reps := 0
	for row := range groups {
		if IsRepresentative(m, row) {
			reps++
		}
	}
	if reps != 4 {
		t.Errorf("representative count = %d, want 4 (2 untabbed + 2 groups)", reps)
	}
}

func TestTabGroupIsRepresentativeUntabbedNeverLoses(t *testing.T) {
	// Untabbed windows all carry Group 0. They must not be treated as one giant group in which
	// only the most recently focused survives.
	groups := []TabGroupID{0, 0, 0}
	focuses := []FocusSeq{9, 1, 5}
	m := tabGroupModel(groups, focuses)

	for row := range groups {
		if !IsRepresentative(m, row) {
			t.Errorf("IsRepresentative(untabbed row %d) = false, want true", row)
		}
	}
}

func TestTabGroupRows(t *testing.T) {
	groups := []TabGroupID{0, 7, 4, 7, 0, 7}
	focuses := []FocusSeq{1, 2, 3, 4, 5, 6}
	m := tabGroupModel(groups, focuses)

	tests := []struct {
		name  string
		group TabGroupID
		want  []int
	}{
		{name: "multi-member group in row order", group: 7, want: []int{1, 3, 5}},
		{name: "single member", group: 4, want: []int{2}},
		{name: "group zero yields nothing", group: 0, want: nil},
		{name: "unknown group yields nothing", group: 99, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GroupRows(m, tc.group, nil)
			if len(got) != len(tc.want) {
				t.Fatalf("GroupRows(%d) = %v, want %v", tc.group, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("GroupRows(%d) = %v, want %v", tc.group, got, tc.want)
				}
			}
		})
	}
}

func TestTabGroupRowsReusesBuffer(t *testing.T) {
	groups := []TabGroupID{7, 4, 7, 4, 7}
	focuses := []FocusSeq{1, 2, 3, 4, 5}
	m := tabGroupModel(groups, focuses)

	dst := GroupRows(m, 7, make([]int, 0, 8))
	base := unsafe.SliceData(dst)

	// A second call must truncate the previous answer rather than append to it, and must not
	// reallocate while the buffer still fits: the switcher calls this per frame.
	dst = GroupRows(m, 4, dst)
	if len(dst) != 2 || dst[0] != 1 || dst[1] != 3 {
		t.Fatalf("GroupRows(4) reusing buffer = %v, want [1 3]", dst)
	}
	if unsafe.SliceData(dst) != base {
		t.Errorf("GroupRows reallocated a buffer with spare capacity")
	}

	// Stale contents beyond len must not leak back on a query that matches nothing.
	dst = GroupRows(m, 0, dst)
	if len(dst) != 0 {
		t.Errorf("GroupRows(0) reusing buffer = %v, want empty", dst)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		dst = GroupRows(m, 7, dst)
	}); allocs != 0 {
		t.Errorf("GroupRows allocated %v times per run with a sufficient buffer, want 0", allocs)
	}
}
