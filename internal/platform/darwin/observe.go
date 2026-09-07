package darwin

/*
#include "observe.h"
*/
import "C"

import (
	"errors"
	"sync"
	"sync/atomic"
)

// The cgo flags for this package are declared once, in shim.go. They apply to every file in the
// package, and repeating them here would be a second place to keep the framework list in sync.

// The callback the C observers end in. An atomic pointer rather than a plain variable because it is
// written by whichever goroutine calls StartObservers and read from an AppKit-owned thread, and
// rather than a mutex because the read is on the notification path: a lock there would let a slow
// StopObservers block an AX callback, which is the one thing docs/ARCHITECTURE.md#the-cgo-rule says
// must never happen.
var observeCallback atomic.Pointer[func()]

// Serialises StartObservers against StopObservers. Deliberately NOT on the notification path: the
// callback reads observeCallback atomically and takes no lock at all, because a lock there would let
// a slow StopObservers block an AX callback. This mutex exists only so the C side never sees a start
// and a stop overlap -- it owns a thread, a run loop and two semaphores whose handover is written
// assuming exactly that.
var observeMu sync.Mutex

// ErrNilCallback is returned by StartObservers when onChange is nil. Registering observers whose
// notifications go nowhere would install the mechanism and silently discard it, which is exactly the
// failure this package is built to avoid.
var ErrNilCallback = errors.New("darwin: start observers: onChange must not be nil")

// StartObservers watches every regular application and calls onChange when the window set may have
// changed. It registers for kAXWindowCreated, kAXUIElementDestroyed, kAXFocusedWindowChanged,
// kAXWindowMiniaturized and kAXWindowDeminiaturized on each application, and follows applications
// launching and quitting so the set does not go stale.
//
// onChange runs on an AppKit-owned thread and MUST return immediately: it is called from the observer
// run loop that delivers every application's notifications, so anything it blocks on blocks all of
// them. Loop.Rescan is a depth-1 latch and never blocks (D22); onChange must be of that shape.
//
// onChange is a hint, not a diff. It says the window set may have changed, never what changed --
// deciding that is the event loop's job, from a fresh enumeration. Expect it to fire more often than
// the set actually changes, and to coalesce nothing.
//
// Requires the Accessibility grant and returns ErrNotTrusted without it. A second call while running
// replaces onChange and returns nil; it does not re-register anything. Calling from any goroutine is
// safe.
//
// It ALSO requires the process to run a main run loop -- AppKit's, or CFRunLoopRun on the main thread
// -- and this is the one precondition it cannot report. NSWorkspace posts its launch and terminate
// notifications from that loop only; measured with no main run loop, launching an application
// delivers nothing at all, so the application is never observed. Everything else keeps working, which
// makes the symptom "windows from apps started before gotab, and no others". See observe.h.
//
// Applications with an .Accessory or .Prohibited activation policy are not observed, the same filter
// and the same known gap as AXWindowList (D20).
func StartObservers(onChange func()) error {
	if onChange == nil {
		return ErrNilCallback
	}
	observeMu.Lock()
	defer observeMu.Unlock()
	// Stored before the C side starts, never after: a notification can arrive on the observer thread
	// during gt_observers_start's initial sweep, and a callback that arrives before the pointer is
	// published would be dropped.
	observeCallback.Store(&onChange)
	if err := statusError("start observers", C.gt_observers_start()); err != nil {
		observeCallback.Store(nil)
		return err
	}
	return nil
}

// StopObservers removes every observer and its run-loop source. It does not return until the observer
// thread has torn down, so after it returns onChange will not be called again. Safe to call when
// nothing was started, and safe to call twice.
//
// The guarantee is "not called again", not "not currently running": onChange is invoked from the
// observer thread, and a call already inside it when the thread was told to stop finishes normally
// before the teardown completes. Since onChange must return immediately anyway (D22), that window is
// the length of a channel send.
func StopObservers() {
	observeMu.Lock()
	defer observeMu.Unlock()
	C.gt_observers_stop()
	observeCallback.Store(nil)
}

//export goObserveChange
func goObserveChange() {
	// The whole Go side of the notification path. Nothing here allocates, and nothing here decides
	// anything: this is the 38 ns C->Go crossing measured in docs/ARCHITECTURE.md#the-cgo-rule, and
	// it happens once per AX notification from every application on the machine.
	if fn := observeCallback.Load(); fn != nil {
		(*fn)()
	}
}
