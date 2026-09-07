package darwin

/*
#include "theme.h"
#include "panel.h"
*/
import "C"

import (
	"errors"
	"sync"
	"sync/atomic"
)

// The cgo flags for this package are declared once, in shim.go; they apply to every file here.

// This file decides the panel's colours and vibrancy from the system's effective appearance and keeps
// them in step when the user flips Light/Dark. The split is deliberate (docs/tasks/P3.4.md): reading
// the appearance and observing it for a change are Objective-C (theme.m); the colour *choices* are the
// two functions below. Everything reaches the panel through panel.h's frozen setters --
// gt_panel_set_palette and gt_panel_set_material -- and this file adds nothing to panel.*.

// ---------------------------------------------------------------------------
// The colour choices
// ---------------------------------------------------------------------------

// rgba builds panel.h's gt_rgba: straight (non-premultiplied) RGBA, each channel 0..1.
func rgba(r, g, b, a float64) C.gt_rgba {
	return C.gt_rgba{r: C.double(r), g: C.double(g), b: C.double(b), a: C.double(a)}
}

// darkPalette and lightPalette are tuned for a floating HUD slab that sits on vibrancy (see
// gt_theme_material -> NSVisualEffectMaterialHUDWindow): the blur behind the panel supplies most of
// the ground, so panel_bg is only the GT_MATERIAL_NONE fallback and the tile backgrounds are light
// washes over the blur. Contrast is carried by the selected tile and by the label, which stay near
// fully opaque in both appearances. is_dark drives panel.h's placeholder art and shadow.
func darkPalette() C.gt_palette {
	return C.gt_palette{
		panel_bg:    rgba(0.11, 0.11, 0.12, 0.92), // fallback only; HUDWindow shows through otherwise
		tile_bg:     rgba(1, 1, 1, 0.06),
		tile_sel_bg: rgba(1, 1, 1, 0.20),
		label:       rgba(1, 1, 1, 0.98),
		label_dim:   rgba(1, 1, 1, 0.55),
		is_dark:     1,
	}
}

func lightPalette() C.gt_palette {
	return C.gt_palette{
		panel_bg:    rgba(0.96, 0.96, 0.97, 0.92), // fallback only
		tile_bg:     rgba(0, 0, 0, 0.04),
		tile_sel_bg: rgba(0, 0, 0, 0.14),
		label:       rgba(0, 0, 0, 0.92),
		label_dim:   rgba(0, 0, 0, 0.50),
		is_dark:     0,
	}
}

// ---------------------------------------------------------------------------
// Applying
// ---------------------------------------------------------------------------

// ApplyTheme reads the current effective appearance and pushes a matching palette and vibrancy
// material to the panel through P3.1's frozen setters.
//
// Main thread only -- it reads NSApp state and calls the panel setters, both of which assert off the
// main thread (docs/ARCHITECTURE.md#threading). A caller on another goroutine wraps it in OnMain, the
// same as every panel.go call.
//
// Safe before CreatePanel and safe to call repeatedly; past the first call it allocates nothing and
// makes no IPC. It must be called once right after CreatePanel and before the first ShowPanel so the
// panel is styled before it is ever seen -- see the note in docs/tasks/P3.4.md on why a pre-Create
// call cannot be replayed from here. Without it the panel falls back to panel.h's built-in dark
// default: styled, not unstyled, but not necessarily matching Light.
func ApplyTheme() error {
	var pal C.gt_palette
	if C.gt_theme_is_dark() != 0 {
		pal = darkPalette()
	} else {
		pal = lightPalette()
	}
	C.gt_panel_set_palette(pal)
	C.gt_panel_set_material(C.gt_theme_material())
	return nil
}

// ---------------------------------------------------------------------------
// Watching
// ---------------------------------------------------------------------------

// themeChangeCB is the callback the KVO observation ends in. An atomic pointer, read from an
// AppKit-owned thread and written by whichever goroutine calls WatchAppearance -- the same shape and
// the same reason as observe.go's observeCallback: a lock on the notification path would let a slow
// StopWatchingAppearance block the callback, which docs/ARCHITECTURE.md#the-cgo-rule forbids.
var themeChangeCB atomic.Pointer[func()]

// themeWatchMu serialises WatchAppearance against StopWatchingAppearance so the C side never sees a
// start and a stop overlap. Deliberately NOT on the callback path.
var themeWatchMu sync.Mutex

// ErrNilThemeCallback is returned by WatchAppearance when onChange is nil: a watch whose
// notifications go nowhere is the install-and-silently-discard failure this package exists to avoid.
var ErrNilThemeCallback = errors.New("darwin: watch appearance: onChange must not be nil")

// WatchAppearance calls onChange whenever the effective appearance flips (Light <-> Dark, including
// via a per-app override or the automatic schedule, not only the System Settings switch).
//
// onChange runs on the main thread -- KVO delivers -effectiveAppearance changes there -- and MUST
// return immediately. It is expected to do nothing but schedule an ApplyTheme on the event loop, the
// same rule as the AX observer callbacks (docs/ARCHITECTURE.md#the-cgo-rule). ApplyTheme itself is
// cheap and allocation-free, so calling it directly from onChange is also fine; a Light/Dark flip is
// a rare user action, not a burst.
//
// onChange is a hint, not a diff: it may fire when nothing the panel cares about changed, and it
// coalesces nothing. A second call while watching replaces onChange and returns nil without
// re-registering. Safe from any goroutine for the bookkeeping; the underlying KVO register/unregister
// is main-thread work, so in practice this is called from the same place the panel is created.
func WatchAppearance(onChange func()) error {
	if onChange == nil {
		return ErrNilThemeCallback
	}
	themeWatchMu.Lock()
	defer themeWatchMu.Unlock()
	// Stored before the C side registers, never after: mirrors observe.go, where a notification can
	// arrive before the pointer is published and would otherwise be dropped.
	themeChangeCB.Store(&onChange)
	if err := statusError("watch appearance", C.gt_theme_watch_start()); err != nil {
		themeChangeCB.Store(nil)
		return err
	}
	return nil
}

// StopWatchingAppearance removes the KVO registration. After it returns onChange is not called again:
// KVO notifications are synchronous with the property change and on the main thread, so once
// removeObserver: has run there is no in-flight callback to outrace. Safe when nothing was started
// and safe to call twice.
func StopWatchingAppearance() {
	themeWatchMu.Lock()
	defer themeWatchMu.Unlock()
	C.gt_theme_watch_stop()
	themeChangeCB.Store(nil)
}

//export goThemeChanged
func goThemeChanged() {
	// The whole Go side of the appearance-change path: the ~39 ns C->Go crossing
	// (docs/ARCHITECTURE.md#the-cgo-rule) and nothing else. No allocation, no decision -- ApplyTheme,
	// which onChange ultimately triggers, re-reads the appearance from scratch.
	if fn := themeChangeCB.Load(); fn != nil {
		(*fn)()
	}
}
