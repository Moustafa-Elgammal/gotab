// The ScreenCaptureKit side of the platform layer: one blocking, time-bounded capture.
//
// Compiled WITHOUT ARC, like shim.m -- cgo does not pass -fobjc-arc -- so retain/release here is
// manual, and CGImage is outside ARC's remit in any case.
//
// Four things in this file are load-bearing and every one of them looks removable. They are the
// findings of spike/sck, recorded as D12 and D14, not guesses:
//
//   - gt_init() at the top of gt_capture. CoreGraphics only opens its WindowServer connection when
//     something touches the display subsystem, and a Go process that is not an NSApplication never
//     does. The FIRST SCScreenshotManager call in such a process then dies on
//     `Assertion failed: (did_initialize), CGS_REQUIRE_INIT` -- an abort(), with no error to handle.
//     gt_init is idempotent and costs a dispatch_once after the first call, so paying it here rather
//     than trusting a caller to have paid it is the cheap side of an unrecoverable bet.
//   - The shareable-content cache. getShareableContent costs ~46 ms, half the whole summon budget.
//     Calling it per thumbnail makes every capture look twice as expensive as it is.
//   - The dispatch_semaphore timeouts. D12 saw a capture simply never answer -- once in ~10 runs,
//     not reproducible -- so a capture is failable and time-bounded rather than assumed to return.
//   - CGImageRetain inside the completion block. The image SCK hands over is autoreleased and owned
//     by SCK; without the retain it dies when the block returns and Go holds a dangling pointer that
//     keeps appearing to work for a while.
//
// And one thing that is load-bearing and is NOT in the spike: the capture context is heap-allocated
// and reference-counted. On timeout the completion block is still outstanding and will run later,
// into whatever it captured. The spike solved that by leaking a malloc'd result buffer, which it was
// entitled to do; here the block and the waiter each hold a reference and whichever finishes last
// frees the context -- and releases the image, if a late handler produced one nobody is waiting for.
#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#include <math.h>
#include <stdlib.h>
#include <pthread.h>
#include <stdatomic.h>
#include "capture.h"

// The cached shareable-window list, and the lock that makes gt_capture callable from any goroutine.
//
// A pthread mutex rather than os_unfair_lock because the critical section can include a ~46 ms
// getShareableContent: this wants a lock that sleeps, not one that assumes it will be brief. The
// capture itself runs OUTSIDE the lock -- the SCWindow is retained and the lock dropped first -- so
// concurrent captures do not serialise here. They serialise in the WindowServer instead, which D12
// measured and which no lock of ours can change.
static SCShareableContent *g_content = nil;
static pthread_mutex_t g_content_lock = PTHREAD_MUTEX_INITIALIZER;

// Is ScreenCaptureKit's screenshot API actually present?
//
// @available is what silences -Wunguarded-availability-new under the minos 12.0 that D17 pins, and
// the NSClassFromString is not redundant with it: with the framework WEAK-linked (see capture.go)
// its classes resolve to NULL when it is absent, and a NULL class must be detected rather than
// messaged. SCScreenshotManager is macOS 14+; the rest of SCK is 12.3+, so this one check covers
// every version on which any of it could be missing.
static BOOL sck_present(void) {
    if (@available(macOS 14.0, *)) {
        return NSClassFromString(@"SCScreenshotManager") != nil
            && NSClassFromString(@"SCShareableContent") != nil;
    }
    return NO;
}

// Refreshes g_content. Caller holds g_content_lock. Returns GT_OK or the status to report.
//
// A getShareableContent failure is almost always a missing Screen Recording grant, but SCK does not
// say so in a form worth modelling, so the grant is asked directly instead. That is a preflight and
// never prompts (see gt_can_record in shim.m).
static gt_status refresh_locked(int32_t timeout_ms) {
    __block SCShareableContent *result = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *content, NSError *error) {
        if (content && !error) result = [content retain];
        dispatch_semaphore_signal(sem);
    }];

    long waited = dispatch_semaphore_wait(
        sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeout_ms * NSEC_PER_MSEC));
    dispatch_release(sem);

    if (waited != 0) {
        // The handler is still outstanding. It writes only to __block storage the block itself keeps
        // alive, and a late `result` is a leaked SCShareableContent -- bounded by one per timeout,
        // which D12 measured at roughly one in ten runs of a spike hammering the API.
        return GT_ERR_TIMEOUT;
    }
    if (!result) {
        return CGPreflightScreenCaptureAccess() ? GT_ERR_UNAVAILABLE : GT_ERR_NO_RECORDING;
    }
    if (g_content) [g_content release];
    g_content = result;
    return GT_OK;
}

// Caller holds g_content_lock. Returns a borrowed SCWindow, or nil.
static SCWindow *find_window_locked(uint32_t wanted) {
    if (!g_content) return nil;
    for (SCWindow *w in g_content.windows) {
        if ((uint32_t)w.windowID == wanted) return w;
    }
    return nil;
}

// Downscale AT CAPTURE TIME (docs/ARCHITECTURE.md, memory rule 4). Capturing full-res and shrinking
// afterwards allocates the full bitmap first, which is the whole cost being avoided -- and D12
// measured that it buys nothing anyway: a 400x237 capture and a 1512x897 one both cost ~45 ms,
// because the time is round-trip latency to the WindowServer and not pixel work.
//
// Only a width is requested; the aspect ratio decides the height. Never upscales: max_w is a bound,
// not a target, and a 200 px window asked for at 400 px stays 200 px rather than becoming a blurry
// bitmap twice the size.
static SCStreamConfiguration *config_for(SCWindow *win, int32_t max_w) {
    CGFloat sw = win.frame.size.width, sh = win.frame.size.height;
    if (sw < 1 || sh < 1) return nil;

    CGFloat scale = (CGFloat)max_w / sw;
    if (scale > 1.0) scale = 1.0;

    size_t w = (size_t)lround(sw * scale);
    size_t h = (size_t)lround(sh * scale);
    if (w < 1) w = 1;
    if (h < 1) h = 1;

    SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
    cfg.width = w;
    cfg.height = h;
    cfg.showsCursor = NO;
    cfg.scalesToFit = YES;
    return cfg;
}

// The context a capture and its completion block share. Heap-allocated and reference-counted because
// the block outlives the call on timeout: see the file header.
typedef struct capture_ctx {
    dispatch_semaphore_t sem;
    CGImageRef image;      // written by the handler before it signals, read by the waiter after
    atomic_int refs;       // one for the waiter, one for the block
    atomic_bool ok;        // the handler produced an image
} capture_ctx;

static void ctx_release(capture_ctx *ctx) {
    if (atomic_fetch_sub_explicit(&ctx->refs, 1, memory_order_acq_rel) != 1) return;
    // Last one out. A surviving image means the handler answered after the waiter gave up, so this
    // is the only place that can free it -- and 330 MB of surfaces for 60 images (D12) is what a
    // handful of these would cost if it did not.
    if (ctx->image) CGImageRelease(ctx->image);
    dispatch_release(ctx->sem);
    free(ctx);
}

gt_status gt_capture(uint32_t window_id, int32_t max_w, int32_t timeout_ms, gt_image_ref *out_img) {
    if (!out_img) return GT_ERR_INTERNAL;
    *out_img = NULL;
    if (window_id == 0 || max_w <= 0 || timeout_ms <= 0) return GT_ERR_INTERNAL;

    // Idempotent, and the alternative to calling it is an abort() rather than an error. See header.
    gt_status init = gt_init();
    if (init != GT_OK) return init;

    if (!sck_present()) return GT_ERR_UNAVAILABLE;

    @autoreleasepool {
        SCWindow *win = nil;
        gt_status err = GT_OK;

        pthread_mutex_lock(&g_content_lock);
        win = find_window_locked(window_id);
        if (!win) {
            // One refresh per call, never a loop. A window absent from a list fetched a moment ago
            // is a window that closed, and re-asking is how a background prefetcher turns a dead id
            // into a 46 ms stall per attempt.
            err = refresh_locked(timeout_ms);
            if (err == GT_OK) {
                win = find_window_locked(window_id);
                if (!win) err = GT_ERR_UNAVAILABLE;  // gone, or never shareable
            }
        }
        // Retained so the capture can run with the lock dropped: g_content may be replaced by
        // another goroutine's refresh while this capture is in flight.
        [win retain];
        pthread_mutex_unlock(&g_content_lock);

        if (err != GT_OK) {
            [win release];  // nil on every path that sets err, and this stays correct if that changes
            return err;
        }

        SCStreamConfiguration *cfg = config_for(win, max_w);
        if (!cfg) {
            [win release];
            return GT_ERR_UNAVAILABLE;  // a degenerate frame; nothing to capture
        }
        SCContentFilter *filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:win];

        capture_ctx *ctx = calloc(1, sizeof(capture_ctx));
        if (!ctx) {
            [filter release]; [cfg release]; [win release];
            return GT_ERR_INTERNAL;
        }
        ctx->sem = dispatch_semaphore_create(0);
        atomic_init(&ctx->refs, 2);
        atomic_init(&ctx->ok, false);

        [SCScreenshotManager captureImageWithFilter:filter
                                      configuration:cfg
                                  completionHandler:^(CGImageRef image, NSError *error) {
            // D12: this fires on a GCD queue, on a Go-owned thread with no NSRunLoop, and needs no
            // marshalling to the main thread. It does nothing but hand the image over.
            if (image && !error) {
                ctx->image = CGImageRetain(image);  // autoreleased and owned by SCK until retained
                atomic_store_explicit(&ctx->ok, true, memory_order_release);
            }
            dispatch_semaphore_signal(ctx->sem);
            ctx_release(ctx);
        }];

        [filter release];
        [cfg release];
        [win release];

        long waited = dispatch_semaphore_wait(
            ctx->sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeout_ms * NSEC_PER_MSEC));

        if (waited != 0) {
            ctx_release(ctx);  // the block still holds the other reference and will clean up
            return GT_ERR_TIMEOUT;
        }
        if (!atomic_load_explicit(&ctx->ok, memory_order_acquire)) {
            ctx_release(ctx);
            // Reported as UNAVAILABLE and not INTERNAL on purpose. The overwhelmingly common cause
            // is the window going away between the lookup and the capture, which is ordinary for a
            // prefetcher and not a bug in this shim; GT_ERR_INTERNAL has to keep meaning "our fault"
            // or it stops being actionable. A missing grant is still separated out, because that one
            // the user can fix.
            return CGPreflightScreenCaptureAccess() ? GT_ERR_UNAVAILABLE : GT_ERR_NO_RECORDING;
        }

        CGImageRef img = ctx->image;
        ctx->image = NULL;  // ownership moves to the caller before the context can free it
        ctx_release(ctx);

        // ESCALATION, recorded in docs/tasks/P2.6.md: the live count that gt_image_live() reports is
        // shim.m's `static atomic_llong g_images_live`, and it has no increment side -- the only
        // function that touches it, gt_image_release, decrements. A handle produced here is
        // therefore never counted, so the count reads 0 while an image is held and -1 after it is
        // released. Fixing it means adding one producer-side function to shim.h/shim.m, which P2.6
        // owns neither of. This line is where that call goes:
        //     *out_img = gt_image_adopt(img);
        *out_img = (gt_image_ref)img;
        return GT_OK;
    }
}

void gt_image_size(gt_image_ref img, int32_t *out_w, int32_t *out_h) {
    // Reads the CGImage header only. No bitmap page is touched, so this does not fault an idle
    // IOSurface back in -- which matters, because D12 measured SCK output as surfaces that are
    // mapped but not resident until something reads them.
    if (out_w) *out_w = img ? (int32_t)CGImageGetWidth((CGImageRef)img) : 0;
    if (out_h) *out_h = img ? (int32_t)CGImageGetHeight((CGImageRef)img) : 0;
}
