package update

import "testing"

// P5.3 deferred these (D16, D23). parseVersion and compare are the whole of GoTab's version
// ordering — a hand-written dozen lines instead of a semver dependency (D39) — so V6.6 pins them.

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in            string
		wantOK        bool
		maj, min, pat int
	}{
		{"0.2.0", true, 0, 2, 0},
		{"1.2.3", true, 1, 2, 3},
		{"v0.2.0", true, 0, 2, 0},
		{"V1.0.0", true, 1, 0, 0},
		{"1", true, 1, 0, 0},                // missing minor and patch default to 0
		{"1.4", true, 1, 4, 0},              // missing patch
		{"0.2.0-rc1", true, 0, 2, 0},        // pre-release trailer dropped
		{"0.2.0+ci", true, 0, 2, 0},         // build metadata dropped
		{"0.2.0-4-g1a2b3c4", true, 0, 2, 0}, // git-describe trailer dropped
		{"  0.2.0  ", true, 0, 2, 0},        // trimmed
		{"1.2.3.4", true, 1, 2, 3},          // dotted fields past patch ignored

		{"", false, 0, 0, 0},
		{"dev", false, 0, 0, 0},
		{"g1a2b3c4", false, 0, 0, 0}, // git-describe with no release field
		{"1.x.0", false, 0, 0, 0},    // a letter in the numeric core
		{"1..0", false, 0, 0, 0},     // empty field is not an integer
		{"-1.0.0", false, 0, 0, 0},   // strips to "" then fails; a negative is not a release
		{"v", false, 0, 0, 0},
	}
	for _, tt := range tests {
		got, ok := parseVersion(tt.in)
		if ok != tt.wantOK {
			t.Errorf("parseVersion(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got.major != tt.maj || got.minor != tt.min || got.patch != tt.pat {
			t.Errorf("parseVersion(%q) = %d.%d.%d, want %d.%d.%d",
				tt.in, got.major, got.minor, got.patch, tt.maj, tt.min, tt.pat)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"0.2.0", "0.2.0-rc1", 0}, // the trailer is gone before compare sees it
		{"1.2.3", "1.2", 1},       // 1.2.3 vs 1.2.0
	}
	for _, tt := range tests {
		a, _ := parseVersion(tt.a)
		b, _ := parseVersion(tt.b)
		if got := compare(a, b); got != tt.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
