// Package core holds every decision GoTab makes: window ordering, filtering, search matching,
// selection, tab grouping, and thumbnail cache policy.
//
// It is pure. No cgo, no AppKit, no IPC, no package-level mutable state. It compiles and its tests
// run on any OS, which is what makes it quick to develop and safe to hand to parallel agents.
//
// The dependency arrow never reverses: core must not import internal/platform. scripts/check.sh
// enforces that mechanically.
//
// Types shared across this package are frozen in api.go before parallel work starts (task P1.0), so
// that independent implementations can be written against a stable contract without coordinating.
package core
