// The CoreAnimation last hop of the thumbnail path: a captured bitmap onto a panel tile's CALayer.
//
// Compiled WITHOUT ARC, like every other .m in this package. gt_image_ref is a retained CGImageRef
// (shim.m's gt_image_adopt casts one straight across), and handing it to CALayer.contents is the
// whole job: CoreAnimation retains it for as long as it is a layer's contents and drops that retain
// when the contents change or the layer goes away. thumbnail.go holds the reference that balances
// gt_image_adopt and releases it on eviction or Stop; nothing here moves gt_image_live.
//
// Everything here runs on the AppKit main thread -- thumbnail.go wraps each call in darwin.OnMain --
// because -setContents: is a CoreAnimation mutation and CALayer is not thread-safe.
#import <Foundation/Foundation.h>
#import <QuartzCore/QuartzCore.h>
#import <AppKit/AppKit.h>
#include "thumbnail.h"
#include "panel.h" // gt_panel_tile_layer, gt_panel_window, gt_panel_visible

// The panel window's backing scale, with sane fallbacks. A tile layer's contents are pixel data at
// this density: thumbnail.go captures at point-size x scale, so contentsScale must be that same
// scale or CoreAnimation assumes 1.0 and a Retina panel shows every thumbnail at half resolution.
static CGFloat panel_backing_scale(void) {
    NSWindow *w = (NSWindow *)gt_panel_window();
    if (w) return [w backingScaleFactor];
    NSScreen *s = [NSScreen mainScreen];
    return s ? [s backingScaleFactor] : 1.0;
}

void gt_thumbnail_set(int32_t index, gt_image_ref image) {
    // Skip painting a panel that is not on screen: a capture queued for the last summon can land
    // after the panel was hidden, and gt_panel_tile_layer would still return a (reused) layer.
    if (!gt_panel_visible()) return;

    CALayer *layer = (CALayer *)gt_panel_tile_layer(index);
    if (!layer) return; // panel gone, or re-shown with fewer tiles since the capture was queued

    // contentsScale before contents so the first display pass already has the right density.
    layer.contentsScale = panel_backing_scale();
    // contentsGravity is P3.1's -- it sets kCAGravityResizeAspect when it builds the layer -- and is
    // left alone here so aspect handling stays one task's decision.
    layer.contents = (id)image; // NULL clears to the placeholder; non-NULL is retained by CoreAnimation
}
