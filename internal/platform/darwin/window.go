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

// Policy reports the owning application's NSApplicationActivationPolicy for the window at index i of
// the most recent List: 0 regular, 1 accessory, 2 prohibited, -1 if the process had already gone.
// Same indexing caveat as Standard.
func (l *Lister) Policy(i int) int32 {
	if i < 0 || i >= len(l.buf) {
		return -1
	}
	return int32(l.buf[i].policy)
}

// ---------------------------------------------------------------------------
// P2.3c — the join
// ---------------------------------------------------------------------------

// Origin says which enumeration a window came from. D20 is why this is worth keeping: neither source
// is complete, so where a window came from is diagnostic information, not trivia.
type Origin uint8

const (
	// OriginBoth — Accessibility and CoreGraphics agree it exists. The ordinary case.
	OriginBoth Origin = iota
	// OriginAXOnly — Accessibility reported it and CoreGraphics did not. Rare; a window that exists
	// to AX but has no WindowServer entry is usually mid-creation.
	OriginAXOnly
	// OriginCGOnly — CoreGraphics reported it and Accessibility did not. **The interesting case.**
	// D20 measured two of these — a second Chrome window and an .Accessory app's window — with both
	// AX calls succeeding and simply answering short. Another Space is the likeliest explanation and
	// is still unconfirmed; P2.4 is what turns that into a number.
	OriginCGOnly
)

func (o Origin) String() string {
	switch o {
	case OriginBoth:
		return "both"
	case OriginAXOnly:
		return "ax"
	default:
		return "cg"
	}
}

// Exclusion is a CoreGraphics window that did not make the list, and why. P2.3c's contract is that
// every titled layer-0 window is either in the list or excluded for a reason the code can name, and
// this is the half that names it. Diagnostic only — nothing on the summon path builds these.
type Exclusion struct {
	ID      core.WindowID
	AppName string
	Title   string
	Reason  string
}

// Enumerator produces the switchable window list by joining both enumerations on CGWindowID.
//
// Neither source alone is correct (D20): CoreGraphics sees every window and cannot say which are
// switchable, Accessibility says which are switchable and cannot see every window. Two crossings —
// one per source — regardless of how many windows there are.
//
// Not safe for concurrent use, for the same reason Lister is not.
type Enumerator struct {
	ax, cg   *Lister
	axWins   []core.Window
	cgWins   []core.Window
	cgRow    map[core.WindowID]int
	origins  []Origin
	excluded []Exclusion
	ids      []core.WindowID // scratch for the Space lookup
	spaces   []core.SpaceID
}

func NewEnumerator() *Enumerator {
	return &Enumerator{
		ax:    NewLister(),
		cg:    NewLister(),
		cgRow: make(map[core.WindowID]int, 128),
	}
}

// Enumerate appends the switchable windows to dst and returns the extended slice.
//
// A window enters the list if Accessibility reported it, or — when only CoreGraphics did — if its
// owning process is an application (activation policy is not `prohibited`) **and** the window has a
// title. Both halves are load-bearing and neither is sufficient: policy alone admitted 37 untitled
// auxiliary windows from Chrome, Finder, Terminal and GoLand, and title alone would admit any XPC
// service that has one.
//
// **Known limitation, and it is a TCC dependency (D21).** CoreGraphics titles need Screen Recording.
// Without that grant every CoreGraphics-only title is empty, so this branch admits nothing and the
// result degrades to exactly the Accessibility list — which means losing the windows AX cannot see,
// D20's other-Space case among them. That is a real degradation with a nameable cause rather than a
// silent one, and MissingRecovery reports when it is in effect.
//
// Returns ErrNotTrusted when Accessibility is not granted, rather than degrading to the CoreGraphics
// list. That list is ~8x noise (D19), and a switcher cannot raise a window without the grant anyway,
// so quietly showing a worse list would hide the one problem the user can fix.
//
// Each returned window carries its Space (P2.4), filled in one extra crossing; 0 means the
// WindowServer would not name one, which core.Rules reads as "do not filter by Space".
func (e *Enumerator) Enumerate(dst []core.Window) ([]core.Window, error) {
	start := len(dst)

	var err error
	e.axWins, err = e.ax.List(e.axWins[:0], FromAX)
	if err != nil && !errors.Is(err, ErrTruncated) {
		return dst, err
	}
	e.cgWins, err = e.cg.List(e.cgWins[:0], FromCoreGraphics)
	if err != nil && !errors.Is(err, ErrTruncated) {
		return dst, err
	}

	clear(e.cgRow)
	for i, w := range e.cgWins {
		e.cgRow[w.ID] = i
	}

	e.origins = e.origins[:0]
	e.excluded = e.excluded[:0]

	// AX first: it is the authority on what is switchable. CoreGraphics contributes the on-screen
	// bit, which AX does not report at all — that is the join earning its keep rather than just
	// reconciling two lists.
	seen := make(map[core.WindowID]bool, len(e.axWins))
	for _, w := range e.axWins {
		seen[w.ID] = true
		origin := OriginAXOnly
		if j, ok := e.cgRow[w.ID]; ok {
			origin = OriginBoth
			w.Flags |= e.cgWins[j].Flags & core.FlagOnScreen
			// CoreGraphics titles come from the WindowServer and AX titles from the app. When AX
			// has none, take the other rather than showing the user a blank row.
			if w.Title == "" {
				w.Title = e.cgWins[j].Title
			}
		}
		e.origins = append(e.origins, origin)
		dst = append(dst, w)
	}

	for i, w := range e.cgWins {
		if seen[w.ID] {
			continue
		}
		switch p := e.cg.Policy(i); {
		case p == 2: // NSApplicationActivationPolicyProhibited — an XPC or view service
			e.note(w, "owning process is not an application (activation policy: prohibited)")
		case p < 0:
			e.note(w, "owning process had exited before it could be identified")
		case w.Title == "":
			// Both conditions are needed and neither is sufficient. Policy alone admitted 37
			// untitled auxiliary windows belonging to Chrome, Finder, Terminal and GoLand —
			// offscreen buffers and popovers that no user can switch to. Title alone would admit
			// an XPC service that happens to have one.
			e.note(w, "no title, and Accessibility did not report it")
		default:
			// A titled window of a real application that Accessibility did not report. D20's case:
			// a second Chrome window and an .Accessory app's window, both recovered here.
			e.origins = append(e.origins, OriginCGOnly)
			dst = append(dst, w)
		}
	}

	e.fillSpaces(dst[start:])
	return dst, nil
}

// fillSpaces asks the WindowServer which Space each window in `wins` is on and writes it back, in one
// cgo crossing (P2.4). A SkyLight failure or an unmapped window leaves Space at 0 — "unknown", which
// core.Rules treats as "do not filter by Space" — so this never fails the enumeration.
func (e *Enumerator) fillSpaces(wins []core.Window) {
	if len(wins) == 0 {
		return
	}
	e.ids = e.ids[:0]
	for _, w := range wins {
		e.ids = append(e.ids, w.ID)
	}
	var err error
	e.spaces, err = SpacesOf(e.ids, e.spaces)
	if err != nil {
		return // every Space stays 0; unknown, not off-Space
	}
	for i := range wins {
		wins[i].Space = e.spaces[i]
	}
}

func (e *Enumerator) note(w core.Window, reason string) {
	e.excluded = append(e.excluded, Exclusion{
		ID: w.ID, AppName: w.AppName, Title: w.Title, Reason: reason,
	})
}

// Origins is index-aligned with the windows the last Enumerate appended. Valid until the next call.
func (e *Enumerator) Origins() []Origin { return e.origins }

// Excluded lists the CoreGraphics windows the last Enumerate rejected, each with its reason.
func (e *Enumerator) Excluded() []Exclusion { return e.excluded }

// MissingRecovery reports whether the CoreGraphics-only recovery path is disabled for want of the
// Screen Recording grant — that is, whether this enumeration could be missing windows Accessibility
// cannot see. True means the list may be short and the reason is fixable by the user.
//
// It is a query rather than a returned error because it is not an error: the list is still correct as
// far as it goes, and every window in it is real.
func (e *Enumerator) MissingRecovery() bool {
	return !CheckPermissions().ScreenRecording
}
