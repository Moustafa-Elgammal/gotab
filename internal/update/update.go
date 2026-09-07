package update

import "context"

// FeedURL is where Check looks for the release manifest. It is a placeholder
// until a real host exists (V6.11); an unreachable URL simply makes Check return
// an error, which every caller treats as "no update, carry on".
var FeedURL = "https://gotab.app/appcast/latest.json"

// Result is what Check found. The zero value — Available false, empty strings —
// is the safe "nothing to do" answer, and is what a caller gets on any error.
type Result struct {
	Current   string // the running version, as passed to Check
	Latest    string // the version the manifest advertises
	URL       string // where to download Latest
	Notes     string // one-line human summary, may be empty
	Available bool   // Latest is a real release newer than Current
}

// Check fetches FeedURL and reports whether it advertises something newer than
// current. current is cmd/gotab's main.version: a release tag like "v0.2.0", or
// "git describe" output, or the literal "dev" — anything that is not a clean
// release yields Available=false. A network or decode failure comes back as an
// error with a zero Result; it is never fatal, and callers log it and move on.
// The context bounds the HTTP request.
func Check(ctx context.Context, current string) (Result, error) {
	return check(ctx, current)
}
