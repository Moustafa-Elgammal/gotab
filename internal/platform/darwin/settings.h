// A native settings window. Kept free of Objective-C so cgo can include it directly.
//
// Every control change is reported to goSettingsAssign("Key=Value") — the same form prefs.Set parses
// — so Go has one callback and no per-control plumbing. The window's close button ends the session by
// calling goSettingsClosed().
//
// Main thread only, and the caller must be running the AppKit run loop (runloop.h). Seed the controls
// from the loaded prefs with the gt_settings_set_* calls AFTER gt_settings_open.
#ifndef GOTAB_DARWIN_SETTINGS_H
#define GOTAB_DARWIN_SETTINGS_H

#include <stdint.h>

#include "shim.h" // gt_status

// Builds the window (once) and shows it, bringing the app forward. Idempotent: a second call just
// re-focuses the existing window.
gt_status gt_settings_open(void);

// Closes the window. The red button does the same; both end the session via goSettingsClosed().
void gt_settings_close(void);

// Control seeders, keyed by the pref name. Call after gt_settings_open. A key with no matching control
// is ignored.
void gt_settings_set_bool(const char *key, int32_t on);
void gt_settings_set_int(const char *key, int32_t v);
void gt_settings_set_choice(const char *key, const char *value); // NSPopUpButton, by item title (case-insensitive)
void gt_settings_set_text(const char *key, const char *value);
void gt_settings_set_hotkey(const char *display); // the recorder button's label

#endif
