package update

import "context"

// check is the real implementation of Check. This is the stub the carve ships so
// the package builds and every caller is safe; P5.3 replaces the body with the
// HTTPS GET of FeedURL, the manifest decode, and the version comparison. Until
// then GoTab always believes it is up to date.
func check(_ context.Context, current string) (Result, error) {
	return Result{Current: current}, nil
}
