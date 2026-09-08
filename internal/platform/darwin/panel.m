// The switcher panel and its single-view renderer.
//
// One borderless non-activating NSPanel, one flipped GTTileView that draws EVERY tile in one
// drawRect: -- background slab, per-tile background, the selection treatment, title + subtitle, and a
// placeholder for any tile whose thumbnail does not exist yet (D12: tiles are on screen before the
// captures finish). Thumbnails ride in dumb CALayer sublayers that never call back into Go; P3.3 sets
// their `contents` through gt_panel_tile_layer and nothing here touches that.
//
// Prior art is spike/panel/panel.m (P0.1/D13): the style mask, the collectionBehavior triple and the
// flipped one-view draw are copied from it. Compiled WITHOUT ARC like every other .m here, so
// retain/release is manual.
//
// Contract: docs/tasks/P3.1.md. panel.h is frozen; thumbnail.* is P3.3 and theme.* is P3.4.
#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include "panel.h"
#include "timing.h" // V6.3 scaffold: gt_timing_arm / goTimingFrameCommitted. Inert unless armed.

// ---------------------------------------------------------------------------
// State. One panel per process; all of this is main-thread-only (panel.h THREADING).
// ---------------------------------------------------------------------------

static NSPanel *g_panel = nil;
static NSVisualEffectView *g_effect = nil; // panel backing; P3.4 sets its material
static NSView *g_content = nil;            // flipped; draws the tiles; hosts the tile layers
static NSMutableArray<CALayer *> *g_tile_layers = nil;
static gt_palette g_palette;
static int g_have_palette = 0;

// VoiceOver (P5.2). GTTileView draws every tile itself, so there is no per-tile NSView for AppKit to
// hand a screen reader: the content view is an accessibility container and each tile is a synthetic
// element in g_a11y_children, kept in step with g_tiles by a11y_sync(). g_a11y_last_* debounce the
// spoken announcement so a cycle speaks the new selection once and a redraw that changed nothing is
// silent; gt_panel_hide resets them so the next summon always speaks its initial selection.
static NSMutableArray *g_a11y_children = nil;
static int32_t g_a11y_last_selected = -1;
static int32_t g_a11y_last_n = -1;

// GT_MATERIAL_NONE until P3.4 sets one. Stored so a palette-only redraw keeps it.
static int32_t g_material = GT_MATERIAL_NONE;

// V6.3 scaffold. One-shot: gt_timing_arm() sets it, the next gt_panel_show consumes it. 0 in the
// shipped switcher, which never arms it.
static atomic_int g_timing_armed = 0;
void gt_timing_arm(void) { atomic_store(&g_timing_armed, 1); }

// A by-value copy of the tiles from the last gt_panel_show / gt_panel_update. The pointer Go hands in
// is alive only for the duration of that call, but drawRect: runs on a later run-loop turn, so it
// reads from here. `image` handles are borrowed exactly as gt_tile documents: copied, drawn, never
// released.
static gt_tile *g_tiles = NULL;
static int32_t g_ntiles = 0;

// How a tile's frame is split between its thumbnail and its text. P3.2 (layout) computes the outer
// frame; the inside is P3.1's until that lands. The tile CALayer is positioned over the thumbnail
// sub-rect ONLY -- not the whole tile -- so an opaque CGImage dropped on it by P3.3 does not cover
// the title strip. gt_panel_tile_layer's callers should assume that geometry.
static const CGFloat kSlabRadius = 12.0;  // matches g_effect.layer.cornerRadius set in gt_panel_create
static const CGFloat kTilePad = 8.0;      // tile edge -> its contents
static const CGFloat kTileRadius = 8.0;
static const CGFloat kLabelStrip = 34.0;  // bottom band of a tile reserved for title + subtitle
static const CGFloat kLabelGap = 4.0;     // thumbnail -> label strip

// ---------------------------------------------------------------------------
// Palette. One built-in dark default so the panel is styled before P3.4's first gt_panel_set_palette.
// ---------------------------------------------------------------------------

static gt_palette default_dark_palette(void) {
    gt_palette p;
    p.panel_bg    = (gt_rgba){0.12, 0.12, 0.13, 0.96};
    p.tile_bg     = (gt_rgba){1.00, 1.00, 1.00, 0.06};
    p.tile_sel_bg = (gt_rgba){0.26, 0.46, 0.86, 0.92};
    p.label       = (gt_rgba){0.97, 0.97, 0.98, 1.00};
    p.label_dim   = (gt_rgba){0.72, 0.72, 0.76, 1.00};
    p.is_dark     = 1;
    return p;
}

static gt_palette current_palette(void) {
    return g_have_palette ? g_palette : default_dark_palette();
}

static NSColor *nscolor(gt_rgba c) {
    return [NSColor colorWithSRGBRed:c.r green:c.g blue:c.b alpha:c.a];
}

// c mixed toward white by t (0..1), keeping a solid alpha. Used for the selection outline and the
// placeholder ink so they read against tile_sel_bg / tile_bg without a second palette entry.
static NSColor *lighten(gt_rgba c, double t) {
    return [NSColor colorWithSRGBRed:c.r + (1.0 - c.r) * t
                               green:c.g + (1.0 - c.g) * t
                                blue:c.b + (1.0 - c.b) * t
                               alpha:1.0];
}

static NSString *tile_string(const char *bytes, uint16_t len) {
    if (len == 0) return @"";
    NSString *s = [[NSString alloc] initWithBytes:bytes length:len encoding:NSUTF8StringEncoding];
    return s ? [s autorelease] : @"";
}

// The thumbnail sub-rect of a tile frame, in the flipped content view's coordinates. Shared by the
// renderer and the CALayer placement so the two never disagree.
static NSRect tile_image_rect(NSRect tile) {
    NSRect inner = NSInsetRect(tile, kTilePad, kTilePad);
    if (inner.size.width <= 0 || inner.size.height <= 0) return NSZeroRect;
    if (inner.size.height > kLabelStrip + 12.0) {
        inner.size.height -= kLabelStrip + kLabelGap;
    }
    return inner;
}

// ---------------------------------------------------------------------------
// One synthetic accessibility element per tile (P5.2). NSAccessibilityElement already does the
// flipped-parent coordinate conversion for accessibilityFrameInParentSpace, which is why the tiles
// can be reported in the same top-left frame drawRect: uses. Role is "button" because activating a
// tile means "switch to this window" -- though the press itself travels the ordinary path (release
// the modifier), since panel.h exposes no Go callback to raise a window from here; accessibility
// PerformPress is a deliberate no-op. Compiled without ARC: elements are owned by g_a11y_children.
@interface GTTileElement : NSAccessibilityElement {
@public
    BOOL tileSelected;
}
@end

@implementation GTTileElement
- (NSAccessibilityRole)accessibilityRole {
    return NSAccessibilityButtonRole;
}
- (BOOL)isAccessibilitySelected {
    return tileSelected;
}
- (BOOL)accessibilityPerformPress {
    return NO;
}
@end

// The label a screen reader speaks for a tile: "<title>, <app>", or just the app when the window has
// no title, or a last-resort constant so an element is never silent.
static NSString *a11y_label(const gt_tile *t) {
    NSString *title = tile_string(t->title, t->title_len);
    NSString *app = tile_string(t->subtitle, t->subtitle_len);
    if (title.length == 0) return app.length ? app : @"Untitled window";
    if (app.length == 0) return title;
    return [NSString stringWithFormat:@"%@, %@", title, app];
}

// Bring g_a11y_children to exactly g_ntiles elements and refresh each one's label, frame and selected
// state from g_tiles. Grows and shrinks at the tail like g_tile_layers; a shrink releases the tail
// elements through the array. Must run on the main thread (panel.h THREADING) and after store_tiles.
static void a11y_sync(void) {
    if (!g_content) return;
    if (!g_a11y_children) g_a11y_children = [[NSMutableArray alloc] init];

    while ((int32_t)g_a11y_children.count > g_ntiles) {
        [g_a11y_children removeLastObject];
    }
    while ((int32_t)g_a11y_children.count < g_ntiles) {
        GTTileElement *e = [[GTTileElement alloc] init];
        [e setAccessibilityParent:g_content];
        [g_a11y_children addObject:e];
        [e release];
    }
    for (int32_t i = 0; i < g_ntiles; i++) {
        GTTileElement *e = g_a11y_children[i];
        const gt_tile *t = &g_tiles[i];
        [e setAccessibilityLabel:a11y_label(t)];
        [e setAccessibilityFrameInParentSpace:NSMakeRect(t->x, t->y, t->w, t->h)];
        e->tileSelected = (t->selected != 0);
    }
}

// ---------------------------------------------------------------------------
// The one view that draws every tile. The conventional design gives every tile its own NSView and
// pays a C->Go callback per tile; this draws them all in drawRect: and keeps thumbnails in dumb
// CALayers that never call back.
// ---------------------------------------------------------------------------

@interface GTTileView : NSView
@end

@implementation GTTileView

- (BOOL)isFlipped {
    return YES;
}

// --- Accessibility (P5.2). The view paints the tiles, so it stands in as their container. ---
- (BOOL)isAccessibilityElement {
    return NO;
}
- (NSAccessibilityRole)accessibilityRole {
    return NSAccessibilityGroupRole;
}
- (NSString *)accessibilityLabel {
    return @"Window switcher";
}
- (NSArray *)accessibilityChildren {
    return g_a11y_children ? [[g_a11y_children copy] autorelease] : @[];
}
- (NSArray *)accessibilitySelectedChildren {
    NSMutableArray *sel = [NSMutableArray array];
    for (GTTileElement *e in g_a11y_children) {
        if (e->tileSelected) [sel addObject:e];
    }
    return sel;
}

- (void)drawPlaceholderInRect:(NSRect)r palette:(gt_palette)pal {
    if (r.size.width < 16 || r.size.height < 16) return;

    NSBezierPath *bg = [NSBezierPath bezierPathWithRoundedRect:r xRadius:6 yRadius:6];
    NSColor *fill = pal.is_dark ? [NSColor colorWithSRGBRed:1 green:1 blue:1 alpha:0.05]
                                : [NSColor colorWithSRGBRed:0 green:0 blue:0 alpha:0.05];
    [fill setFill];
    [bg fill];

    NSColor *ink = [nscolor(pal.label_dim) colorWithAlphaComponent:0.55];
    [ink setStroke];
    [bg setLineWidth:1.0];
    [bg stroke];

    // A minimal "image not here yet" glyph: a sun disc and a mountain ridge inside the frame. Cheap
    // to stroke every keystroke and unmistakably a placeholder rather than a failed draw.
    NSRect g = NSInsetRect(r, r.size.width * 0.26, r.size.height * 0.30);
    if (g.size.width < 8 || g.size.height < 8) return;
    [ink setFill];

    CGFloat disc = fmin(g.size.width, g.size.height) * 0.28;
    NSRect sun = NSMakeRect(NSMinX(g), NSMinY(g), disc, disc);
    [[NSBezierPath bezierPathWithOvalInRect:sun] fill];

    NSBezierPath *ridge = [NSBezierPath bezierPath];
    [ridge moveToPoint:NSMakePoint(NSMinX(g), NSMaxY(g))];
    [ridge lineToPoint:NSMakePoint(NSMinX(g) + g.size.width * 0.40, NSMinY(g) + g.size.height * 0.45)];
    [ridge lineToPoint:NSMakePoint(NSMinX(g) + g.size.width * 0.62, NSMinY(g) + g.size.height * 0.70)];
    [ridge lineToPoint:NSMakePoint(NSMinX(g) + g.size.width * 0.82, NSMinY(g) + g.size.height * 0.38)];
    [ridge lineToPoint:NSMakePoint(NSMaxX(g), NSMaxY(g))];
    [ridge closePath];
    [ridge fill];
}

- (void)drawImage:(CGImageRef)img inRect:(NSRect)r {
    if (!img || r.size.width < 2 || r.size.height < 2) return;
    size_t iw = CGImageGetWidth(img), ih = CGImageGetHeight(img);
    if (iw == 0 || ih == 0) return;

    CGFloat scale = fmin(r.size.width / (CGFloat)iw, r.size.height / (CGFloat)ih);
    CGFloat dw = (CGFloat)iw * scale, dh = (CGFloat)ih * scale;
    NSRect dst = NSMakeRect(NSMidX(r) - dw / 2.0, NSMidY(r) - dh / 2.0, dw, dh);

    // respectFlipped:YES draws upright in this flipped view without a manual CTM flip.
    NSImage *im = [[NSImage alloc] initWithCGImage:img size:NSZeroSize];
    [im drawInRect:dst
          fromRect:NSZeroRect
         operation:NSCompositingOperationSourceOver
          fraction:1.0
    respectFlipped:YES
             hints:nil];
    [im release];
}

- (void)drawRect:(NSRect)dirty {
    (void)dirty;
    @autoreleasepool {
        gt_palette pal = current_palette();
        NSRect b = [self bounds];

        // The rounded slab. With a vibrancy material the NSVisualEffectView behind us is the
        // background and we leave it showing; with GT_MATERIAL_NONE we paint palette.panel_bg.
        if (g_material == GT_MATERIAL_NONE) {
            NSBezierPath *slab = [NSBezierPath bezierPathWithRoundedRect:b
                                                                xRadius:kSlabRadius
                                                                yRadius:kSlabRadius];
            [nscolor(pal.panel_bg) setFill];
            [slab fill];
        }

        if (g_ntiles == 0 || !g_tiles) return;

        NSMutableParagraphStyle *para = [[[NSMutableParagraphStyle alloc] init] autorelease];
        para.lineBreakMode = NSLineBreakByTruncatingTail;
        NSDictionary *titleAttrs = @{
            NSFontAttributeName : [NSFont systemFontOfSize:13 weight:NSFontWeightSemibold],
            NSForegroundColorAttributeName : nscolor(pal.label),
            NSParagraphStyleAttributeName : para,
        };
        NSDictionary *subAttrs = @{
            NSFontAttributeName : [NSFont systemFontOfSize:11 weight:NSFontWeightRegular],
            NSForegroundColorAttributeName : nscolor(pal.label_dim),
            NSParagraphStyleAttributeName : para,
        };

        for (int32_t i = 0; i < g_ntiles; i++) {
            gt_tile *t = &g_tiles[i];
            NSRect r = NSMakeRect(t->x, t->y, t->w, t->h);
            if (r.size.width < 2 || r.size.height < 2) continue;

            NSBezierPath *tilePath = [NSBezierPath bezierPathWithRoundedRect:r
                                                                    xRadius:kTileRadius
                                                                    yRadius:kTileRadius];
            [(t->selected ? nscolor(pal.tile_sel_bg) : nscolor(pal.tile_bg)) setFill];
            [tilePath fill];

            NSRect inner = NSInsetRect(r, kTilePad, kTilePad);
            NSRect imageRect = tile_image_rect(r);

            // Thumbnail if this frame carries one (a warm cache hit at summon); otherwise the
            // placeholder so the tile is never a hole (D12). Independent of that, P3.3 may drop a
            // CGImage on gt_panel_tile_layer(i), which composites on top of everything here.
            if (imageRect.size.height > 8.0) {
                if (t->image) {
                    [self drawImage:(CGImageRef)t->image inRect:imageRect];
                } else {
                    [self drawPlaceholderInRect:imageRect palette:pal];
                }
            }

            // Title + subtitle in the bottom strip.
            if (inner.size.width > 4.0) {
                CGFloat ly = (imageRect.size.height > 8.0) ? NSMaxY(imageRect) + kLabelGap
                                                           : inner.origin.y;
                NSRect titleLine = NSMakeRect(inner.origin.x, ly, inner.size.width, 17);
                NSRect subLine = NSMakeRect(inner.origin.x, ly + 17, inner.size.width, 15);
                if (NSMaxY(subLine) <= NSMaxY(inner) + 2.0) {
                    [tile_string(t->title, t->title_len) drawInRect:titleLine withAttributes:titleAttrs];
                    [tile_string(t->subtitle, t->subtitle_len) drawInRect:subLine withAttributes:subAttrs];
                } else {
                    [tile_string(t->title, t->title_len) drawInRect:titleLine withAttributes:titleAttrs];
                }
            }

            // Selection outline, drawn last and inset 1pt so it stays visible even when P3.3's
            // thumbnail layer (inset kTilePad) is opaque.
            if (t->selected) {
                NSBezierPath *ring = [NSBezierPath bezierPathWithRoundedRect:NSInsetRect(r, 1.0, 1.0)
                                                                    xRadius:kTileRadius
                                                                    yRadius:kTileRadius];
                [ring setLineWidth:2.0];
                [lighten(pal.tile_sel_bg, 0.45) setStroke];
                [ring stroke];
            }
        }
    }
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
    [[g_effect layer] setCornerRadius:kSlabRadius];
    [[g_effect layer] setMasksToBounds:YES];
    [g_panel setContentView:g_effect];

    g_content = [[GTTileView alloc] initWithFrame:frame];
    [g_content setWantsLayer:YES];
    [g_content setAutoresizingMask:(NSViewWidthSizable | NSViewHeightSizable)];
    [g_effect addSubview:g_content];

    g_tile_layers = [[NSMutableArray alloc] init];
    return GT_OK;
}

// Deep-copies the incoming tiles into g_tiles (see the note on that global). tiles may be NULL when
// n == 0; anything else with a NULL pointer is treated as n == 0.
static void store_tiles(const gt_tile *tiles, int32_t n) {
    if (n < 0 || !tiles) n = 0;
    if (n == 0) {
        free(g_tiles);
        g_tiles = NULL;
        g_ntiles = 0;
        return;
    }
    gt_tile *buf = realloc(g_tiles, (size_t)n * sizeof(gt_tile));
    if (!buf) return; // keep the previous frame rather than crash; next summon retries
    g_tiles = buf;
    memcpy(g_tiles, tiles, (size_t)n * sizeof(gt_tile));
    g_ntiles = n;
}

// The shared body of show and update: copy the tiles, resize the CALayer array to match, place each
// layer over its tile's thumbnail sub-rect, and mark the view for redraw. Deliberately does NOT
// resolve a screen or order the window -- gt_panel_show owns both of those.
static gt_status panel_populate(const gt_tile *tiles, int32_t n) {
    if (!g_panel) return GT_ERR_INTERNAL;
    if (!tiles || n < 0) n = 0;

    store_tiles(tiles, n);

    // No implicit animation: on the keystroke path every tile's frame can move and the default
    // 0.25 s CALayer action would smear the whole strip (D13 separates this path from the summon).
    [CATransaction begin];
    [CATransaction setDisableActions:YES];

    while ((int32_t)g_tile_layers.count > n) {
        [[g_tile_layers lastObject] removeFromSuperlayer];
        [g_tile_layers removeLastObject];
    }
    while ((int32_t)g_tile_layers.count < n) {
        CALayer *l = [CALayer layer];
        l.contentsGravity = kCAGravityResizeAspect;
        l.masksToBounds = YES;
        l.cornerRadius = 6.0;
        [[g_content layer] addSublayer:l];
        [g_tile_layers addObject:l];
    }
    for (int32_t i = 0; i < n; i++) {
        CALayer *l = g_tile_layers[i];
        NSRect img = tile_image_rect(NSMakeRect(tiles[i].x, tiles[i].y, tiles[i].w, tiles[i].h));
        l.frame = NSRectToCGRect(img);
    }

    [CATransaction commit];

    // Accessibility (P5.2): keep the synthetic tile elements in step, and speak the selection. A
    // changed tile count is a layout change; a moved selection (or the first frame of a summon, which
    // gt_panel_hide armed by resetting the trackers to -1) is spoken once at high priority so a
    // VoiceOver user hears each ⌥⇥ cycle. All of this is inert when VoiceOver is not running.
    @autoreleasepool {
        int32_t sel = -1;
        for (int32_t i = 0; g_tiles && i < g_ntiles; i++) {
            if (g_tiles[i].selected) { sel = i; break; }
        }
        a11y_sync();

        BOOL setChanged = (g_ntiles != g_a11y_last_n);
        if (setChanged) {
            NSAccessibilityPostNotification(g_content, NSAccessibilityLayoutChangedNotification);
        }
        if (sel >= 0 && (sel != g_a11y_last_selected || setChanged)) {
            NSAccessibilityPostNotificationWithUserInfo(
                g_content, NSAccessibilityAnnouncementRequestedNotification,
                @{NSAccessibilityAnnouncementKey : a11y_label(&g_tiles[sel]),
                  NSAccessibilityPriorityKey : @(NSAccessibilityPriorityHigh)});
            NSAccessibilityPostNotification(g_content,
                                            NSAccessibilitySelectedChildrenChangedNotification);
        }
        g_a11y_last_selected = sel;
        g_a11y_last_n = g_ntiles;
    }

    [g_content setNeedsDisplay:YES];
    return GT_OK;
}

// The screen under the mouse is where the user is looking -- not mainScreen, which is wherever the
// key window / menu bar is. mouseLocation and NSScreen.frame are both global, bottom-left. Shared by
// gt_panel_show (placement) and gt_active_screen (so core.Layout sizes for the same display).
static NSScreen *screen_under_mouse(void) {
    NSPoint mouse = [NSEvent mouseLocation];
    for (NSScreen *s in [NSScreen screens]) {
        if (NSPointInRect(mouse, [s frame])) return s;
    }
    return [NSScreen mainScreen];
}

void gt_active_screen(int32_t *width_pt, int32_t *height_pt, int32_t *scale) {
    NSScreen *s = screen_under_mouse();
    NSRect vf = s ? [s visibleFrame] : NSZeroRect;
    if (width_pt) *width_pt = (int32_t)vf.size.width;
    if (height_pt) *height_pt = (int32_t)vf.size.height;
    if (scale) *scale = s ? (int32_t)[s backingScaleFactor] : 1;
}

gt_status gt_panel_show(const gt_tile *tiles, int32_t n, int32_t panel_w, int32_t panel_h) {
    if (!g_panel) return GT_ERR_INTERNAL;
    if (panel_w <= 0 || panel_h <= 0) return GT_ERR_INTERNAL;

    NSScreen *screen = screen_under_mouse();
    if (!screen) return GT_ERR_UNAVAILABLE;

    NSRect vf = [screen visibleFrame];
    NSRect frame = NSMakeRect(NSMidX(vf) - panel_w / 2.0,
                              NSMidY(vf) - panel_h / 2.0,
                              panel_w, panel_h);
    [g_panel setFrame:frame display:NO];

    gt_status st = panel_populate(tiles, n);
    if (st != GT_OK) return st;

    // orderFrontRegardless, not orderFront: an Accessory app may not bring a window forward through
    // the ordinary path. The non-activating style mask keeps the app behind us frontmost -- P0.1
    // checked the frontmost pid was unchanged across a summon 20x (D13).
    if (atomic_exchange(&g_timing_armed, 0)) {
        // V6.3 scaffold. Same edge spike/panel measured (D13): the completion block fires when this
        // transaction is handed to the render server -- the closest reachable thing to "pixels"
        // without a display link. The app's own main run loop drains it; no nested pump.
        [CATransaction begin];
        [CATransaction setCompletionBlock:^{ goTimingFrameCommitted(); }];
        [g_panel orderFrontRegardless];
        [CATransaction commit];
    } else {
        [g_panel orderFrontRegardless];
    }
    return GT_OK;
}

gt_status gt_panel_update(const gt_tile *tiles, int32_t n) {
    if (!g_panel || ![g_panel isVisible]) return GT_OK;
    return panel_populate(tiles, n);
}

void gt_panel_hide(void) {
    [g_panel orderOut:nil];
    // Re-arm the announcement: the next summon must speak its initial selection even if the panel
    // comes back with the same tile count and selection index as last time (P5.2).
    g_a11y_last_selected = -1;
    g_a11y_last_n = -1;
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
    if (g_effect) {
        if (material == GT_MATERIAL_NONE) {
            // Vibrancy off: drop the effect view to inactive (there is no true "no material") and let
            // GTTileView paint palette.panel_bg over it. P3.4 owns finer control from theme.m.
            [g_effect setState:NSVisualEffectStateInactive];
        } else {
            [g_effect setState:NSVisualEffectStateActive];
            [g_effect setMaterial:(NSVisualEffectMaterial)material];
        }
    }
    [g_content setNeedsDisplay:YES];
}
