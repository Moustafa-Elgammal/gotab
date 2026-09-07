// SKELETON for P3.1. The panel and its views are created for real here -- that part is settled prior
// art from spike/panel (P0.1/D13) and freezing panel.h depends on it existing. Everything that draws
// a tile, positions the window on the mouse's screen, distinguishes show from update, or wires the
// palette/material is a stub marked `// P3.1:` for the owning agent to replace.
//
// Compiled WITHOUT ARC, like every other .m in this package: retain/release is manual.
//
// P3.1's contract is docs/tasks/P3.1.md. Do not touch panel.h (frozen), and do not edit thumbnail.*
// or theme.* -- those are P3.3 and P3.4.
#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include "panel.h"

// ---------------------------------------------------------------------------
// State. One panel per process; all of this is main-thread-only (panel.h THREADING).
// ---------------------------------------------------------------------------

static NSPanel *g_panel = nil;
static NSVisualEffectView *g_effect = nil; // panel backing; P3.4 sets its material
static NSView *g_content = nil;            // flipped; draws the tiles; hosts the tile layers
static NSMutableArray<CALayer *> *g_tile_layers = nil;
static gt_palette g_palette;
static int g_have_palette = 0;

// GT_MATERIAL_NONE until P3.4 sets one. Stored so a palette-only redraw keeps it.
static int32_t g_material = GT_MATERIAL_NONE;

// ---------------------------------------------------------------------------
// The one view that draws every tile. AltTab has 53 NSView subclasses and pays a C->Go callback per
// tile; this draws them all in drawRect: and keeps thumbnails in dumb CALayers that never call back.
// ---------------------------------------------------------------------------

@interface GTTileView : NSView
@end

@implementation GTTileView
- (BOOL)isFlipped {
    return YES;
}
- (void)drawRect:(NSRect)dirty {
    (void)dirty;
    // P3.1: draw the rounded panel slab (g_palette.panel_bg when g_material == GT_MATERIAL_NONE,
    // otherwise let the NSVisualEffectView show through), then each tile's background, selection
    // treatment, title and subtitle. Placeholder art when a tile's layer has no contents yet (D12:
    // tiles are shown before their thumbnails exist). The tile frames arrive via gt_panel_show /
    // gt_panel_update; store them alongside g_tile_layers.
}
@end

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

gt_status gt_panel_create(void) {
    if (g_panel) return GT_OK;

    NSApplication *app = [NSApplication sharedApplication];
    // Accessory: no Dock tile, no menu bar, and the process never becomes frontmost on its own --
    // which is exactly what a switcher must not do to the app behind it.
    [app setActivationPolicy:NSApplicationActivationPolicyAccessory];

    NSScreen *screen = [NSScreen mainScreen];
    if (!screen) return GT_ERR_UNAVAILABLE;

    // A provisional frame; gt_panel_show sets the real size and origin every summon.
    NSRect frame = NSMakeRect(0, 0, 800, 160);

    g_panel = [[NSPanel alloc]
        initWithContentRect:frame
                  styleMask:(NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel)
                    backing:NSBackingStoreBuffered
                      defer:NO];
    [g_panel setOpaque:NO];
    [g_panel setBackgroundColor:[NSColor clearColor]];
    [g_panel setHasShadow:YES];
    [g_panel setLevel:NSPopUpMenuWindowLevel];
    [g_panel setHidesOnDeactivate:NO];
    [g_panel setBecomesKeyOnlyIfNeeded:YES];
    // canJoinAllSpaces: follow the user rather than living on one Space. fullScreenAuxiliary: appear
    // OVER a full-screen app, not behind it. stationary: do not slide in Exposé. (D13; the human half
    // of this is V6.2.)
    [g_panel setCollectionBehavior:(NSWindowCollectionBehaviorCanJoinAllSpaces |
                                    NSWindowCollectionBehaviorFullScreenAuxiliary |
                                    NSWindowCollectionBehaviorStationary)];

    g_effect = [[NSVisualEffectView alloc] initWithFrame:frame];
    [g_effect setState:NSVisualEffectStateActive];
    [g_effect setBlendingMode:NSVisualEffectBlendingModeBehindWindow];
    [g_effect setWantsLayer:YES];
    [[g_effect layer] setCornerRadius:12];
    [[g_effect layer] setMasksToBounds:YES];
    [g_panel setContentView:g_effect];

    g_content = [[GTTileView alloc] initWithFrame:frame];
    [g_content setWantsLayer:YES];
    [g_content setAutoresizingMask:(NSViewWidthSizable | NSViewHeightSizable)];
    [g_effect addSubview:g_content];

    g_tile_layers = [[NSMutableArray alloc] init];
    return GT_OK;
}

// P3.1: fold the shared body of show/update out of here; keep the screen-resolve and orderFront in
// show only.
static gt_status panel_populate(const gt_tile *tiles, int32_t n) {
    if (!g_panel) return GT_ERR_INTERNAL;

    // P3.1: rebuild g_tile_layers to length n, position each at tiles[i].{x,y,w,h}, store the frames
    // for drawRect:, and mark the view for display. For now just size the layer array so
    // gt_panel_tile_layer does not read past it.
    while ((int32_t)g_tile_layers.count > n && g_tile_layers.count > 0) {
        [[g_tile_layers lastObject] removeFromSuperlayer];
        [g_tile_layers removeLastObject];
    }
    while ((int32_t)g_tile_layers.count < n) {
        CALayer *l = [CALayer layer];
        l.contentsGravity = kCAGravityResizeAspect;
        [[g_content layer] addSublayer:l];
        [g_tile_layers addObject:l];
    }
    for (int32_t i = 0; i < n; i++) {
        CALayer *l = g_tile_layers[i];
        l.frame = CGRectMake(tiles[i].x, tiles[i].y, tiles[i].w, tiles[i].h);
    }
    [g_content setNeedsDisplay:YES];
    return GT_OK;
}

gt_status gt_panel_show(const gt_tile *tiles, int32_t n, int32_t panel_w, int32_t panel_h) {
    if (!g_panel) return GT_ERR_INTERNAL;

    // P3.1: resolve the screen under the mouse and centre there. Skeleton uses the main screen.
    NSScreen *screen = [NSScreen mainScreen];
    NSRect vf = [screen visibleFrame];
    NSRect frame = NSMakeRect(NSMidX(vf) - panel_w / 2.0, NSMidY(vf) - panel_h / 2.0, panel_w, panel_h);
    [g_panel setFrame:frame display:NO];

    gt_status st = panel_populate(tiles, n);
    if (st != GT_OK) return st;

    [g_panel orderFrontRegardless];
    return GT_OK;
}

gt_status gt_panel_update(const gt_tile *tiles, int32_t n) {
    if (![g_panel isVisible]) return GT_OK;
    return panel_populate(tiles, n);
}

void gt_panel_hide(void) {
    [g_panel orderOut:nil];
}

int32_t gt_panel_visible(void) {
    return (g_panel && [g_panel isVisible]) ? 1 : 0;
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

void *gt_panel_tile_layer(int32_t i) {
    if (!g_tile_layers || i < 0 || i >= (int32_t)g_tile_layers.count) return NULL;
    return (void *)g_tile_layers[i];
}

void *gt_panel_window(void) {
    return (void *)g_panel;
}
void *gt_panel_effect_view(void) {
    return (void *)g_effect;
}
void *gt_panel_content_view(void) {
    return (void *)g_content;
}

void gt_panel_set_palette(gt_palette p) {
    g_palette = p;
    g_have_palette = 1;
    [g_content setNeedsDisplay:YES];
}

void gt_panel_set_material(int32_t material) {
    g_material = material;
    // P3.1: when material == GT_MATERIAL_NONE, hide g_effect's vibrancy and paint palette.panel_bg;
    // otherwise [g_effect setMaterial:material].
    if (material != GT_MATERIAL_NONE && g_effect) {
        [g_effect setMaterial:(NSVisualEffectMaterial)material];
    }
}
