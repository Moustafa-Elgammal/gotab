// The Objective-C side of the platform layer.
//
// Compiled WITHOUT ARC: cgo does not pass -fobjc-arc, and every spike that works (panel, sck, hotkey)
// is manual retain/release. A half-ARC shim would be worse than a consistent manual one. CoreFoundation
// and CGImage are outside ARC's remit anyway, and they are what this layer mostly holds.
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
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

// ---------------------------------------------------------------------------
// Accessibility enumeration
// ---------------------------------------------------------------------------

// PRIVATE API, and the whole approach depends on it.
//
// The window model is keyed by CGWindowID (core.WindowID, frozen in P1.0) because that is what
// CGWindowList, ScreenCaptureKit and the WindowServer all speak. Accessibility does not expose that
// number through any public call. Every serious macOS window manager -- AltTab, Hammerspoon, yabai --
// uses this symbol for the same reason, which is what makes it safe in practice rather than in theory.
//
// It has been present and unchanged since 10.x. If it ever disappears the fallback is matching AX
// windows to CGWindowList entries by pid plus frame, which is ambiguous for two identically sized
// windows of one app -- so this is worth the dependency, and worth the comment saying why.
extern AXError _AXUIElementGetWindow(AXUIElementRef element, CGWindowID *out);

// Seconds an application gets to answer one AX request. An app that is beachballing, paused in a
// debugger, or swapped out will not answer at all, and the default behaviour is to wait -- which would
// hang enumeration and therefore the switcher. Skipping a wedged app costs one missing entry;
// waiting on it costs the whole feature.
static const float GT_AX_TIMEOUT_SEC = 0.25f;

static bool ax_bool(AXUIElementRef el, CFStringRef attr, bool fallback) {
    CFTypeRef v = NULL;
    if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return fallback;
    bool r = CFGetTypeID(v) == CFBooleanGetTypeID() ? CFBooleanGetValue((CFBooleanRef)v) : fallback;
    CFRelease(v);
    return r;
}

// Copies a string attribute into dst. Returns the byte length, 0 if the attribute is absent -- which
// for kAXTitleAttribute is a real state: a new untitled document has no title.
static uint16_t ax_string(AXUIElementRef el, CFStringRef attr, char *dst, int cap) {
    CFTypeRef v = NULL;
    if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) {
        if (cap > 0) dst[0] = 0;
        return 0;
    }
    uint16_t n = 0;
    if (CFGetTypeID(v) == CFStringGetTypeID()) n = copy_cfstring((CFStringRef)v, dst, cap);
    else if (cap > 0) dst[0] = 0;
    CFRelease(v);
    return n;
}

gt_status gt_ax_window_list(gt_window *buf, int32_t cap, int32_t *out_n, int32_t *out_total) {
    if (!buf || cap < 0 || !out_n || !out_total) return GT_ERR_INTERNAL;
    *out_n = 0;
    *out_total = 0;

    // Checked before doing any work: ungranted, every call below returns kAXErrorAPIDisabled and the
    // result is an empty list that looks exactly like a machine with no windows open.
    if (!AXIsProcessTrusted()) return GT_ERR_NOT_TRUSTED;

    int32_t stored = 0, total = 0;

    @autoreleasepool {
        // Regular applications only. NSApplicationActivationPolicyAccessory covers menubar-only agents
        // and .prohibited covers XPC services -- between them, most of the 52 entries D19 found were
        // noise. Filtering here is cheaper than filtering their windows later.
        //
        // KNOWN MISS, measured (D20): an .Accessory application can own a perfectly ordinary titled
        // window -- Notion Calendar does. Removing this filter would not recover it, because AX
        // reports zero windows for that process anyway, so the filter is kept and the gap is recorded
        // rather than papered over. P2.3c's join against CGWindowList is what finds these.
        for (NSRunningApplication *app in [[NSWorkspace sharedWorkspace] runningApplications]) {
            if (app.activationPolicy != NSApplicationActivationPolicyRegular) continue;
            pid_t pid = app.processIdentifier;
            if (pid <= 0) continue;

            AXUIElementRef appEl = AXUIElementCreateApplication(pid);
            if (!appEl) continue;
            AXUIElementSetMessagingTimeout(appEl, GT_AX_TIMEOUT_SEC);

            CFTypeRef windowsVal = NULL;
            AXError err = AXUIElementCopyAttributeValue(appEl, kAXWindowsAttribute, &windowsVal);
            if (err != kAXErrorSuccess || !windowsVal) {
                // kAXErrorCannotComplete is the timeout, and it is the expected outcome for an app
                // that is not answering. Not an error for the caller: one app's silence must not
                // fail everyone else's enumeration.
                if (windowsVal) CFRelease(windowsVal);
                CFRelease(appEl);
                continue;
            }

            char appName[GT_APPNAME_MAX];
            uint16_t appLen = copy_cfstring((__bridge CFStringRef)app.localizedName,
                                            appName, GT_APPNAME_MAX);
            int32_t appHidden = app.isHidden ? 1 : 0;

            CFArrayRef windows = (CFArrayRef)windowsVal;
            CFIndex wcount = CFArrayGetCount(windows);
            for (CFIndex i = 0; i < wcount; i++) {
                AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
                if (!win) continue;

                CGWindowID wid = 0;
                if (_AXUIElementGetWindow(win, &wid) != kAXErrorSuccess || wid == 0) {
                    // No CGWindowID means nothing downstream can key it: not the model, not the
                    // thumbnail cache, not a raise. Dropping it is the only honest option.
                    continue;
                }

                total++;
                if (stored >= cap) continue;

                gt_window *w = &buf[stored];
                memset(w, 0, sizeof(*w));
                w->id = (uint32_t)wid;
                w->pid = (int32_t)pid;
                w->hidden = appHidden;
                w->minimized = ax_bool(win, kAXMinimizedAttribute, false) ? 1 : 0;
                w->title_len = ax_string(win, kAXTitleAttribute, w->title, GT_TITLE_MAX);
                w->app_len = appLen;
                memcpy(w->app, appName, GT_APPNAME_MAX);

                char subrole[64];
                uint16_t sn = ax_string(win, kAXSubroleAttribute, subrole, sizeof(subrole));
                w->standard = (sn > 0 && strcmp(subrole, "AXStandardWindow") == 0) ? 1 : 0;

                stored++;
            }

            CFRelease(windowsVal);
            CFRelease(appEl);
        }
    }

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
