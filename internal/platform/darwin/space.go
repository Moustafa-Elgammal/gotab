package darwin

/*
#include "space.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
)

// spaceAttempts bounds Spaces' grow-and-retry loop, for the same reason listAttempts bounds the
// window list's: two passes is the realistic worst case, and more than that means the user is
// creating Spaces faster than they can be enumerated.
const spaceAttempts = 4

// CurrentSpace returns the Space the user is looking at, or 0 if it cannot be determined.
//
// Zero is a real answer and callers must handle it: SkyLight is private (see space.h on why this
// package resolves it at runtime rather than linking it), and a process without a window server
// session has no current Space at all. core.SpaceID's contract is that 0 is unknown and never a
// Space — do not compare it for equality with a window's Space and conclude anything.
//
// With Displays Have Separate Spaces enabled there is no single current Space; this reports the one
// on the display that owns the menubar, which is where a summoned panel appears. Measured on a
// single-display machine the per-display answer and the connection-wide CGSGetActiveSpace agreed;
// with one display that agreement is weak evidence, and the multi-display path stays unconfirmed.
//
// Call Init first, like everything else in this package.
func CurrentSpace() core.SpaceID { return core.SpaceID(C.gt_current_space()) }

// SpacesOf returns the Space of each window in ids, index-aligned: the result has exactly len(ids)
// entries and result[i] is the Space of ids[i]. 0 means unknown.
//
// dst is truncated and refilled, so passing the slice from the previous call reuses its capacity and
// a steady state allocates nothing.
//
// One cgo crossing regardless of len(ids). The loop over the WindowServer happens on the C side,
// because CGSCopySpacesForWindows returns a deduplicated *set* for a batch of windows and cannot be
// aligned to its input — 63 ids in one call came back as a 1-element array, and space.h records the
// measurement. Budget 22–87 µs per window; the spread is WindowServer IPC and is real.
//
// Unknown is not an error and is by far the common case: measured on an ordinary session, 46–54 of
// 54–63 layer-0 windows were on no Space the WindowServer would name — offscreen buffers, popovers
// and backing stores that were never mapped onto one. An error here means SkyLight itself was
// unreachable, which makes every entry unknown rather than some of them.
//
// The 0 is trustworthy in the direction that matters: window ids that cannot exist come back 0 in the
// same call that returns 1 for real windows, so the API declines rather than defaulting to the
// current Space, and this function never invents one.
func SpacesOf(ids []core.WindowID, dst []core.SpaceID) ([]core.SpaceID, error) {
	dst = dst[:0]
	if len(ids) == 0 {
		return dst, nil
	}
	if cap(dst) < len(ids) {
		dst = make([]core.SpaceID, len(ids))
	}
	dst = dst[:len(ids)]

	// Both slices hold fixed-width integers and no Go pointers, which is what makes passing their
	// addresses to C legal; C does not retain either past the call. core.WindowID is uint32 and
	// core.SpaceID is uint64, both frozen in internal/core/api.go, so the layouts match the C
	// prototypes exactly and the conversion is a reinterpretation rather than a copy.
	st := C.gt_spaces_of(
		(*C.uint32_t)(unsafe.Pointer(&ids[0])),
		C.int32_t(len(ids)),
		(*C.uint64_t)(unsafe.Pointer(&dst[0])),
	)
	if err := statusError("spaces of", st); err != nil {
		return dst, err
	}
	return dst, nil
}

// Spaces returns every Space the WindowServer manages, across all displays, ordered by display and
// then by the display's own order.
//
// Diagnostic, not a hot path. It exists because "this window is on Space 1" says nothing on its own:
// whether that is interesting depends entirely on how many Spaces there are, and that is the question
// D20 left open. `gotab -list` is expected to print a Space per window; this is what makes those
// numbers readable — and on the machine P2.4 measured it returned a single Space, which is precisely
// what let the D20 finding be stated rather than guessed at again.
//
// Returns ErrTruncated alongside a partial list if Spaces were created faster than they could be
// counted — the same contract as Lister.List, and for the same reason: a caller that knows its list
// is partial can say so, and one that believes it is complete cannot.
func Spaces() ([]core.SpaceID, error) {
	buf := make([]C.uint64_t, 16)
	var n, total C.int32_t

	truncated := false
	for attempt := 0; ; attempt++ {
		st := C.gt_space_list(&buf[0], C.int32_t(len(buf)), &n, &total)
		if err := statusError("space list", st); err != nil {
			return nil, err
		}
		if int(total) <= len(buf) {
			break
		}
		if attempt == spaceAttempts-1 {
			truncated = true
			break
		}
		buf = make([]C.uint64_t, int(total)+4)
	}

	out := make([]core.SpaceID, int(n))
	for i := range out {
		out[i] = core.SpaceID(buf[i])
	}
	if truncated {
		return out, fmt.Errorf("darwin: space list: %w (%d of %d)", ErrTruncated, n, total)
	}
	return out, nil
}
