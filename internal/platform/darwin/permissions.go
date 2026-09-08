package darwin

/*
#include <stdlib.h>
#include "permissions.h"
*/
import "C"

import "unsafe"

// PromptPermissions shows a modal alert naming the missing grants and, on "Open System Settings",
// opens the relevant Privacy & Security pane(s) — and for Screen Recording registers the app in that
// list. It blocks until the user answers. Main thread only — and safe to run from an OnMain closure
// once RunLoop has started, which is how the first-run prompt reliably surfaces for an Accessory app
// (D55).
//
// canDefer picks the dismiss button: false gives "Quit" (the caller exits on it), true gives "Not
// Now" (the caller keeps running). shown is false when there is no window server to show the alert on
// (headless / ssh); the caller then falls back to a printed explanation. proceed is true when the
// user chose to open Settings, false otherwise (and always false when shown is false).
func PromptPermissions(needAccessibility, needScreenRecording, canDefer bool) (shown, proceed bool) {
	b := func(v bool) C.int32_t {
		if v {
			return 1
		}
		return 0
	}
	switch C.gt_permissions_prompt(b(needAccessibility), b(needScreenRecording), b(canDefer)) {
	case C.GT_OK:
		return true, true
	case C.GT_ERR_UNAVAILABLE:
		return true, false
	default: // GT_ERR_INTERNAL — no window server
		return false, false
	}
}

// OpenPrivacyPane opens a Privacy & Security pane: "accessibility", "screen-recording", or "" for the
// Privacy & Security root.
func OpenPrivacyPane(which string) {
	c := C.CString(which)
	defer C.free(unsafe.Pointer(c))
	C.gt_permissions_open_pane(c)
}
