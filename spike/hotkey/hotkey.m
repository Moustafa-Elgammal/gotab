// Spike P0.2: the CGEventTap bridge.
//
// A switcher's hotkey has to be seen before the frontmost app sees it, and has to be swallowed so the
// app never gets it at all. That is what a session-level event tap inserted at the head does.
//
// Three things in here are load-bearing and look removable:
//
//   - The kCGEventTapDisabledByTimeout branch. macOS disables a tap whose callback is too slow and
//     tells you exactly once, by sending that event type. Without the re-enable, the hotkey dies
//     silently after a single stall and every later press does nothing. It is the documented failure
//     mode, not defensive noise.
//   - Reading CGEventGetTimestamp rather than only timing our own code. It is the moment the event
//     entered the system, in the same mach timebase as mach_absolute_time, so "how late are we"
//     is measurable directly instead of inferred from when we happened to look.
//   - The Go round trip measured *inside* the callback. The tap fires on a CoreFoundation thread the
//     Go runtime has never seen; attaching one is not the 39 ns D1 measured on a thread it owns.
//
// Compiled without ARC (cgo does not pass -fobjc-arc), so retain/release here is manual.

#import <Foundation/Foundation.h>
#import <ApplicationServices/ApplicationServices.h>
#include <mach/mach_time.h>
#include "hotkey.h"

// Defined in Go, called from the tap callback. Deliberately trivial: this measures the crossing, so
// anything it did would be measured too.
extern void goHotkeyEntered(void);

static CFMachPortRef g_tap = NULL;
static CFRunLoopSourceRef g_src = NULL;
static uint32_t g_keycode = 48;                    // Tab
static uint64_t g_flags = kCGEventFlagMaskAlternate; // Option
static int64_t g_seen = 0, g_swallowed = 0;

static volatile BOOL g_have = NO;
static hk_sample g_last;
static int32_t g_pending_reenable = 0;

static double g_ns_per_tick = 0;
static void init_timebase(void) {
    if (g_ns_per_tick == 0) {
        mach_timebase_info_data_t tb;
        mach_timebase_info(&tb);
        g_ns_per_tick = (double)tb.numer / (double)tb.denom;
    }
}
static double ticks_to_ms(int64_t d) {
    init_timebase();
    return (double)d * g_ns_per_tick / 1e6;
}

// mach_absolute_time() counts TICKS, and a tick is not a nanosecond: the timebase is 125/3 on Apple
// Silicon, so a tick is 41.667 ns. CGEventGetTimestamp() hands back NANOSECONDS. Subtracting one
// from the other produced a delivery latency of -12.7 days that drifted by ~40,667 ms per second of
// uptime - the exact signature of the 41.667x mismatch. Everything is converted to ns before any
// comparison now. On Intel the timebase is 1/1, so this is also correct there rather than merely
// harmless.
static uint64_t mach_now_ns(void) {
    init_timebase();
    return (uint64_t)((double)mach_absolute_time() * g_ns_per_tick);
}

int32_t hk_trusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

static CGEventRef on_event(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *ctx) {
    (void)proxy; (void)ctx;

    // The system disabled us. Say so, turn the tap back on, and pass the event through.
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (g_tap) CGEventTapEnable(g_tap, true);
        g_pending_reenable = 1;
        return event;
    }

    g_seen++;

    if (type != kCGEventKeyDown) return event;

    uint64_t t_cb_ns = mach_now_ns();
    uint32_t code = (uint32_t)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
    CGEventFlags flags = CGEventGetFlags(event);

    if (code != g_keycode || (flags & g_flags) != g_flags) return event;

    // Nanoseconds since boot - NOT the same units as mach_absolute_time's ticks. See mach_now_ns.
    uint64_t t_event_ns = CGEventGetTimestamp(event);

    // Baseline first: the same pair of clock reads around nothing. Subtracting it separates the
    // crossing from the cost of asking what time it is.
    uint64_t b0 = mach_absolute_time();
    uint64_t b1 = mach_absolute_time();

    uint64_t t0 = mach_absolute_time();
    goHotkeyEntered();
    uint64_t t1 = mach_absolute_time();

    // Signed: a synthetic event's timestamp is not on the HID path and can precede or follow t_cb.
    g_last.deliver_ms = (double)((int64_t)t_cb_ns - (int64_t)t_event_ns) / 1e6;
    g_last.raw_event_ns = t_event_ns;
    g_last.raw_cb_ns = t_cb_ns;
    g_last.baseline_us = ticks_to_ms((int64_t)b1 - (int64_t)b0) * 1000.0;
    g_last.synthetic = (CGEventGetIntegerValueField(event, kCGEventSourceStateID)
                        == kCGEventSourceStateHIDSystemState) ? 0 : 1;
    g_last.roundtrip_us = ticks_to_ms(t1 - t0) * 1000.0;
    g_last.keycode = code;
    g_last.flags = (uint64_t)flags;
    g_last.reenabled = g_pending_reenable;
    g_pending_reenable = 0;
    g_have = YES;
    g_swallowed++;

    // Swallow it: the app underneath must never see the switcher's own hotkey.
    return NULL;
}

int32_t hk_start(uint32_t keycode, uint64_t flags, char *msg, int32_t msg_len) {
    g_keycode = keycode;
    g_flags = flags;

    if (!AXIsProcessTrusted()) {
        if (msg && msg_len > 0)
            strncpy(msg, "Accessibility not granted to the responsible process", msg_len - 1);
        return HK_NOT_TRUSTED;
    }

    // Session tap at the head of the queue: we see the event before the focused application, which
    // is the only position from which it can be swallowed.
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) | CGEventMaskBit(kCGEventKeyUp);
    g_tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                             kCGEventTapOptionDefault, mask, on_event, NULL);
    if (!g_tap) {
        if (msg && msg_len > 0) strncpy(msg, "CGEventTapCreate returned NULL", msg_len - 1);
        return HK_TAP_FAILED;
    }

    g_src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, g_tap, 0);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), g_src, kCFRunLoopCommonModes);
    CGEventTapEnable(g_tap, true);
    return HK_OK;
}

int32_t hk_wait(int32_t timeout_ms, hk_sample *out) {
    memset(out, 0, sizeof(*out));
    g_have = NO;
    uint64_t t0 = mach_absolute_time();

    while (!g_have) {
        if (ticks_to_ms((int64_t)(mach_absolute_time() - t0)) > (double)timeout_ms) return HK_TIMEOUT;
        CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.002, true);
    }
    *out = g_last;
    return HK_OK;
}

void hk_post(uint32_t keycode, uint64_t flags) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, true);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, false);
    CGEventSetFlags(down, (CGEventFlags)flags);
    CGEventSetFlags(up, (CGEventFlags)flags);
    // Stamp it ourselves, in mach_absolute_time, immediately before posting. A posted event's own
    // timestamp is not comparable to our clock (it read as -12 days), so without this the delivery
    // figure is garbage. With it, the figure is real but means something narrower than the budget:
    // post -> tap routing through the event system, excluding hardware, driver and HID entirely.
    CGEventSetTimestamp(down, mach_now_ns());
    CGEventPost(kCGSessionEventTap, down);
    CGEventPost(kCGSessionEventTap, up);
    CFRelease(down);
    CFRelease(up);
}

int64_t hk_seen(void) { return g_seen; }
int64_t hk_swallowed(void) { return g_swallowed; }
