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
// The shim's conventions were fixed by P2.1 and are documented in shim.h: a closed gt_status enum
// that becomes a Go sentinel error in exactly one place, bulk results written into a caller-allocated
// buffer so one crossing serves N items, and bitmaps handed out as opaque handles rather than
// pointers. Later tasks add functions to that shim; they do not invent a second shape.
//
// Bitmaps have no producer until P2.6. ImageRef, its release path and the live counter exist ahead of
// it on purpose, so the task that starts allocating megabytes is not also the task deciding how they
// are freed. The debug-build finalizer that ARCHITECTURE.md describes as a leak-detection backstop
// lands with that producer — a finalizer on a type nothing constructs would only rot.
//
// This package is deliberately not unit-tested. It is the humble object — correctness here is
// verified at runtime by the spikes under spike/, not by mocks.
package darwin
