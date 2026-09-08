package darwin

/*
#include "action.h"
*/
import "C"

import (
	"errors"
	"fmt"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
)

// ErrNoWindow means the id does not name a live window any more. It is the expected outcome of the
// race every one of these functions is written around: a switcher acts on a list that was already out
// of date when it was drawn, and the window the user selected may have closed while they were looking
// at it. Callers drop the window from the model and move on — this is not a failure to report.
var ErrNoWindow = errors.New("the window no longer exists")

// actionError extends shim.go's statusError with the one status this file adds. Everything else routes
// through the same switch as the rest of the package, so the sentinel set stays in one place plus this
// one documented addition (see GT_ERR_NO_WINDOW in action.h).
func actionError(op string, s C.gt_status) error {
	if s == C.GT_ERR_NO_WINDOW {
		return fmt.Errorf("darwin: %s: %w", op, ErrNoWindow)
	}
	return statusError(op, s)
}

// Raise brings the window to the front of its application and makes that application frontmost. Both
// halves are required: a raise inside an app nobody is looking at is invisible, and activating an app
// surfaces whichever window it already considered its front one, which is rarely the one the user
// picked.
//
// A minimized window is unminimized first, and a hidden application (Cmd-H) is unhidden. Selecting a
// minimized window and seeing nothing happen is the failure mode this exists to prevent.
//
// If the id cannot be resolved to a window but its owning application is still running — a window the
// enumeration join surfaced because Accessibility could not see it, typically on another Space — the
// application is brought forward and Raise returns nil (P7.1, D46): the specific window is not
// reordered, but the user reaches what they selected. ErrNoWindow is returned only when the owning
// process is gone too, and the caller should then drop the window from the model.
//
// Returns ErrNotTrusted without the Accessibility grant, and ErrTimeout if the owning application did
// not answer within the messaging timeout — an app that is beachballing or paused in a debugger.
// Errors are sentinels; match with errors.Is.
//
// Blocking: this is Mach IPC into another process and can take up to the messaging timeout. Call it
// from the event-loop goroutine, never from a callback (docs/ARCHITECTURE.md#the-cgo-rule).
func Raise(id core.WindowID) error {
	return actionError("raise", C.gt_window_raise(C.uint32_t(id)))
}

// Minimize sends the window to the Dock, as its yellow button does. A window that is already
// minimized is a success and not a toggle: this asks for a state.
func Minimize(id core.WindowID) error {
	return actionError("minimize", C.gt_window_minimize(C.uint32_t(id)))
}

// Unminimize restores the window from the Dock without changing which application is frontmost. Raise
// already does this when it needs to; this is for the caller that wants the restore on its own.
func Unminimize(id core.WindowID) error {
	return actionError("unminimize", C.gt_window_unminimize(C.uint32_t(id)))
}

// Close presses the window's close button — the same gesture as clicking the red button, so an
// application may put up a save sheet and keep the window open. A nil error means the button was
// pressed, never that the window is gone. A window with no close button reports ErrUnavailable.
func Close(id core.WindowID) error {
	return actionError("close", C.gt_window_close(C.uint32_t(id)))
}
