// Package darwin is the impure shell: every cgo call, every AppKit object, every bitmap.
//
// Two rules govern everything here, both from docs/ARCHITECTURE.md:
//
//   - Batch every crossing. A cgo call costs ~31ns against ~2.6ns for a Go call (docs/DECISIONS.md
//     D1), so one call returns N windows. Never one call per window.
//   - Callbacks enqueue and return. An Accessibility or SkyLight callback pushes onto a channel and
//     does nothing else: no logic, no allocation, no logging in the callback body.
//
// Bitmap lifetime is manual and explicit. CGImage backing store lives outside the Go heap, so the
// garbage collector sees an 8-byte pointer where megabytes are pinned and will not reclaim it.
// Every acquire has a matching release; runtime.SetFinalizer is a debug-build leak detector only,
// never the mechanism.
//
// This package is deliberately not unit-tested. It is the humble object — correctness here is
// verified at runtime by the spikes under spike/, not by mocks.
package darwin
