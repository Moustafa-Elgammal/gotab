package darwin

/*
#cgo CFLAGS: -x objective-c -Wall -Wextra -Wno-unused-parameter -Werror=unguarded-availability-new
// -Werror=unguarded-availability-new: this package targets minos 12.0 (D17) and reaches 12.3+ /
// 14.0 APIs behind weak links (capture.go) that resolve to NULL on an older OS. Every such call MUST
// sit in an if (@available) block or NSClassFromString-guard, or the NULL is messaged and the
// behaviour is undefined. A missing guard is a bug, not a warning — it fails the build. Only bites
// under build.sh's MACOSX_DEPLOYMENT_TARGET=12.0; a plain `go build` uses the SDK's own version.
#cgo LDFLAGS: -framework Foundation -framework AppKit -framework ApplicationServices -framework CoreGraphics
#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"
)

// The five failures the platform can report. They are sentinels rather than strings because callers
// act differently on them: a missing grant is something the user fixes (P4.3 walks them through it), a
// timeout is something to retry, and GT_ERR_INTERNAL is a bug in this package. Wrap with %w and match
// with errors.Is.
var (
	ErrNotTrusted  = errors.New("Accessibility is not granted to the responsible process")
	ErrNoRecording = errors.New("Screen Recording is not granted to the responsible process")
	ErrUnavailable = errors.New("the OS declined to answer")
	ErrTimeout     = errors.New("the WindowServer did not answer in time")
	ErrInternal    = errors.New("a framework returned something this shim does not model")
)

// statusError is the single place a C status becomes a Go error. Every gt_ call routes through it, so
// adding a status code means editing one switch rather than auditing call sites.
func statusError(op string, s C.gt_status) error {
	if s == C.GT_OK {
		return nil
	}
	var base error
	switch s {
	case C.GT_ERR_NOT_TRUSTED:
		base = ErrNotTrusted
	case C.GT_ERR_NO_RECORDING:
		base = ErrNoRecording
	case C.GT_ERR_UNAVAILABLE:
		base = ErrUnavailable
	case C.GT_ERR_TIMEOUT:
		base = ErrTimeout
	default:
		// Including GT_ERR_INTERNAL. An unrecognised code is the same class of problem as an
		// unmodelled one, and reporting the number is what makes it diagnosable.
		base = fmt.Errorf("%w (status %d)", ErrInternal, int32(s))
	}
	return fmt.Errorf("darwin: %s: %w", op, base)
}

// Init performs one-time process setup and must be called before anything else in this package.
// Idempotent and safe from any goroutine. It does not request any permission and does not prompt.
func Init() error { return statusError("init", C.gt_init()) }

// Permissions is the TCC state, as of the moment it was asked. It is not stable for the life of the
// process: the user can revoke a grant from System Settings while the app runs, and macOS relaunches
// some apps on grant (that behaviour cost a 90-sample run during P0.7). Re-read it, don't cache it.
type Permissions struct {
	Accessibility   bool // required to observe and raise windows
	ScreenRecording bool // required for thumbnails, and for window *titles* of other apps
}

// OK reports whether both grants are held, which is the only state in which the switcher is fully
// functional. Missing Screen Recording degrades gracefully to titles-and-icons; missing Accessibility
// does not degrade at all.
func (p Permissions) OK() bool { return p.Accessibility && p.ScreenRecording }

// CheckPermissions reads the TCC state without prompting. Both answers concern the *responsible*
// process, which under `go run` is the terminal rather than this binary — a distinction that makes an
// ungranted event tap install cleanly and then never fire (docs/PLATFORM-LESSONS.md section 5).
func CheckPermissions() Permissions {
	return Permissions{
		Accessibility:   C.gt_trusted() != 0,
		ScreenRecording: C.gt_can_record() != 0,
	}
}

// ---------------------------------------------------------------------------
// Bitmap handles
// ---------------------------------------------------------------------------

// ImageRef is an opaque handle to a bitmap that lives outside the Go heap. The GC sees this struct's
// eight bytes and feels no pressure from the megabytes behind them, which is why Release is mandatory
// and not advisory — see docs/ARCHITECTURE.md#the-memory-rule.
//
// Nothing produces an ImageRef until P2.6. The type exists now so that the task which starts
// allocating megabytes is not also the task deciding how they are freed.
type ImageRef struct {
	c C.gt_image_ref
}

// Release frees the bitmap. Safe to call on a zero ImageRef and safe to call twice: the handle is
// cleared first, so the second call is a no-op rather than the double free it would otherwise be.
// That is the whole reason callers hold this struct instead of the raw pointer.
func (r *ImageRef) Release() {
	if r == nil || r.c == nil {
		return
	}
	c := r.c
	r.c = nil
	C.gt_image_release(c)
}

// Valid reports whether the handle still refers to a bitmap.
func (r *ImageRef) Valid() bool { return r != nil && r.c != nil }

// LiveImages is how many bitmaps this process is holding. The leak assertion: once a summon has
// completed and the cache has evicted, this returns to the cache bound and not to something larger.
// V6.4 is where that becomes a measurement.
func LiveImages() int64 { return int64(C.gt_image_live()) }

// ---------------------------------------------------------------------------
// Threading
// ---------------------------------------------------------------------------

// OnMain runs fn on the main thread and returns immediately, without waiting for it. Every AppKit
// mutation from a Go goroutine goes through here; touching AppKit from anywhere else asserts at
// runtime. See docs/ARCHITECTURE.md#threading.
//
// Asynchronous on purpose. A synchronous hop would deadlock whenever the main thread is itself waiting
// on the event-loop goroutine, and that deadlock only shows up under load.
func OnMain(fn func()) {
	if fn == nil {
		return
	}
	// cgo.Handle rather than a pointer into the Go heap: a Go pointer may not be stored in C, and the
	// block below outlives this call. The handle is deleted on the other side, exactly once.
	C.gt_dispatch_main(C.uintptr_t(cgo.NewHandle(fn)))
}

//export goDispatchMain
func goDispatchMain(token C.uintptr_t) {
	h := cgo.Handle(token)
	defer h.Delete()
	// Deliberately not recovered. A panic on the main thread during a UI mutation is a bug that must
	// be loud; swallowing it leaves AppKit in a state nobody can reason about afterwards.
	h.Value().(func())()
}
