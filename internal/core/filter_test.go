package core

import (
	"fmt"
	"testing"
)

// Helpers are prefixed filter- because every P1 task's tests share package core and a bare name
// like modelOf would collide with a sibling file.

// filterModel builds a Model straight from the parallel slices rather than through P1.1's
// constructor: the filter kernel never reads byID, so going through Upsert would only couple these
// tests to a mutation API they do not exercise.
func filterModel(ws ...Window) *Model {
	m := &Model{}
	for _, w := range ws {
		m.IDs = append(m.IDs, w.ID)
		m.Apps = append(m.Apps, w.App)
		m.Groups = append(m.Groups, w.Group)
		m.Spaces = append(m.Spaces, w.Space)
		m.Focuses = append(m.Focuses, w.Focus)
		m.Flags = append(m.Flags, w.Flags)
		m.Titles = append(m.Titles, w.Title)
		m.AppNames = append(m.AppNames, w.AppName)
	}
	return m
}

func filterRows(m *Model, r Rules) []WindowID {
	got := make([]WindowID, 0, len(m.IDs))
	for _, row := range Filter(m, r, nil) {
		got = append(got, m.IDs[row])
	}
	return got
}

func filterEqual(a, b []WindowID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterAllows(t *testing.T) {
	tests := []struct {
		name string
		win  Window
		r    Rules
		want bool
	}{
		{
			name: "zero rules show a plain window on the current space",
			win:  Window{Space: 7, Flags: FlagOnScreen},
			r:    Rules{CurrentSpace: 7},
			want: true,
		},
		{
			name: "zero rules hide minimized",
			win:  Window{Space: 7, Flags: FlagMinimized},
			r:    Rules{CurrentSpace: 7},
			want: false,
		},
		{
			name: "ShowMinimized admits minimized",
			win:  Window{Space: 7, Flags: FlagMinimized},
			r:    Rules{CurrentSpace: 7, ShowMinimized: true},
			want: true,
		},
		{
			name: "zero rules hide hidden",
			win:  Window{Space: 7, Flags: FlagHidden},
			r:    Rules{CurrentSpace: 7},
			want: false,
		},
		{
			name: "ShowHidden admits hidden",
			win:  Window{Space: 7, Flags: FlagHidden},
			r:    Rules{CurrentSpace: 7, ShowHidden: true},
			want: true,
		},
		{
			name: "ShowMinimized does not imply ShowHidden",
			win:  Window{Space: 7, Flags: FlagMinimized | FlagHidden},
			r:    Rules{CurrentSpace: 7, ShowMinimized: true},
			want: false,
		},
		{
			name: "zero rules hide another space",
			win:  Window{Space: 9},
			r:    Rules{CurrentSpace: 7},
			want: false,
		},
		{
			name: "ShowOtherSpace admits another space",
			win:  Window{Space: 9},
			r:    Rules{CurrentSpace: 7, ShowOtherSpace: true},
			want: true,
		},
		{
			name: "fullscreen window on its own space is not special-cased",
			win:  Window{Space: 9, Flags: FlagFullscreen},
			r:    Rules{CurrentSpace: 7},
			want: false,
		},
		{
			name: "ActiveAppOnly keeps the active app",
			win:  Window{App: 42, Space: 7},
			r:    Rules{CurrentSpace: 7, ActiveAppOnly: true, ActiveApp: 42},
			want: true,
		},
		{
			name: "ActiveAppOnly drops another app",
			win:  Window{App: 43, Space: 7},
			r:    Rules{CurrentSpace: 7, ActiveAppOnly: true, ActiveApp: 42},
			want: false,
		},
		{
			name: "ActiveAppOnly is ignored when ActiveApp is zero",
			win:  Window{App: 43, Space: 7},
			r:    Rules{CurrentSpace: 7, ActiveAppOnly: true},
			want: true,
		},
		{
			name: "BlockedApps drops by app name",
			win:  Window{Space: 7, AppName: "Finder"},
			r:    Rules{CurrentSpace: 7, BlockedApps: []string{"Finder"}},
			want: false,
		},
		{
			name: "BlockedApps is case-insensitive",
			win:  Window{Space: 7, AppName: "Finder"},
			r:    Rules{CurrentSpace: 7, BlockedApps: []string{"fInDeR"}},
			want: false,
		},
		{
			name: "BlockedApps does not match a different app",
			win:  Window{Space: 7, AppName: "Finder"},
			r:    Rules{CurrentSpace: 7, BlockedApps: []string{"Safari", "Terminal"}},
			want: true,
		},
		{
			name: "BlockedApps does not match a prefix",
			win:  Window{Space: 7, AppName: "Finder"},
			r:    Rules{CurrentSpace: 7, BlockedApps: []string{"Find"}},
			want: true,
		},
		{
			name: "a tabbed window is not filtered here",
			win:  Window{Space: 7, Group: 3, Flags: FlagTabbed},
			r:    Rules{CurrentSpace: 7},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := filterModel(tt.win)
			if got := tt.r.Allows(m, 0); got != tt.want {
				t.Errorf("Allows() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFilterUnknownSpace is the case ALTTAB-LESSONS §7 exists for: an unavailable SkyLight must
// degrade to "show everything", never to an empty switcher.
func TestFilterUnknownSpace(t *testing.T) {
	m := filterModel(
		Window{ID: 1, Space: 7},
		Window{ID: 2, Space: 9},
		Window{ID: 3, Space: 0},
	)

	t.Run("CurrentSpace zero keeps every window", func(t *testing.T) {
		want := []WindowID{1, 2, 3}
		if got := filterRows(m, Rules{}); !filterEqual(got, want) {
			t.Errorf("Filter() = %v, want %v", got, want)
		}
	})

	t.Run("a window of unknown space survives a known CurrentSpace", func(t *testing.T) {
		want := []WindowID{1, 3}
		if got := filterRows(m, Rules{CurrentSpace: 7}); !filterEqual(got, want) {
			t.Errorf("Filter() = %v, want %v", got, want)
		}
	})
}

func TestFilterRows(t *testing.T) {
	m := filterModel(
		Window{ID: 1, App: 10, Space: 7, AppName: "Safari"},
		Window{ID: 2, App: 10, Space: 7, AppName: "Safari", Flags: FlagMinimized},
		Window{ID: 3, App: 11, Space: 9, AppName: "Terminal"},
		Window{ID: 4, App: 12, Space: 7, AppName: "Finder", Flags: FlagHidden},
		Window{ID: 5, App: 10, Space: 7, AppName: "Safari"},
	)

	tests := []struct {
		name string
		r    Rules
		want []WindowID
	}{
		{
			name: "zero value on the current space",
			r:    Rules{CurrentSpace: 7},
			want: []WindowID{1, 5},
		},
		{
			name: "everything shown",
			r: Rules{
				CurrentSpace:   7,
				ShowMinimized:  true,
				ShowHidden:     true,
				ShowOtherSpace: true,
			},
			want: []WindowID{1, 2, 3, 4, 5},
		},
		{
			name: "active app only",
			r:    Rules{CurrentSpace: 7, ActiveAppOnly: true, ActiveApp: 10},
			want: []WindowID{1, 5},
		},
		{
			name: "blocked app plus other spaces",
			r: Rules{
				CurrentSpace:   7,
				ShowOtherSpace: true,
				ShowHidden:     true,
				BlockedApps:    []string{"finder"},
			},
			want: []WindowID{1, 3, 5},
		},
		{
			name: "no window survives",
			r:    Rules{CurrentSpace: 7, ActiveAppOnly: true, ActiveApp: 99},
			want: []WindowID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterRows(m, tt.r); !filterEqual(got, tt.want) {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFilterReusesDst pins the contract that makes the hot path allocation-free: dst is truncated,
// not appended to, so a shorter result never leaves the previous summon's rows visible.
func TestFilterReusesDst(t *testing.T) {
	m := filterModel(
		Window{ID: 1, App: 10, Space: 7},
		Window{ID: 2, App: 11, Space: 7},
		Window{ID: 3, App: 12, Space: 7},
	)

	dst := Filter(m, Rules{CurrentSpace: 7}, nil)
	if len(dst) != 3 {
		t.Fatalf("first pass = %v, want 3 rows", dst)
	}

	before := &dst[0]
	dst = Filter(m, Rules{CurrentSpace: 7, ActiveAppOnly: true, ActiveApp: 11}, dst)
	if len(dst) != 1 || m.IDs[dst[0]] != 2 {
		t.Fatalf("second pass = %v, want the single row of window 2", dst)
	}
	if &dst[:1][0] != before {
		t.Error("Filter reallocated a dst that already had capacity")
	}

	if allocs := testing.AllocsPerRun(100, func() {
		dst = Filter(m, Rules{CurrentSpace: 7}, dst)
	}); allocs != 0 {
		t.Errorf("Filter with a warm dst = %v allocs/op, want 0", allocs)
	}
}

func BenchmarkFilter(b *testing.B) {
	const n = 200

	ws := make([]Window, 0, n)
	for i := range n {
		w := Window{
			ID:      WindowID(i + 1),
			App:     AppID(i%8 + 1),
			Space:   SpaceID(i%3 + 1),
			AppName: fmt.Sprintf("App %d", i%8),
		}
		switch i % 5 {
		case 0:
			w.Flags = FlagMinimized
		case 1:
			w.Flags = FlagHidden
		default:
			w.Flags = FlagOnScreen
		}
		ws = append(ws, w)
	}
	m := filterModel(ws...)

	r := Rules{CurrentSpace: 1, BlockedApps: []string{"App 7"}}
	dst := Filter(m, r, make([]int, 0, n))

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dst = Filter(m, r, dst)
	}
}
