package update

import (
	"strconv"
	"strings"
)

// releaseVersion is a parsed major.minor.patch triple. Pre-release ("-rc1") and
// build ("+ci") metadata are dropped at parse time: GoTab orders releases by the
// numeric triple alone, so "1.2.3-rc1" and "1.2.3" compare equal here. That is
// all "is the feed newer than me?" needs, and it keeps the comparison a
// hand-written dozen lines rather than a semver dependency (D39).
type releaseVersion struct {
	major, minor, patch int
}

// parseVersion reads a version string into a comparable triple.
//
// ok is false when the string is not a release number at all: the literal "dev",
// an empty string, or "git describe" output whose leading field is a commit hash
// like "g1a2b3c4". For those there is simply nothing to compare, and check
// reports "no update" without treating it as an error.
//
// It accepts a single leading "v" ("v0.2.0"), a missing minor or patch (they
// default to 0, so "1" is 1.0.0), and trailing "-suffix" / "+build" metadata,
// which is discarded. Every numeric field that is present must actually be a
// non-negative integer; a stray letter anywhere in the numeric core makes the
// whole string uncomparable.
func parseVersion(s string) (v releaseVersion, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "dev" {
		return releaseVersion{}, false
	}
	if s[0] == 'v' || s[0] == 'V' {
		s = s[1:]
	}
	// Build metadata first, then the pre-release / git-describe trailer, so
	// "0.2.0+ci", "0.2.0-rc1" and "0.2.0-4-g1a2b3c4" all reduce to "0.2.0".
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return releaseVersion{}, false
	}

	var out [3]int
	for i, field := range strings.Split(s, ".") {
		if i > 2 {
			break // extra dotted fields beyond patch are ignored
		}
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return releaseVersion{}, false
		}
		out[i] = n
	}
	return releaseVersion{out[0], out[1], out[2]}, true
}

// compare orders two triples field by field: -1 if a is older than b, +1 if
// newer, 0 if equal.
func compare(a, b releaseVersion) int {
	for _, p := range [3][2]int{
		{a.major, b.major},
		{a.minor, b.minor},
		{a.patch, b.patch},
	} {
		switch {
		case p[0] < p[1]:
			return -1
		case p[0] > p[1]:
			return 1
		}
	}
	return 0
}
