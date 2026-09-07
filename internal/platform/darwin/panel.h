// The C surface of the switcher panel: one borderless non-activating NSPanel, one content view that
// draws every tile, and the accessors P3.3/P3.4 reach it through. Kept free of Objective-C so cgo can
// include it directly.
//
// FROZEN by the Phase 3 carve, exactly as internal/core/api.go was frozen for Phase 1. P3.1 writes
// panel.m against these signatures; P3.3 and P3.4 only *call* them from their own files. An agent that
// needs to change this header stops and escalates -- that is the whole mechanism that lets four agents
// share one panel without sharing one file.
//
// Conventions are shim.h's: gt_ prefix, gt_status where a call can fail, nothing allocated that Go
// frees. THREADING: every function here touches AppKit and must run on the thread cmd/gotab locked in
// init and then hands to the run loop (docs/ARCHITECTURE.md#threading). None is thread-safe; a Go
// caller on another goroutine marshals through darwin.OnMain.
#ifndef GOTAB_DARWIN_PANEL_H
#define GOTAB_DARWIN_PANEL_H

#include <stdint.h>

#include "shim.h" // gt_status, gt_image_ref

// ---------------------------------------------------------------------------
// Data passed in on every frame. Go computes all of it; C only draws.
// ---------------------------------------------------------------------------

// One tile, in content-view coordinates with the origin at the TOP-left (the content view is flipped,
// so Go and C agree on "y grows downward" and P3.2's layout math needs no flip).
//
// `image` is a BORROWED gt_image_ref: the panel draws it and never releases it. The caller (P3.3) must
// keep it alive for as long as it is the current image of a visible tile, and swaps it out with
// gt_panel_tile_layer rather than by handing a freed handle to the next gt_panel_update.
typedef struct gt_tile {
    int32_t x, y, w, h;    // frame in the content view, points
    int32_t selected;      // 1 for the single tile that gets the selection treatment
    gt_image_ref image;    // thumbnail, or NULL for "not captured yet" -> draw the placeholder
    uint16_t title_len;    // bytes in title[], not counting any terminator
    uint16_t subtitle_len; // bytes in subtitle[]
    char title[256];       // the window title (GT_TITLE_MAX in shim.h)
    char subtitle[128];    // the application name (GT_APPNAME_MAX)
} gt_tile;

// Straight (non-premultiplied) RGBA, each channel 0..1. P3.4 fills a palette from the effective
// appearance; P3.1 draws chrome and text with it. One place colours are chosen, one place they are used.
typedef struct gt_rgba {
    double r, g, b, a;
} gt_rgba;

typedef struct gt_palette {
    gt_rgba panel_bg;    // the rounded slab behind the tiles (used when material is GT_MATERIAL_NONE)
    gt_rgba tile_bg;     // an unselected tile
    gt_rgba tile_sel_bg; // the selected tile
    gt_rgba label;       // title text
    gt_rgba label_dim;   // subtitle text
    int32_t is_dark;     // 1 if the effective appearance is dark; drives the placeholder and shadow
} gt_palette;

// Vibrancy material for the panel's backing NSVisualEffectView. The integer is passed straight to
// -[NSVisualEffectView setMaterial:]; P3.4 owns the mapping from a Go constant and may pass any value
// AppKit accepts. GT_MATERIAL_NONE turns vibrancy off and falls back to palette.panel_bg.
enum { GT_MATERIAL_NONE = -1 };

// ---------------------------------------------------------------------------
// Lifecycle -- P3.1 owns every body below
// ---------------------------------------------------------------------------

// Builds NSApp (Accessory activation policy: no Dock tile, never frontmost on its own), the panel, its
// backing NSVisualEffectView and the flipped content view. Idempotent. Shows nothing and starts no run
// loop -- cmd/gotab owns the loop, and wiring it is the integrator's, not P3.1's.
gt_status gt_panel_create(void);

// Shows the panel populated with exactly n tiles, sized panel_w x panel_h points, centred on the
// screen that currently holds the mouse (that is where the user is looking). Uses orderFrontRegardless
// and the non-activating style mask, so the application being switched away from stays active -- P0.1
// verified the frontmost pid never changes across a summon (D13). Replaces whatever was shown; safe to
// call repeatedly while summoned. n == 0 is valid and draws an empty slab.
gt_status gt_panel_show(const gt_tile *tiles, int32_t n, int32_t panel_w, int32_t panel_h);

// Re-populates the tiles and redraws WITHOUT re-resolving the screen or re-ordering the window: the
// cycle path, where only the selection moved or a thumbnail just arrived. No-op when not visible.
// P0.1/D13: a re-order call costs ~1 ms but the frame does not commit for ~14 ms cold, so the summon
// path calls gt_panel_show once and the keystroke path calls this.
gt_status gt_panel_update(const gt_tile *tiles, int32_t n);

// Orders the panel out. Safe when already hidden. The panel and its views are kept for reuse.
void gt_panel_hide(void);

// 1 while the panel is on screen.
int32_t gt_panel_visible(void);

// ---------------------------------------------------------------------------
// Accessors -- P3.1 owns the bodies; P3.3 and P3.4 are the only callers
// ---------------------------------------------------------------------------

// The CALayer backing tile i (0-based, matching the last gt_panel_show / gt_panel_update). NULL if i
// is out of range or the panel does not exist. P3.3 sets its `contents`; nothing else touches tile
// layers. Valid until the next call that changes the tile count.
void *gt_panel_tile_layer(int32_t i);

// The panel's NSWindow, its backing NSVisualEffectView and its flipped content view, as void* an ObjC
// file casts back. For P3.4 only -- material, appearance observation and layer background colours live
// in theme.m. Any of them is NULL before gt_panel_create.
void *gt_panel_window(void);
void *gt_panel_effect_view(void);
void *gt_panel_content_view(void);

// Installs the palette P3.1 draws chrome and text with. Copied by value; call again on a dark-mode
// switch. Before the first call P3.1 uses a built-in dark default, so the panel is never unstyled.
void gt_panel_set_palette(gt_palette p);

// Sets the NSVisualEffectView material (see the enum above). GT_MATERIAL_NONE disables vibrancy.
void gt_panel_set_material(int32_t material);

// The visible frame (points) and backing scale of the display the next gt_panel_show will use — the
// one under the mouse, resolved by the same helper gt_panel_show uses so the two never disagree.
// core.Layout needs these to scale its margin and to size thumbnails; feeding it a guessed scale was
// P4.1's V6.8 assumption. Any out-param may be NULL; values are 0 (scale 1) if there is no screen.
void gt_active_screen(int32_t *width_pt, int32_t *height_pt, int32_t *scale);

#endif
