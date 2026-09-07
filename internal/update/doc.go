// Package update answers one question: has a newer GoTab been published?
//
// It is pure Go and standard-library only — no cgo, no Sparkle, no vendored
// framework (D39). Check does an HTTPS GET of a small JSON manifest, compares its
// version against the running one, and returns whether an upgrade exists and
// where to get it. It does NOT download or install anything: in-place update
// wants a Developer ID signature and notarization the ad-hoc bundle does not have
// (D38), so that is a separate, later concern.
//
// Failure is never fatal. An unreachable feed, a malformed manifest, or a
// development build with no comparable version all yield a zero Result; the first
// two also return an error the caller logs and otherwise ignores. Nothing here
// runs on the summon path — it backs the `gotab -check-update` subcommand and,
// later, an optional check at launch.
//
// update.go and its Check signature are frozen (the P5.3 fan-out codes against
// them); the implementation lives in http.go and its helpers.
package update
