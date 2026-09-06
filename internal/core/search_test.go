package core

import (
	"fmt"
	"reflect"
	"testing"
)

// searchModel builds a Model from (title, appName) pairs. It writes the struct-of-arrays fields
// directly rather than going through the P1.1 mutators, because Search reads nothing else and the
// tests should not fail on someone else's constructor.
func searchModel(rows ...[2]string) *Model {
	m := &Model{}
	for i, r := range rows {
		m.IDs = append(m.IDs, WindowID(i+1))
		m.Apps = append(m.Apps, AppID(i+1))
		m.Groups = append(m.Groups, 0)
		m.Spaces = append(m.Spaces, 1)
		m.Focuses = append(m.Focuses, FocusSeq(i))
		m.Flags = append(m.Flags, FlagOnScreen)
		m.Titles = append(m.Titles, r[0])
		m.AppNames = append(m.AppNames, r[1])
	}
	return m
}

func TestSearchScoreSubsequence(t *testing.T) {
	cases := []struct {
		query, text string
		want        bool
	}{
		{"", "anything", true},
		{"", "", true},
		{"a", "", false},
		{"term", "Terminal", true},
		{"trml", "Terminal", true},
		{"TERM", "terminal", true},
		{"term", "Terminal", true},
		{"mret", "Terminal", false},
		{"terminals", "Terminal", false},
		{"gh", "GitHub Desktop", true},
		{"ü", "Münster", true},
		{"Ü", "Münster", true},
		{"münster", "Café MÜNSTER", true},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q_in_%q", c.query, c.text), func(t *testing.T) {
			_, _, ok := Score(c.query, c.text, nil)
			if ok != c.want {
				t.Errorf("Score(%q, %q) ok = %v, want %v", c.query, c.text, ok, c.want)
			}
		})
	}
}

func TestSearchScorePositions(t *testing.T) {
	cases := []struct {
		query, text string
		want        []int
	}{
		{"term", "Terminal", []int{0, 1, 2, 3}},
		{"tab", "AltTab", []int{3, 4, 5}},
		// Tightened right: the greedy forward pass would land on 0 and 3, the backward pass pulls
		// the whole match onto the trailing consecutive pair.
		{"ab", "a_ab", []int{2, 3}},
		{"", "Terminal", []int{}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q_in_%q", c.query, c.text), func(t *testing.T) {
			_, pos, ok := Score(c.query, c.text, nil)
			if !ok {
				t.Fatalf("Score(%q, %q) did not match", c.query, c.text)
			}
			if len(pos) != len(c.want) || (len(pos) > 0 && !reflect.DeepEqual(pos, c.want)) {
				t.Errorf("positions = %v, want %v", pos, c.want)
			}
		})
	}
}

// TestSearchNonASCIIPositions is the test docs/tasks/P1.4.md calls for: Positions index runes, and
// on this title every byte offset past the é differs from its rune index, so a byte-indexed
// implementation cannot pass by accident.
func TestSearchNonASCIIPositions(t *testing.T) {
	const title = "Café Münster — Notes"

	_, pos, ok := Score("cafmns", title, nil)
	if !ok {
		t.Fatal("Score did not match")
	}
	want := []int{0, 1, 2, 5, 7, 8}
	if !reflect.DeepEqual(pos, want) {
		t.Fatalf("positions = %v, want %v", pos, want)
	}

	runes := []rune(title)
	var got []rune
	for _, p := range pos {
		got = append(got, runes[p])
	}
	if string(got) != "CafMns" {
		t.Errorf("runes at positions = %q, want %q", string(got), "CafMns")
	}

	// The same match expressed in bytes is a different sequence; naming it here so a regression to
	// byte offsets is obvious in the failure output.
	if bytes := []int{0, 1, 2, 6, 9, 10}; reflect.DeepEqual(pos, bytes) {
		t.Errorf("positions are byte offsets %v, not rune indices", bytes)
	}

	// A multi-byte rune in the query itself must fold and land on its own rune index.
	_, pos, ok = Score("mü", title, nil)
	if !ok || !reflect.DeepEqual(pos, []int{5, 6}) {
		t.Errorf("Score(%q) = %v, %v; want [5 6], true", "mü", pos, ok)
	}
}

// TestSearchScoreOrdering pins the three rewards the contract orders: an exact prefix outranks a
// word boundary, which outranks a consecutive run.
func TestSearchScoreOrdering(t *testing.T) {
	cases := []struct {
		why                  string
		query, better, worse string
	}{
		{"exact prefix beats a camelCase-hump boundary", "term", "Terminal", "iTerm Ultra"},
		{"exact prefix beats a separator boundary", "code", "Code Editor", "VS Code"},
		{"word boundary beats mid-word", "g", "Sublime Git", "Toggle"},
		{"consecutive run beats a scattered subsequence", "ab", "xxab", "xaxxb"},
		{"earlier match breaks a tie", "ab", "ab__", "__ab__"},
	}
	for _, c := range cases {
		t.Run(c.why, func(t *testing.T) {
			hi, _, okHi := Score(c.query, c.better, nil)
			lo, _, okLo := Score(c.query, c.worse, nil)
			if !okHi || !okLo {
				t.Fatalf("both texts must match: %v %v", okHi, okLo)
			}
			if hi <= lo {
				t.Errorf("Score(%q, %q) = %d, want > Score(%q, %q) = %d",
					c.query, c.better, hi, c.query, c.worse, lo)
			}
		})
	}
}

func TestSearchScoreReusesBuffer(t *testing.T) {
	buf := make([]int, 0, 8)
	_, pos, _ := Score("term", "Terminal", buf)
	if &pos[:1][0] != &buf[:1][0] {
		t.Error("Score allocated instead of appending into positions[:0]")
	}

	// A shorter follow-up match must not leave the previous run's tail behind.
	_, pos, _ = Score("t", "Terminal", pos)
	if len(pos) != 1 {
		t.Errorf("len(pos) = %d, want 1 — positions was not truncated", len(pos))
	}
}

func TestSearchEmptyQueryReturnsEveryRow(t *testing.T) {
	m := searchModel(
		[2]string{"Inbox", "Mail"},
		[2]string{"README.md", "Zed"},
		[2]string{"gotab — main", "Ghostty"},
	)
	got := Search(m, "", nil)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, mt := range got {
		if mt.Row != i {
			t.Errorf("got[%d].Row = %d, want %d — empty query must keep Model order", i, mt.Row, i)
		}
		if mt.Score != 0 {
			t.Errorf("got[%d].Score = %d, want 0", i, mt.Score)
		}
		if len(mt.Positions) != 0 {
			t.Errorf("got[%d].Positions = %v, want empty", i, mt.Positions)
		}
	}
}

func TestSearchRanksBestFirst(t *testing.T) {
	m := searchModel(
		[2]string{"Untitled Terrarium", "Preview"}, // scattered subsequence
		[2]string{"main.go — gotab", "Terminal"},   // exact prefix, on the app name
		[2]string{"iTerm profile", "Settings"},     // camelCase hump boundary
		[2]string{"Inbox", "Mail"},                 // no match at all
	)
	got := Search(m, "term", nil)

	wantRows := []int{1, 2, 0}
	gotRows := make([]int, len(got))
	for i, mt := range got {
		gotRows[i] = mt.Row
	}
	if !reflect.DeepEqual(gotRows, wantRows) {
		t.Fatalf("rows = %v, want %v (scores %v)", gotRows, wantRows, searchScores(got))
	}
}

func TestSearchMatchesAppNameWhenTitleDoesNot(t *testing.T) {
	m := searchModel([2]string{"Untitled", "Ghostty"})
	got := Search(m, "ghost", nil)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := []int{0, 1, 2, 3, 4}; !reflect.DeepEqual(got[0].Positions, want) {
		t.Errorf("Positions = %v, want %v — should index the app name that won", got[0].Positions, want)
	}
}

func TestSearchTieBreaksByRow(t *testing.T) {
	m := searchModel(
		[2]string{"Terminal", "Alacritty"},
		[2]string{"Terminal", "Alacritty"},
		[2]string{"Terminal", "Alacritty"},
	)
	got := Search(m, "term", nil)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, mt := range got {
		if mt.Row != i {
			t.Errorf("got[%d].Row = %d, want %d — equal scores must order by row", i, mt.Row, i)
		}
	}
}

func TestSearchDeterministic(t *testing.T) {
	m := searchModel(
		[2]string{"Café Münster — Notes", "Notes"},
		[2]string{"main.go — gotab", "Terminal"},
		[2]string{"Terminal", "Terminal"},
		[2]string{"iTerm profile", "Settings"},
		[2]string{"Untitled Terrarium", "Preview"},
		[2]string{"Inbox", "Mail"},
	)
	for _, q := range []string{"", "t", "term", "cafmns", "zzz"} {
		a := Search(m, q, nil)
		b := Search(m, q, nil)
		if !searchSameResult(a, b) {
			t.Errorf("query %q: %v != %v", q, a, b)
		}
		// A reused dst must produce the same answer as a fresh one, or the recycled Positions
		// buffers are leaking state between summons.
		c := Search(m, q, Search(m, "zzz", make([]Match, 0, 16)))
		if !searchSameResult(a, c) {
			t.Errorf("query %q via reused dst: %v != %v", q, c, a)
		}
	}
}

func TestSearchReusedDstDoesNotAllocate(t *testing.T) {
	m := searchModel(searchFixture(200)...)
	dst := make([]Match, 0, 200)
	dst = Search(m, "term", dst)

	if n := testing.AllocsPerRun(20, func() { dst = Search(m, "term", dst) }); n != 0 {
		t.Errorf("Search allocated %v times per call on a reused dst, want 0", n)
	}
}

// searchSameResult compares two result slices by value. reflect.DeepEqual alone would report a
// fresh nil result and a recycled empty one as different, which is a property of the buffer the
// caller passed in, not of the ranking.
func searchSameResult(a, b []Match) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Row != b[i].Row || a[i].Score != b[i].Score {
			return false
		}
		if len(a[i].Positions) != len(b[i].Positions) {
			return false
		}
		for j := range a[i].Positions {
			if a[i].Positions[j] != b[i].Positions[j] {
				return false
			}
		}
	}
	return true
}

func searchScores(ms []Match) []int {
	out := make([]int, len(ms))
	for i, m := range ms {
		out[i] = m.Score
	}
	return out
}

func searchFixture(n int) [][2]string {
	titles := []string{
		"main.go — gotab", "Café Münster — Notes", "iTerm profile", "Untitled Terrarium",
		"Inbox — moustafa", "README.md", "ARCHITECTURE.md — gotab", "Terminal — zsh",
	}
	apps := []string{"Ghostty", "Notes", "Settings", "Preview", "Mail", "Zed", "Zed", "Terminal"}
	rows := make([][2]string, n)
	for i := range rows {
		rows[i] = [2]string{
			fmt.Sprintf("%s (%d)", titles[i%len(titles)], i),
			apps[i%len(apps)],
		}
	}
	return rows
}

func BenchmarkScore(b *testing.B) {
	buf := make([]int, 0, 16)
	b.ReportAllocs()
	for b.Loop() {
		_, buf, _ = Score("cafmns", "Café Münster — Notes", buf)
	}
}

func BenchmarkSearch(b *testing.B) {
	m := searchModel(searchFixture(200)...)
	dst := Search(m, "term", make([]Match, 0, 200))
	b.ReportAllocs()
	for b.Loop() {
		dst = Search(m, "term", dst)
	}
}
