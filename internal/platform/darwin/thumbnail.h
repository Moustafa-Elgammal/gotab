// The last hop of the thumbnail path: a captured bitmap onto a panel tile's CALayer. Kept free of
// Objective-C so cgo can include it directly.
//
// Conventions are shim.h's and panel.h's -- gt_ prefix, nothing allocated that Go frees -- plus the
// one that matters here: this function touches CoreAnimation and MUST run on the thread cmd/gotab
// locked in init and handed to the run loop (docs/ARCHITECTURE.md#threading). It is not thread-safe;
// thumbnail.go calls it only from inside darwin.OnMain. It never blocks.
//
// P3.3's contract is docs/tasks/P3.3.md. The prefetcher that drives this -- one background capture
// goroutine feeding core.Cache, holding every gt_image_ref it has shown and releasing it on eviction
// or Stop -- lives in thumbnail.go. This file is only "image handle -> layer contents".
#ifndef GOTAB_DARWIN_THUMBNAIL_H
#define GOTAB_DARWIN_THUMBNAIL_H

#include <stdint.h>

#include "shim.h" // gt_image_ref

// Sets tile `index`'s backing CALayer (the one gt_panel_tile_layer hands out) to show `image`, and
// pins the layer's contentsScale to the panel window's backing scale so the bitmap is not resampled
// soft on a Retina display (docs/tasks/P3.3.md notes).
//
// `image` is BORROWED. gt_image_ref is the retained CGImageRef that gt_image_adopt wrapped;
// assigning it to CALayer.contents makes CoreAnimation take its own retain for as long as the layer
// shows it, and drop that retain when the contents change or the layer is torn down. This function
// neither retains nor releases against gt_image_live -- thumbnail.go owns that handle and calls
// gt_image_release exactly once, on cache eviction or teardown (panel.h: the panel never releases a
// tile image). Passing NULL clears the tile back to P3.1's placeholder.
//
// No-op when the panel is not on screen, does not exist, or `index` is out of range for the current
// tile count (gt_panel_tile_layer returns NULL). A capture can finish just after the panel was
// hidden or re-shown with fewer tiles, and that race is ordinary for a background prefetcher, not a
// fault.
//
// MAIN THREAD ONLY.
void gt_thumbnail_set(int32_t index, gt_image_ref image);

#endif
