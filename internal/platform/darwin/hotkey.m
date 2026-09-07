// The Objective-C side of the switcher's global hotkey.
//
// Compiled WITHOUT ARC like the rest of this package; retain/release is manual.
//
// The tap runs on its own thread, for the reason observe.m's observers do: a keystroke callback must
// not wait behind whatever the main run loop is doing (a panel draw, a CA commit). It is a session
// tap at kCGHeadInsertEventTap, the only position from which the switcher can see Option+Tab before
// the focused app and swallow it. kCGEventTapDisabledByTimeout is handled because the system disables
// a slow tap exactly once, by that event, and a hotkey that dies after one stall is the worst
// outcome available (D22, and P0.2's spike).
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdatomic.h>
#include "hotkey.h"

// Defined in Go (hotkey.go). Called only from the tap thread, and it must return immediately.
extern void goHotkeyGesture(int kind);

// Tab and Escape hardware keycodes. Layout-independent for these two on every Mac keyboard.
enum { GT_KEY_TAB = 48, GT_KEY_ESC = 53 };

static _Atomic(CFRunLoopRef) g_loop = NULL;
static atomic_bool g_active = false;
static atomic_bool g_stopping = false;
static dispatch_semaphore_t g_ready = NULL;
static dispatch_semaphore_t g_done = NULL;

static CFMachPortRef g_tap = NULL;
static CFRunLoopSourceRef g_src = NULL;

// Tap-thread-only, so unguarded. g_armed: at least one Tab has been seen since Option went down, so
// releasing Option should commit the selection rather than be ignored.
static int g_armed = 0;
static BOOL g_option_was_down = NO;

static CGEventRef on_event(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *ctx) {
    (void)proxy;
    (void)ctx;

    // The system disabled us for being slow (or a fast user switch did). Turn the tap back on and let
    // the event through; the callback body below is a bounded, non-blocking hop, so this should not
    // recur, but silence here would be a dead hotkey.
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (g_tap) CGEventTapEnable(g_tap, true);
        return event;
    }

    CGEventFlags flags = CGEventGetFlags(event);
    BOOL option = (flags & kCGEventFlagMaskAlternate) != 0;
    BOOL shift = (flags & kCGEventFlagMaskShift) != 0;

    if (type == kCGEventFlagsChanged) {
        if (g_option_was_down && !option && g_armed) {
            goHotkeyGesture(GT_HK_ACTIVATE);
            g_armed = 0;
        }
        g_option_was_down = option;
        return event; // never swallow a modifier change: other apps track Option state too
    }

    if (type == kCGEventKeyDown) {
        uint32_t code = (uint32_t)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);

        if (code == GT_KEY_TAB && option) {
            // Autorepeat from a held Tab fires ~15x/s — too fast to cycle on, and a user who wants to
            // spin can tap. Act on the real presses only.
            if (CGEventGetIntegerValueField(event, kCGKeyboardEventAutorepeat)) return NULL;
            int kind;
            if (!g_armed) kind = shift ? GT_HK_SUMMON_BWD : GT_HK_SUMMON_FWD;
            else kind = shift ? GT_HK_CYCLE_BWD : GT_HK_CYCLE_FWD;
            g_armed = 1;
            goHotkeyGesture(kind);
            return NULL; // swallow: the focused app must never get the switcher's chord
        }

        if (code == GT_KEY_ESC && g_armed) {
            goHotkeyGesture(GT_HK_DISMISS);
            g_armed = 0;
            return NULL;
        }
        return event;
    }

    if (type == kCGEventKeyUp) {
        uint32_t code = (uint32_t)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        // Swallow the key-up of a Tab whose key-down we swallowed, so the app underneath never gets
        // an orphan key-up.
        if (code == GT_KEY_TAB && g_armed) return NULL;
        return event;
    }

    return event;
}

static void hotkey_thread_main(void) {
    @autoreleasepool {
        CFRunLoopRef loop = CFRunLoopGetCurrent();
        CFRetain(loop);
        atomic_store(&g_loop, loop);

        CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) | CGEventMaskBit(kCGEventKeyUp) |
                           CGEventMaskBit(kCGEventFlagsChanged);
        g_tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                                 kCGEventTapOptionDefault, mask, on_event, NULL);
        if (g_tap) {
            g_src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, g_tap, 0);
            CFRunLoopAddSource(loop, g_src, kCFRunLoopCommonModes);
            CGEventTapEnable(g_tap, true);
        }
        // Signalled after the tap exists (or failed to), so gt_hotkey_start can tell the difference.
        dispatch_semaphore_signal(g_ready);

        if (g_tap) {
            while (!atomic_load(&g_stopping)) {
                @autoreleasepool {
                    CFRunLoopRun();
                }
            }
        }

        if (g_src) {
            CFRunLoopRemoveSource(loop, g_src, kCFRunLoopCommonModes);
            CFRelease(g_src);
            g_src = NULL;
        }
        if (g_tap) {
            CGEventTapEnable(g_tap, false);
            CFRelease(g_tap);
            g_tap = NULL;
        }
        CFRelease(loop);
        dispatch_semaphore_signal(g_done);
    }
}

gt_status gt_hotkey_start(void) {
    if (!AXIsProcessTrusted()) return GT_ERR_NOT_TRUSTED;
    if (atomic_exchange(&g_active, true)) return GT_OK; // already running

    atomic_store(&g_stopping, false);
    g_armed = 0;
    g_option_was_down = NO;
    g_ready = dispatch_semaphore_create(0);
    g_done = dispatch_semaphore_create(0);

    [NSThread detachNewThreadWithBlock:^{
        [[NSThread currentThread] setName:@"gotab.hotkey"];
        hotkey_thread_main();
    }];

    dispatch_semaphore_wait(g_ready, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));
    if (!g_tap) {
        atomic_store(&g_stopping, true);
        CFRunLoopRef loop = atomic_load(&g_loop);
        if (loop) {
            CFRunLoopStop(loop);
            CFRunLoopWakeUp(loop);
        }
        if (g_done) dispatch_semaphore_wait(g_done, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));
        atomic_store(&g_loop, NULL);
        atomic_store(&g_active, false);
        return GT_ERR_INTERNAL; // tap thread started but CGEventTapCreate returned NULL
    }
    return GT_OK;
}

void gt_hotkey_stop(void) {
    if (!atomic_exchange(&g_active, false)) return; // not running

    atomic_store(&g_stopping, true);
    CFRunLoopRef loop = atomic_load(&g_loop);
    atomic_store(&g_loop, NULL);
    if (loop) {
        CFRunLoopStop(loop);
        CFRunLoopWakeUp(loop);
    }
    if (g_done) {
        dispatch_semaphore_wait(g_done, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));
    }
}
