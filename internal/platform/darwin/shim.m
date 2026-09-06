// The Objective-C side of the platform layer.
//
// Compiled WITHOUT ARC: cgo does not pass -fobjc-arc, and every spike that works (panel, sck, hotkey)
// is manual retain/release. A half-ARC shim would be worse than a consistent manual one. CoreFoundation
// and CGImage are outside ARC's remit anyway, and they are what this layer mostly holds.
#import <Foundation/Foundation.h>
#import <ApplicationServices/ApplicationServices.h>
#import <CoreGraphics/CoreGraphics.h>
#include <stdatomic.h>
#include "shim.h"

// Defined in Go. Called on the main queue, never anywhere else.
extern void goDispatchMain(uintptr_t token);

static atomic_llong g_images_live = 0;

gt_status gt_init(void) {
    static dispatch_once_t once;
    static gt_status result = GT_OK;
    dispatch_once(&once, ^{
        // Load bearing, and it looks like a no-op whose result is discarded.
        //
        // GoTab is LSUIElement and reaches CoreGraphics from a Go goroutine rather than through
        // NSApplication's normal startup. D12 found that in a process shaped like that, CoreGraphics
        // abort()s on its FIRST capture -- not returns an error, aborts -- unless the display
        // subsystem has been touched first. CGMainDisplayID() is that touch.
        //
        // It lives here rather than in P2.6 because the abort fires in whichever code path reaches
        // CoreGraphics first, and by Phase 3 that is the panel, not the capture. Debugging it from
        // there would mean rediscovering D12.
        (void)CGMainDisplayID();
    });
    return result;
}

int32_t gt_trusted(void) {
    // AXIsProcessTrusted, not AXIsProcessTrustedWithOptions: the WithOptions form can raise the
    // system prompt, and a status query that has a side effect on the user's screen is a trap for
    // every caller that just wanted to render a disabled state. Prompting is P4.3's decision to make.
    return AXIsProcessTrusted() ? 1 : 0;
}

int32_t gt_can_record(void) {
    // Preflight, not Request: same reason as above. Request pops the dialog and, worse, macOS only
    // ever shows it once per process lifetime.
    return CGPreflightScreenCaptureAccess() ? 1 : 0;
}

void gt_image_release(gt_image_ref img) {
    if (!img) return;
    CGImageRelease((CGImageRef)img);
    atomic_fetch_sub_explicit(&g_images_live, 1, memory_order_relaxed);
}

int64_t gt_image_live(void) {
    return (int64_t)atomic_load_explicit(&g_images_live, memory_order_relaxed);
}

void gt_dispatch_main(uintptr_t token) {
    // Async, never dispatch_sync. A sync hop from the event-loop goroutine to a main thread that is
    // itself waiting on that goroutine is a deadlock, and it is the kind that only appears under
    // load. The Go side owns the token's lifetime and frees it inside the callback.
    dispatch_async(dispatch_get_main_queue(), ^{
        goDispatchMain(token);
    });
}
