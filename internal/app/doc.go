// Package app is the event loop: the one goroutine that owns every piece of mutable state GoTab has.
//
// The window model, the presentation order and the selection live here and are reached from nowhere
// else. That is the whole design — share by communicating, not by locking (docs/ARCHITECTURE.md#threading).
// There is no mutex in this package and there must not be one: a mutex around the window model means
// two goroutines believe they own it, and the fix is to move the caller onto the loop, not to lock.
//
// Everything outside sends events. Platform callbacks arrive on threads the Go runtime may never have
// seen, do nothing but Post, and return immediately — an Accessibility callback or an event tap that
// stalls is disabled by the system, so posting must never block, and Post is built so it cannot.
//
// What may reorder the list is the constraint that shapes the rest (docs/PLATFORM-LESSONS.md §3): an
// attention decision (the user focused a window) or a structural repair (a window appeared or
// vanished), and nothing else. Re-enumeration reads every window on every pass and must leave MRU
// order exactly as it found it, which is why Rescan upserts with a zero FocusSeq and rebuilds the
// order only when membership actually changed.
package app
