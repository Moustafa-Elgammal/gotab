package darwin

/*
#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
)

// ErrTruncated means the window list grew faster than the buffer could be grown to hold it, so some
// windows were dropped. Returned alongside the windows that were captured: a partial list the caller
// knows is partial is useful, and one it believes is complete is not.
var ErrTruncated = errors.New("the window list grew faster than the buffer")

// listAttempts bounds the grow-and-retry loop. Two passes is the realistic worst case — the first
// learns the true count, the second has room for it. More than that means windows are being created
// faster than they can be enumerated, which is a state to report rather than to spin on.
const listAttempts = 4

// Source selects which of the two enumerations a Lister runs. They are not interchangeable and the
// difference is the subject of D19: Accessibility knows which windows a user can switch to, and
// CoreGraphics knows the numbers everything downstream is keyed by.
type Source int

const (
	// FromAX enumerates kAXWindowsAttribute per regular application. This is the switchable set and
	// the one the switcher is built on. Needs the Accessibility grant; without it List returns
	// ErrNotTrusted rather than an empty list.
	FromAX Source = iota

	// FromCoreGraphics enumerates CGWindowList. It over-reports by roughly 8x (D19) and its titles
	// need the Screen Recording grant, but it is the only source for on-screen state and it needs no
	// Accessibility grant at all. Kept for that, and for comparing the two.
	FromCoreGraphics
)

// Lister enumerates windows. It owns the buffer C writes into and reuses it across calls, so a steady
// state costs no allocation for the records themselves.
//
// Not safe for concurrent use, and deliberately so: one event-loop goroutine owns the window model and
// therefore owns this (docs/ARCHITECTURE.md#threading). A Lister behind a mutex is a sign the caller
// has the threading model wrong.
type Lister struct {
	buf []C.gt_window
}

// NewLister returns a Lister sized for a typical session. The buffer grows on demand and never
// shrinks, so the capacity here only decides how many enumerations happen before it settles.
func NewLister() *Lister {
	return &Lister{buf: make([]C.gt_window, 128)}
}

// List appends the windows src reports to dst and returns the extended slice, in one cgo crossing
// regardless of how many windows there are. Pass a slice retained from the previous call, truncated to
// zero length, to reuse its capacity.
//
// What comes back depends on the source, and neither one is complete:
//
//   - FromAX: identity, owner, title, Minimized and Hidden. Not on-screen state — Accessibility does
//     not report it. Needs the Accessibility grant.
//   - FromCoreGraphics: identity, owner, title and on-screen state. Not Minimized or Hidden. Titles of
//     other applications' windows need the Screen Recording grant.
//
// Neither carries Fullscreen, Tabbed, Space or focus order; those are P2.4's and the event loop's.
// **An unset flag means "not known from this source", never "false".**
//
// Titles come back empty for other applications' windows without the Screen Recording grant. That is
// not an error; check Permissions.ScreenRecording to tell an untitled window from an unpermitted one.
//
// FromCoreGraphics is not the switchable set. Layer 0 is necessary and nowhere near sufficient:
// measured on a normal session it returned 59 windows of which 7 were switchable (D19). Both sources
// key on CGWindowID, so the two lists can be joined.
//
// This allocates: a Go string per title and per app name, which no packed representation avoids. That
// is acceptable here and only here — enumeration is not the hot path, and D5 established that its cold
// cost is paid at launch rather than on first summon. Summon and cycling must stay allocation-free.
func (l *Lister) List(dst []core.Window, src Source) ([]core.Window, error) {
	var n, total C.int32_t

	op := "ax window list"
	if src == FromCoreGraphics {
		op = "window list"
	}

	truncated := false
	for attempt := 0; ; attempt++ {
		// &l.buf[0] is a Go pointer to memory containing no Go pointers, which cgo permits, and C
		// does not retain it past the call. gt_window is fixed-size for exactly this reason.
		var st C.gt_status
		if src == FromCoreGraphics {
			st = C.gt_window_list(&l.buf[0], C.int32_t(len(l.buf)), &n, &total)
		} else {
			st = C.gt_ax_window_list(&l.buf[0], C.int32_t(len(l.buf)), &n, &total)
		}
		if err := statusError(op, st); err != nil {
			return dst, err
		}
		if int(total) <= len(l.buf) {
			break
		}
		if attempt == listAttempts-1 {
			truncated = true
			break
		}
		// Grow past the reported total: between the count and the retry, windows keep opening.
		l.buf = make([]C.gt_window, int(total)+int(total)/4+8)
	}

	for i := 0; i < int(n); i++ {
		w := &l.buf[i]
		// Only bits this source actually knows are set. A cleared flag means "not known from here",
		// never "false" — the C record documents which fields each source fills, and turning an
		// unknown into a cleared flag is how a switcher ends up hiding a window it should show.
		var flags core.WindowFlags
		if w.on_screen != 0 {
			flags |= core.FlagOnScreen
		}
		if w.minimized != 0 {
			flags |= core.FlagMinimized
		}
		if w.hidden != 0 {
			flags |= core.FlagHidden
		}
		dst = append(dst, core.Window{
			ID:      core.WindowID(w.id),
			App:     core.AppID(w.pid),
			Flags:   flags,
			Title:   C.GoStringN(&w.title[0], C.int(w.title_len)),
			AppName: C.GoStringN(&w.app[0], C.int(w.app_len)),
		})
	}

	if truncated {
		return dst, fmt.Errorf("darwin: %s: %w (%d of %d)", op, ErrTruncated, n, total)
	}
	return dst, nil
}

// Standard reports, for the window at index i of the most recent List, whether its Accessibility
// subrole is AXStandardWindow — as opposed to a dialog, sheet or palette. It is not a core.WindowFlags
// bit because api.go is frozen (P1.0) and adding one is an escalation, not an edit. Meaningless after a
// FromCoreGraphics list, which cannot know it.
func (l *Lister) Standard(i int) bool { return i < len(l.buf) && l.buf[i].standard != 0 }
