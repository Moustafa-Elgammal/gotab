// Permissions onboarding: name the missing grant, send the user to the right place, and let the app
// pick up the change without a relaunch. Kept free of Objective-C so cgo can include it directly.
//
// The switcher needs Accessibility (enumerate, raise, observe, tap the hotkey) and, for thumbnails
// and other apps' titles, Screen Recording. Reading the state is gt_trusted()/gt_can_record() in
// shim.h; this file is the part that explains and recovers.
#ifndef GOTAB_DARWIN_PERMISSIONS_H
#define GOTAB_DARWIN_PERMISSIONS_H

#include <stdint.h>

#include "shim.h" // gt_status

// Shows a modal alert naming the missing grants and what each is for. On "Open System Settings" it
// opens the relevant Privacy & Security pane(s) and, for Screen Recording, calls
// CGRequestScreenCaptureAccess so the app is registered in that list. Blocks on a nested modal run
// loop until the user answers; the caller then polls gt_trusted()/gt_can_record() to recover.
//
// GT_OK if the user chose to open Settings, GT_ERR_UNAVAILABLE if they chose to quit, GT_ERR_INTERNAL
// if there is no window server to show the alert on (the caller should fall back to a printed
// explanation). Main thread only.
gt_status gt_permissions_prompt(int32_t need_accessibility, int32_t need_recording);

// Opens one Privacy & Security pane: "accessibility" or "screen-recording"; anything else opens the
// Privacy & Security root.
void gt_permissions_open_pane(const char *which);

#endif
