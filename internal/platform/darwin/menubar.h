// The menu-bar status item: the only way to reach GoTab without a terminal. GoTab is LSUIElement — no
// Dock tile, no app menu — so a background switcher a user installed from Finder has, until this, no
// visible surface at all (P8.1). One NSStatusItem with two items:
//
//   Settings…    -> goMenubarSettings()   (Go spawns `gotab -settings`, the standalone entry point)
//   Quit GoTab   -> goMenubarQuit()       (Go tears the switcher down cleanly)
//
// Kept free of Objective-C so cgo can include it directly. THREADING: main thread only, and the
// caller must already be running the AppKit run loop (runloop.h) — same contract as panel.h /
// settings.h. A status item works under Accessory activation policy, so this does not change it.
#ifndef GOTAB_DARWIN_MENUBAR_H
#define GOTAB_DARWIN_MENUBAR_H

#include "shim.h" // gt_status

// Installs the status item and its menu. `title` is a disabled header row (e.g. "GoTab 0.3.2"); pass
// NULL or "" to omit it. Idempotent: a second call is a no-op. The button shows a template symbol,
// falling back to a short text title where the symbol is unavailable.
gt_status gt_menubar_install(const char *title);

// Removes the status item. Safe when nothing was installed, and safe twice.
void gt_menubar_remove(void);

// Defined in Go (menubar.go). Both run on the main thread, from the menu action.
extern void goMenubarSettings(void);
extern void goMenubarQuit(void);

#endif
