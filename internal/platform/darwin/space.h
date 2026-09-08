// Spaces: which Space a window is on, and which Space the user is looking at.
//
// Separate from shim.h because everything here goes through SkyLight, a PRIVATE framework, and the
// separation is the point: shim.h is the surface that will still exist if these symbols disappear.
// Same conventions as shim.h -- gt_ prefix, gt_status for anything that can fail, one crossing for N
// items into a caller-allocated buffer.
//
// Zero means UNKNOWN everywhere in this file, never "not on a Space" and never a real Space.
// internal/core/api.go freezes that meaning for core.SpaceID and this file is where it originates:
// SkyLight can be absent, can refuse a connection, and can answer with an empty set for a window that
// exists. All three are "unknown", and a switcher that turned any of them into a Space number would
// filter windows out of the user's list on invented evidence.
#ifndef GOTAB_DARWIN_SPACE_H
#define GOTAB_DARWIN_SPACE_H

#include <stdint.h>

#include "shim.h"

// The Space the user is currently looking at, or 0 if it cannot be determined.
//
// "Current" is per display when Displays Have Separate Spaces is on, so this reports the Space of the
// display that owns the menubar -- the one a summon would appear on. Falls back to the connection's
// active Space if the per-display query is unavailable.
uint64_t gt_current_space(void);

// Fills out[i] with the Space of window ids[i], index-aligned, in ONE crossing. 0 means unknown.
//
// This loops internally, and that is not an oversight. CGSCopySpacesForWindows takes an array of
// window ids and returns the SET of Spaces they occupy -- deduplicated and unordered, so N ids come
// back as one element per distinct Space and nothing can be aligned to anything. Measured on this
// machine (P2.4): 54, 62 and 63 ids passed in one call each returned a ONE-element array. The
// batching rule in docs/ARCHITECTURE.md is about cgo crossings, which cost 31 ns each and are what
// this file is avoiding; the loop over the WindowServer runs on the C side of exactly one of them.
//
// Measured cost, five runs over a live 54-63 window session: 1.4-4.8 ms total, 22-87 us per window.
// The spread is real and is WindowServer IPC, not noise in the timer -- budget the top of the range.
//
// A window can be on more than one Space (assigned to All Desktops, or mid-drag between two). Where
// that happens and one of them is the current Space, the current Space is reported -- that is the one
// the user can see it on. Otherwise the first the WindowServer names. NOT MEASURED: this machine had
// exactly one Space, so the multi-Space branch is reasoned, not confirmed. See docs/tasks/P2.4.md.
//
// An empty answer for a window is reported as 0, not as an error. It is a real and common state:
// measured on a normal session, 46-54 of 54-63 layer-0 windows were on no Space at all -- offscreen
// buffers and popovers that were never mapped onto one.
//
// That 0 is a genuine "the WindowServer does not place this window", not a silent default. Verified
// by asking for window ids that cannot exist (999999, 999998, 1, 2): all four came back 0 while real
// windows in the same call came back 1. The API declines rather than guessing, and so does this.
gt_status gt_spaces_of(const uint32_t *ids, int32_t n, uint64_t *out);

// Every Space the WindowServer manages, across all displays, with the same buffer contract as
// gt_window_list: up to cap ids into buf, how many were stored in *out_n, how many exist in
// *out_total. Ordered by display, then by the display's own Space order.
//
// Diagnostic. Nothing on the summon path needs it; it exists because "this window is on Space 1" is
// uninterpretable without knowing whether the machine has one Space or nine, and that question is
// exactly what P2.4 was asked to settle. On the machine P2.4 measured, the answer was one -- which is
// what made the D20 finding readable at all, and is why this stays in the surface rather than being
// deleted as unused.
//
// Reads "id64", falling back to "ManagedSpaceID". Both keys were present and both were 1 on the
// measured machine, so which one wins is UNTESTED where they could differ.
gt_status gt_space_list(uint64_t *buf, int32_t cap, int32_t *out_n, int32_t *out_total);

// A lead, recorded so the next person does not have to find it independently (the D21 convention).
//
// CGSCopyWindowsWithOptionsAndTags inverts the question -- given a Space, which windows are on it --
// and so costs one WindowServer round trip per SPACE rather than per window, which is 1-5 instead of
// the 54-63 above. It works: with options 0 and tag-mode 0 it returned 18-19 windows for Space 1.
//
// It is deliberately NOT used here, because it answers a subtly different question. Cross-checked
// against gt_spaces_of on one run, the two agreed on every window except exactly the two that
// Accessibility also could not see (an untitled Chrome window and the Claude window): gt_spaces_of
// placed both on Space 1, the inverted call omitted both. The bogus-id probe above rules out
// gt_spaces_of guessing, so the difference is one of meaning -- "which Space is this window assigned
// to" versus "which windows are currently ordered in on this Space". P2.4 was asked the former.
// Swapping to the latter would need a multi-Space machine to validate, which P2.4 did not have.

// P7.3 (optional): if window wid is on a Space other than the current one, switch to that Space so
// gt_window_raise can then resolve the window through Accessibility -- kAXWindowsAttribute lists only
// the current Space (D20/D46). Returns 1 only if the current Space actually changed (the caller
// retries the AX resolve); 0 -- private write symbols absent, the window's Space unknown or already
// current, or the switch did not take -- means fall back to P7.1's app-only activation.
//
// This is the one call in this file that WRITES, and it is a bet on symbols Apple never documented.
// It lives here rather than in shim.h/action.h for the reason in this file's opening comment: a
// switcher that will not launch because a private symbol moved is worse than one optional behaviour
// degrading to P7.1's floor. NOT VERIFIED on this one-Space machine -- see docs/tasks/P7.3.md, V6.2.
int gt_space_switch_to_window(uint32_t wid);

#endif
