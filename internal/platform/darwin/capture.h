// Thumbnail capture through ScreenCaptureKit. Kept free of Objective-C so cgo can include it.
//
// Same conventions as shim.h and no second shape: gt_ prefix, gt_status for anything that can fail,
// bitmaps handed back as opaque gt_image_ref handles the caller must release, nothing here allocates
// memory Go is expected to free.
//
// Why SCK at all: CGWindowListCreateImage is obsoleted in macOS 15 (D3), so this is the only path to
// a thumbnail. SCK is asynchronous and block-based; Go cannot hold an Objective-C block, so capture.m
// wraps the async API into the one blocking call below and Go never sees a block.
//
// Two facts from spike/sck that shape this file (D12):
//   - getShareableContent costs ~46 ms, half the summon budget, so the window list is cached and the
//     enumeration cost is paid on a cache miss rather than per thumbnail.
//   - a capture is ~46 ms warm / ~112 ms cold and does NOT parallelise. Capture therefore cannot run
//     on the summon path at all; this is an API for filling a cache *ahead* of a summon.
#ifndef GOTAB_DARWIN_CAPTURE_H
#define GOTAB_DARWIN_CAPTURE_H

#include <stdint.h>
#include "shim.h"

// Captures one window, downscaled to at most max_w pixels wide, and blocks until it completes.
// Safe from any thread and does not need a run loop: the completion handler fires on a queue of
// SCK's choosing, and D12 measured that working from a Go-owned thread with no NSRunLoop.
//
// On GT_OK *out_img receives a live handle the caller MUST pass to gt_image_release exactly once.
// On anything else *out_img is NULL and there is nothing to release.
//
// timeout_ms bounds EACH blocking phase, not the call: a capture whose window is not in the cache
// refreshes first, so the worst case is two timeouts. A handler that never fires yields
// GT_ERR_TIMEOUT rather than a hang - that is not hypothetical, spike/sck saw it fire.
//
// Errors map to shim.h's closed enum and nothing wider:
//   GT_ERR_NO_RECORDING - Screen Recording is not granted; nothing is capturable without it
//   GT_ERR_UNAVAILABLE  - SCK is missing (macOS < 14, see below), or the window is gone. A capture
//                         that fails for any other reason lands here too: the window closing between
//                         the lookup and the capture is by far the likeliest cause, and it is an
//                         ordinary event for a prefetcher rather than a fault.
//   GT_ERR_TIMEOUT      - a completion handler did not fire in time
//   GT_ERR_INTERNAL     - a bad argument, or an allocation that failed. It always means a bug in this
//                         shim or its caller, never an ordinary runtime failure.
//
// SCScreenshotManager is macOS 14+ and the rest of SCK is 12.3+, against a minos of 12.0 (D17). The
// class is therefore checked at runtime -- @available plus NSClassFromString, because a weak-linked
// framework that is absent resolves its classes to NULL -- and this returns GT_ERR_UNAVAILABLE on
// 12.0-13.x instead of assuming the API is there. Not failing to LAUNCH on 12.0-12.2 is a separate
// requirement and a build-time one: it needs the framework weak-linked, which needs
// CGO_LDFLAGS_ALLOW. See the cgo preamble in capture.go.
gt_status gt_capture(uint32_t window_id, int32_t max_w, int32_t timeout_ms, gt_image_ref *out_img);

// Pixel dimensions of a handle. Reads the CGImage header only; no bitmap is touched, no copy is made.
// Zero on a NULL handle. This is how a caller confirms the downscale happened at capture time rather
// than trusting that it did.
void gt_image_size(gt_image_ref img, int32_t *out_w, int32_t *out_h);

#endif
