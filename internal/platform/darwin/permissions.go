package darwin

/*
#include <stdlib.h>
#include "permissions.h"
*/
import "C"

import "unsafe"

// PromptPermissions shows a modal alert naming the missing grants and, on "Open System Settings",
// opens the relevant Privacy & Security pane(s) — and for Screen Recording registers the app in that
// list. It blocks until the user answers. Main thread only.
//
// shown is false when there is no window server to show the alert on (headless / ssh); the caller
// then falls back to a printed explanation. proceed is true when the user chose to open Settings,
// false when they chose to quit (and always false when shown is false).
func PromptPermissions(needAccessibility, needScreenRecording bool) (shown, proceed bool) {
	b := func(v bool) C.int32_t {
		if v {
			return 1
		}
		return 0
	}
	switch C.gt_permissions_prompt(b(needAccessibility), b(needScreenRecording)) {
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
