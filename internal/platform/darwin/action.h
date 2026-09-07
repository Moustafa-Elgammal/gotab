// Window actions: raise, minimize, unminimize, close. The half of the platform layer that writes.
//
// Conventions are shim.h's and are not restated here: gt_status in, out-parameters for bulk data,
// nothing allocated that Go must free. Two things are specific to this file:
//
//   - These are NOT batched, and that is not a violation of docs/ARCHITECTURE.md#the-cgo-rule. The
//     rule exists because a per-item crossing multiplies 31 ns by N. An action is one per user
//     gesture, and each one is Mach IPC into another process measured in hundreds of microseconds at
//     best -- the crossing is noise beside the work. Batching here would buy nothing and would force
//     a partial-failure model on a caller that acts on one window at a time.
//   - Every one of them can fail because the window stopped existing between the enumeration that
//     listed it and the gesture that acted on it. That race is the normal case, not an edge case: a
//     switcher shows a list that is already out of date the moment it is drawn.
#ifndef GOTAB_DARWIN_ACTION_H
#define GOTAB_DARWIN_ACTION_H

#include <stdint.h>

#include "shim.h"

enum {
    // The window id is not one any live window answers to any more -- it was closed, or its
    // application exited, between enumeration and this call.
    //
    // This extends shim.h's enum rather than living in it because shim.h is frozen (P2.1) and this
    // is the only status that needed adding since. The number continues that enum's sequence and
    // action.go is where it becomes a Go error, the same way shim.go is for the other five. If a
    // later task adds a sixth status of its own, these two must be reconciled -- the closed enum is
    // still the design, and this is a documented extension of it, not a second numbering space.
    GT_ERR_NO_WINDOW = 6
};

// Brings the window to the front of its application AND makes that application frontmost. Both are
// required and neither is sufficient: raising alone reorders a window inside an app nobody is looking
// at, and activating alone brings forward whichever window that app already considered its front one.
//
// A minimized window is unminimized first. A switcher that selects a minimized window and does
// nothing visible is broken, and the Dock's restore animation is the user-visible confirmation that
// the switch happened. A hidden application (Cmd-H) is unhidden for the same reason.
//
// Safe from any thread. AXUIElement calls are thread-safe by design, and NSRunningApplication is
// documented as such -- gt_ax_window_list already relies on the latter.
gt_status gt_window_raise(uint32_t wid);

// Minimizes to the Dock, as the yellow button does. Returns GT_OK for a window that was already
// minimized: this is a request for a state, not a toggle.
gt_status gt_window_minimize(uint32_t wid);

// Restores from the Dock without activating the application. Raise does this on its own; this exists
// for the caller who wants the restore without the focus change.
gt_status gt_window_unminimize(uint32_t wid);

// Presses the window's close button. This is the ordinary close a user performs, so an application
// may put up a save sheet and keep the window: GT_OK means the button was pressed, never that the
// window went away. A window with no close button -- a sheet, some palettes -- reports
// GT_ERR_UNAVAILABLE, because there is no such gesture for it rather than because anything failed.
gt_status gt_window_close(uint32_t wid);

#endif
