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

// List appends every switchable window to dst and returns the extended slice, in one cgo crossing
// regardless of how many windows there are. Pass a slice retained from the previous call, truncated to
// zero length, to reuse its capacity.
//
// The returned windows carry what CGWindowList knows: identity, owner, title, and whether the window is
// on screen. They do NOT carry Minimized, Hidden, Fullscreen, Tabbed, Space or focus order — those come
// from Accessibility (P2.3) and SkyLight (P2.4), and this layer leaves the bits clear rather than
// guessing. An unset flag here means "not known yet", not "false".
//
// Titles come back empty for other applications' windows without the Screen Recording grant. That is
// not an error; check Permissions.ScreenRecording to tell an untitled window from an unpermitted one.
//
// **This is not the switchable set.** Layer 0 is necessary and nowhere near sufficient: measured on a
// normal session it returned 59 windows of which 7 were things a user could switch to (D19). The rest
// are XPC view services, offscreen helpers and per-app auxiliary windows. Accessibility decides what is
// really switchable (P2.3); this list is the candidate set it starts from.
//
// This allocates: a Go string per title and per app name, which no packed representation avoids. That
// is acceptable here and only here — enumeration is not the hot path, and D5 established that its cold
// cost is paid at launch rather than on first summon. Summon and cycling must stay allocation-free.
func (l *Lister) List(dst []core.Window) ([]core.Window, error) {
	var n, total C.int32_t

	truncated := false
	for attempt := 0; ; attempt++ {
		// &l.buf[0] is a Go pointer to memory containing no Go pointers, which cgo permits, and C
		// does not retain it past the call. gt_window is fixed-size for exactly this reason.
		st := C.gt_window_list(&l.buf[0], C.int32_t(len(l.buf)), &n, &total)
		if err := statusError("window list", st); err != nil {
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
		var flags core.WindowFlags
		if w.on_screen != 0 {
			flags |= core.FlagOnScreen
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
		return dst, fmt.Errorf("darwin: window list: %w (%d of %d)", ErrTruncated, n, total)
	}
	return dst, nil
}
