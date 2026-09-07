// Appearance -> palette + vibrancy for the switcher panel. The Objective-C half of P3.4: it reads the
// *effective* appearance and watches it for a Light/Dark flip. The colour choices themselves live on
// the Go side (theme.go) and reach the panel through panel.h's frozen setters; nothing here draws.
//
// Conventions are shim.h's: gt_ prefix, gt_status where a call can fail, nothing allocated that Go
// frees. THREADING: every function here touches NSApp / NSWindow and MUST run on the thread cmd/gotab
// hands to AppKit (docs/ARCHITECTURE.md#threading). None is thread-safe; a Go caller on another
// goroutine marshals through darwin.OnMain, exactly as it does for panel.h.
#ifndef GOTAB_DARWIN_THEME_H
#define GOTAB_DARWIN_THEME_H

#include <stdint.h>

#include "shim.h" // gt_status

// 1 when the panel's effective appearance resolves to Dark, 0 for Light. Read from the panel's own
// NSVisualEffectView if it exists (that is the appearance the tiles actually render in, and it already
// carries any per-window override), otherwise from NSApp. Resolved with -bestMatchFromAppearances-
// WithNames: against {Aqua, DarkAqua} -- NOT string equality on the raw name, so the accessibility
// high-contrast appearances fold onto the right side -- and NOT AppleInterfaceStyle from NSUserDefaults,
// which misses the per-app override and the automatic Light/Dark schedule. Defaults to 1 (Dark) if no
// appearance can be read at all, matching panel.h's built-in default.
int32_t gt_theme_is_dark(void);

// The NSVisualEffectMaterial value the panel's backing view should use, resolved from the AppKit enum
// by name so no magic number lives on the Go side. NSVisualEffectMaterialHUDWindow: the system's
// material for a floating, non-document overlay, which is exactly what a switcher is. Passed straight
// through to gt_panel_set_material; panel.h maps GT_MATERIAL_NONE to the palette.panel_bg fallback.
int32_t gt_theme_material(void);

// Starts one KVO observation of NSApp.effectiveAppearance. On every change it calls goThemeChanged()
// and does nothing else -- no reading of the new value, no restyle -- the same hand-off-and-return
// rule the AX observers follow (docs/ARCHITECTURE.md#the-cgo-rule). Idempotent: a second start with no
// stop between is a no-op that returns GT_OK. GT_ERR_UNAVAILABLE only if NSApp cannot be created.
gt_status gt_theme_watch_start(void);

// Removes the KVO observation gt_theme_watch_start added and releases the observer object. Safe when
// nothing was started and safe to call twice: the observer is registered exactly once and cleared
// here, so no KVO registration and no observer instance leaks across start/stop cycles.
void gt_theme_watch_stop(void);

#endif
