// The switcher's global hotkey: a session-level CGEventTap that recognises the Option+Tab gesture,
// swallows the switcher's own chord, and lets every other key through. Kept free of Objective-C so
// cgo can include it directly.
//
// Prior art is spike/hotkey (P0.2/D15): the session tap at the head of the queue is the only position
// from which an event can be seen before the focused app and then swallowed, and the
// kCGEventTapDisabledByTimeout re-enable is the documented failure mode, not defensive noise. The tap
// runs on its own thread (like observe.m's AX observers), so a keystroke is never queued behind a
// panel draw on the main run loop.
#ifndef GOTAB_DARWIN_HOTKEY_H
#define GOTAB_DARWIN_HOTKEY_H

#include <stdint.h>

#include "shim.h" // gt_status, GT_ERR_NOT_TRUSTED

// One step of the gesture, as a single int across the boundary. cmd/gotab maps these onto
// internal/app events; this layer must not know internal/app exists. The tap thread decides
// summon-vs-cycle itself (it tracks whether a Tab has been seen since Option went down), so the Go
// side is a stateless switch.
enum {
    GT_HK_SUMMON_FWD = 0, // first Option+Tab of a hold: show the panel, select the next window
    GT_HK_SUMMON_BWD = 1, // first Option+Shift+Tab
    GT_HK_CYCLE_FWD  = 2, // Option+Tab again while the panel is up
    GT_HK_CYCLE_BWD  = 3,
    GT_HK_ACTIVATE   = 4, // Option released after at least one Tab: commit the selection
    GT_HK_DISMISS    = 5   // Escape while the panel is up: close it and change nothing
};

// Installs the tap on a dedicated thread and calls goHotkeyGesture(kind) for each step, on that
// thread, which MUST return immediately (docs/ARCHITECTURE.md#the-cgo-rule). Option+Tab and its
// key-up are swallowed so the focused application never sees the switcher's chord; a modifier change
// is never swallowed, and every other key passes through untouched.
//
// Requires the Accessibility grant. Without it CGEventTapCreate succeeds and then never fires — which
// looks exactly like a broken hotkey — so this returns GT_ERR_NOT_TRUSTED instead, the same way
// gt_ax_window_list does. Idempotent: a second call while running is GT_OK. Go serialises start/stop.
gt_status gt_hotkey_start(void);

// Removes the tap and its run-loop source and does not return until the tap thread has torn down, so
// goHotkeyGesture cannot be called after it returns. Safe when not running, and safe twice.
void gt_hotkey_stop(void);

#endif
