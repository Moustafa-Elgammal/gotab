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

// ---------------------------------------------------------------------------
// Window enumeration
// ---------------------------------------------------------------------------

// Copies a CFString into a fixed C buffer as UTF-8 and returns the byte length.
//
// CFStringGetBytes, not CFStringGetCString: CFStringGetCString fails outright when the buffer is too
// small, which would drop a long title entirely. CFStringGetBytes fills what fits and stops at the
// last WHOLE character, so a truncated title is still valid UTF-8 rather than a byte sequence Go's
// string conversion would replace with U+FFFD. Window titles are user text and contain emoji, CJK and
// combining marks constantly, so this is the common case, not an edge case.
static uint16_t copy_cfstring(CFStringRef s, char *dst, int cap) {
    if (cap <= 0) return 0;
    dst[0] = 0;
    if (!s) return 0;
    CFIndex used = 0;
    CFStringGetBytes(s, CFRangeMake(0, CFStringGetLength(s)), kCFStringEncodingUTF8,
                     0, false, (UInt8 *)dst, (CFIndex)(cap - 1), &used);
    if (used < 0) used = 0;
    dst[used] = 0;
    return (uint16_t)used;
}

static int32_t dict_int32(CFDictionaryRef d, CFStringRef key, int32_t fallback) {
    CFNumberRef n = (CFNumberRef)CFDictionaryGetValue(d, key);
    int32_t v = fallback;
    if (n) CFNumberGetValue(n, kCFNumberSInt32Type, &v);
    return v;
}

gt_status gt_window_list(gt_window *buf, int32_t cap, int32_t *out_n, int32_t *out_total) {
    if (!buf || cap < 0 || !out_n || !out_total) return GT_ERR_INTERNAL;
    *out_n = 0;
    *out_total = 0;

    // ExcludeDesktopElements drops the desktop picture and the icon layer. OnScreenOnly is NOT used:
    // a minimized window is exactly the thing a switcher exists to reach, and it is off screen.
    CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListExcludeDesktopElements, kCGNullWindowID);
    if (!list) {
        // The WindowServer declining to answer is a real state, not a bug in this process -- it
        // happens during login, fast user switching, and when the session is locked.
        return GT_ERR_UNAVAILABLE;
    }

    CFIndex count = CFArrayGetCount(list);
    int32_t stored = 0, total = 0;

    for (CFIndex i = 0; i < count; i++) {
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
        if (!d) continue;

        // Layer 0 is an ordinary window. Everything else is the menubar, the Dock, a shadow, a
        // tooltip or a status item -- present in this API, not switchable by any user.
        if (dict_int32(d, kCGWindowLayer, -1) != 0) continue;

        total++;
        if (stored >= cap) continue;  // count it, drop it; the caller grows and retries

        gt_window *w = &buf[stored];
        w->id = (uint32_t)dict_int32(d, kCGWindowNumber, 0);
        w->pid = dict_int32(d, kCGWindowOwnerPID, 0);
        w->layer = 0;
        w->on_screen = CFDictionaryGetValue(d, kCGWindowIsOnscreen) == kCFBooleanTrue ? 1 : 0;

        w->alpha = 1.0f;
        CFNumberRef a = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowAlpha);
        if (a) {
            double av = 1.0;
            CFNumberGetValue(a, kCFNumberDoubleType, &av);
            w->alpha = (float)av;
        }

        // kCGWindowName is absent without the Screen Recording grant. Absent and empty are the same
        // shape here on purpose -- gt_can_record() is what distinguishes them, and duplicating that
        // answer into every one of a few hundred records would be a worse place to keep it.
        w->title_len = copy_cfstring((CFStringRef)CFDictionaryGetValue(d, kCGWindowName),
                                     w->title, GT_TITLE_MAX);
        w->app_len = copy_cfstring((CFStringRef)CFDictionaryGetValue(d, kCGWindowOwnerName),
                                   w->app, GT_APPNAME_MAX);
        stored++;
    }

    CFRelease(list);
    *out_n = stored;
    *out_total = total;
    return GT_OK;
}

void gt_dispatch_main(uintptr_t token) {
    // Async, never dispatch_sync. A sync hop from the event-loop goroutine to a main thread that is
    // itself waiting on that goroutine is a deadlock, and it is the kind that only appears under
    // load. The Go side owns the token's lifetime and frees it inside the callback.
    dispatch_async(dispatch_get_main_queue(), ^{
        goDispatchMain(token);
    });
}
