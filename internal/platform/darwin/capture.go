package darwin

/*
// ScreenCaptureKit is linked here rather than in shim.go because this is the only file that needs it,
// and the link is HARD by default. That is a compromise, not a choice.
//
// cgo rejects `-weak_framework ScreenCaptureKit` and `-Wl,-weak_framework,ScreenCaptureKit` alike
// unless CGO_LDFLAGS_ALLOW permits them, and exporting that variable for every plain `go build`,
// `go test` and `go vet` would break the commands AGENTS.md documents. SCK arrives in macOS 12.3 and
// D17 pins minos to 12.0, so a hard-linked binary does not merely fail to capture on 12.0-12.2 — it
// fails to LAUNCH there, because dyld cannot find the framework.
//
// The `gotab_weak_sck` build tag selects the weak link instead, under which the framework's classes
// resolve to NULL when it is absent and gt_capture reports GT_ERR_UNAVAILABLE:
//
//	CGO_LDFLAGS_ALLOW='-Wl,-weak_framework.*' go build -tags gotab_weak_sck ./cmd/gotab
//
// scripts/build.sh must do that before Phase 2 ships anything; `otool -L` shows the framework marked
// `weak` when it worked. The default stays hard so that the gate keeps running with no environment.
#cgo !gotab_weak_sck LDFLAGS: -framework ScreenCaptureKit
#cgo gotab_weak_sck  LDFLAGS: -Wl,-weak_framework,ScreenCaptureKit
#include "capture.h"
*/
import "C"

import (
	"fmt"
	"time"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
)

// captureTimeout bounds each blocking phase of a capture, so a call that has to refresh the window
// cache first can take twice this before it gives up.
//
// It exists because D12 watched a capture never answer at all: once in roughly ten runs of
// spike/sck, unreproducibly, the completion handler simply did not fire within ten seconds. A
// switcher that inherits that hang stops prefetching for the rest of the session.
//
// Two seconds is roughly 18x the ~112 ms cold capture D12 measured and 40x the ~46 ms warm one.
// It is deliberately generous rather than tight: capture never runs on the summon path (D12), so
// the cost of waiting too long is one background goroutine parked, not a late panel. **assumption:
// the number is headroom-over-measurement, not a measured tail latency** — nothing has yet observed
// what the slow end of a real capture distribution looks like under load.
const captureTimeout = 2 * time.Second

// Capture grabs a thumbnail of one window, downscaled at capture time to at most maxWidth pixels
// wide, and returns a handle the caller MUST Release exactly once. Aspect ratio is preserved and the
// image is never upscaled: maxWidth is a bound, not a target.
//
// Blocking — ~46 ms warm, ~112 ms cold (D12) — and it holds an OS thread for that whole time. It is
// safe to call from any goroutine and from several at once, but concurrency buys almost nothing:
// SCScreenshotManager serialises in the WindowServer, where ten captures at once cost 324 ms against
// 460 ms serial. **Never call this on the summon path.** Enumeration alone is ~46 ms against a 100 ms
// budget, so a summon can afford zero fresh captures; this API exists to fill P1.7's cache *ahead* of
// a summon, driven by the window notifications of P2.3/P2.4.
//
// Failure is ordinary here, not exceptional. A window can close between being listed and being
// captured, and errors.Is against the shim's sentinels is how a caller tells the cases apart:
// ErrNoRecording is the user's to fix, ErrTimeout is worth retrying later, ErrUnavailable usually
// means the window is gone (or that SCScreenshotManager is absent, below macOS 14), and ErrInternal
// is a bug in this package.
func Capture(id core.WindowID, maxWidth int) (ImageRef, error) {
	var img C.gt_image_ref
	st := C.gt_capture(
		C.uint32_t(id),
		C.int32_t(maxWidth),
		C.int32_t(captureTimeout/time.Millisecond),
		&img,
	)
	if err := statusError(fmt.Sprintf("capture window %d", id), st); err != nil {
		return ImageRef{}, err
	}
	return ImageRef{c: img}, nil
}

// Size reports the pixel dimensions of the bitmap, or 0, 0 for a released or zero handle.
//
// It reads the CGImage header and touches no pixels, which matters: SCK output is IOSurface-backed
// and mapped-but-not-resident (D12), so an accessor that read the bitmap would fault megabytes back
// in to answer a question about two integers.
//
// This is also how a caller checks that the downscale happened at capture time rather than trusting
// that it did — the returned width is what SCStreamConfiguration produced, not what was asked for.
func (r *ImageRef) Size() (w, h int) {
	if r == nil || r.c == nil {
		return 0, 0
	}
	var cw, ch C.int32_t
	C.gt_image_size(r.c, &cw, &ch)
	return int(cw), int(ch)
}

// There is deliberately no runtime.SetFinalizer backstop on ImageRef, and doc.go anticipated one
// landing with the first producer. It cannot: Capture returns an ImageRef by value, so there is no
// stable heap object to attach a finalizer to. Attaching one to a local before copying it out would
// arm a finalizer that frees the bitmap while the caller's copy still points at it — a
// use-after-free introduced by the leak detector. A debug-build backstop needs ImageRef to be
// handed out as a pointer, which is a change to a frozen type and to the P2.6 contract's signature,
// so it is left to whoever revisits that. ARCHITECTURE.md is unaffected either way: the finalizer
// was never the mechanism, only ever a detector.
