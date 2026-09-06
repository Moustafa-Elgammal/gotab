// Spike P0.1: the NSPanel bridge.
//
// A switcher panel has to appear over everything, on whatever Space is current, without taking focus
// from the app the user is leaving. That is four separate AppKit settings and getting any of them
// wrong is invisible until you try it in the situation it breaks.
//
// Three things in here are load-bearing and look removable:
//
//   - NSWindowStyleMaskNonactivatingPanel plus orderFrontRegardless. Without the style mask the panel
//     takes key status and the app behind it deactivates, which is precisely what a switcher must not
//     do. Without orderFrontRegardless a process with Accessory activation policy may not show at all.
//   - The collectionBehavior triple. canJoinAllSpaces makes the panel follow the user rather than
//     living on the Space it was created on; fullScreenAuxiliary is what lets it appear over a
//     full-screen app instead of behind it. A switcher that vanishes in full-screen is not a switcher.
//   - The nested run loop in panel_cycle. AppKit only commits a frame at the end of a runloop turn
//     (ALTTAB-LESSONS section 5), so a measurement that does not pump the loop measures nothing but
//     the cost of asking. This is a spike; the real app runs the loop normally and never nests.
//
// Compiled without ARC (cgo does not pass -fobjc-arc), so retain/release here is manual.

#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include <mach/mach_time.h>
#include "panel.h"

static NSPanel *g_panel = nil;
static NSInteger g_tiles = 8;
static pid_t g_front_pid_at_summon = 0;

// Set at the top of each summon so drawRect: can timestamp itself against it. A measurement that
// cannot tell "the frame was re-rendered" from "the window was re-ordered over a cached backing
// store" is not measuring what it claims to.
static uint64_t g_summon_t0 = 0;
static double g_draw_ms = 0;
static int32_t g_draws = 0;

// ---------------------------------------------------------------------------------------------
// Timing. mach_absolute_time is the monotonic clock that does not jump; the conversion factor is
// fixed for the life of the process, so it is read once.
static double g_ns_per_tick = 0;
static uint64_t now_ticks(void) { return mach_absolute_time(); }
static double ms_since(uint64_t t0) {
    if (g_ns_per_tick == 0) {
        mach_timebase_info_data_t tb;
        mach_timebase_info(&tb);
        g_ns_per_tick = (double)tb.numer / (double)tb.denom;
    }
    return (double)(now_ticks() - t0) * g_ns_per_tick / 1e6;
}

// ---------------------------------------------------------------------------------------------
// One view draws every tile. P3.1's design: AltTab has 53 NSView subclasses and pays a C->Go callback
// for each; measuring an empty window here would flatter a shape we are not going to build.
@interface TileStrip : NSView
@end

@implementation TileStrip

- (BOOL)isFlipped { return YES; }

- (void)drawRect:(NSRect)dirty {
    (void)dirty;
    if (g_summon_t0 != 0) { g_draw_ms = ms_since(g_summon_t0); g_draws++; }
    NSRect b = [self bounds];

    // Panel background: a rounded dark slab, drawn by us because the window is borderless.
    NSBezierPath *bg = [NSBezierPath bezierPathWithRoundedRect:b xRadius:12 yRadius:12];
    [[NSColor colorWithCalibratedWhite:0.12 alpha:0.92] setFill];
    [bg fill];

    CGFloat pad = 16, gap = 10;
    CGFloat tw = (b.size.width - pad * 2 - gap * (g_tiles - 1)) / g_tiles;
    CGFloat th = b.size.height - pad * 2;

    NSDictionary *attrs = @{
        NSFontAttributeName: [NSFont systemFontOfSize:13 weight:NSFontWeightMedium],
        NSForegroundColorAttributeName: [NSColor colorWithCalibratedWhite:0.85 alpha:1.0],
    };

    for (NSInteger i = 0; i < g_tiles; i++) {
        NSRect t = NSMakeRect(pad + i * (tw + gap), pad, tw, th);
        NSBezierPath *p = [NSBezierPath bezierPathWithRoundedRect:t xRadius:8 yRadius:8];

        // The selected tile, so the panel looks like the thing it is standing in for.
        if (i == 1) {
            [[NSColor colorWithCalibratedRed:0.25 green:0.45 blue:0.85 alpha:0.9] setFill];
        } else {
            [[NSColor colorWithCalibratedWhite:0.25 alpha:1.0] setFill];
        }
        [p fill];

        NSString *label = [NSString stringWithFormat:@"%ld", (long)i + 1];
        NSSize sz = [label sizeWithAttributes:attrs];
        [label drawAtPoint:NSMakePoint(NSMidX(t) - sz.width / 2, NSMidY(t) - sz.height / 2)
            withAttributes:attrs];
    }
}
@end

// ---------------------------------------------------------------------------------------------

int32_t panel_init(int32_t tiles) {
    g_tiles = tiles > 0 ? tiles : 8;

    NSApplication *app = [NSApplication sharedApplication];
    // Accessory, not Regular: no Dock icon and no menu bar for a thing that is only ever a panel.
    // It also means the process never becomes frontmost on its own, which is the behaviour we want.
    [app setActivationPolicy:NSApplicationActivationPolicyAccessory];

    NSScreen *screen = [NSScreen mainScreen];
    if (!screen) return PANEL_NO_SCREEN;

    NSRect vf = [screen visibleFrame];
    CGFloat w = fmin(940, vf.size.width - 80);
    CGFloat h = 150;
    NSRect frame = NSMakeRect(NSMidX(vf) - w / 2, NSMidY(vf) - h / 2, w, h);

    g_panel = [[NSPanel alloc]
        initWithContentRect:frame
                  styleMask:(NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel)
                    backing:NSBackingStoreBuffered
                      defer:NO];

    [g_panel setOpaque:NO];
    [g_panel setBackgroundColor:[NSColor clearColor]];
    [g_panel setHasShadow:YES];
    [g_panel setLevel:NSPopUpMenuWindowLevel];   // above normal and floating windows
    [g_panel setHidesOnDeactivate:NO];
    [g_panel setCollectionBehavior:(NSWindowCollectionBehaviorCanJoinAllSpaces |
                                    NSWindowCollectionBehaviorFullScreenAuxiliary |
                                    NSWindowCollectionBehaviorStationary)];
    // A switcher must never become the key window: the app being switched away from has to stay
    // active until the user actually picks something.
    [g_panel setBecomesKeyOnlyIfNeeded:YES];

    TileStrip *view = [[TileStrip alloc] initWithFrame:frame];
    [view setWantsLayer:YES];
    [g_panel setContentView:view];
    [view release];

    return PANEL_OK;
}

// Pumps the main run loop until *flag is set or the deadline passes.
static BOOL pump_until(volatile BOOL *flag, double timeout_ms, uint64_t t0) {
    while (!*flag) {
        if (ms_since(t0) > timeout_ms) return NO;
        [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode
                                 beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.001]];
    }
    return YES;
}

void panel_cycle(int32_t timeout_ms, int32_t hold_ms, panel_sample *out) {
    memset(out, 0, sizeof(*out));
    if (!g_panel) { out->err = PANEL_NO_SCREEN; return; }

    NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
    g_front_pid_at_summon = front ? [front processIdentifier] : 0;

    __block volatile BOOL committed = NO;
    __block volatile BOOL turned = NO;
    __block double commit_ms = 0, turn_ms = 0;

    uint64_t t0 = now_ticks();
    g_summon_t0 = t0;
    g_draw_ms = 0;
    g_draws = 0;

    // The completion block fires when this transaction is handed to the render server. That is the
    // closest thing to "pixels" reachable without a display link, and it is the honest number.
    [CATransaction begin];
    [CATransaction setCompletionBlock:^{
        commit_ms = ms_since(t0);
        committed = YES;
    }];

    [[g_panel contentView] setNeedsDisplay:YES];
    [g_panel orderFrontRegardless];

    [CATransaction commit];

    out->call_ms = ms_since(t0);

    // The following main-queue turn: by here CoreAnimation has certainly committed, so this is the
    // conservative upper bound on the same event.
    dispatch_async(dispatch_get_main_queue(), ^{
        turn_ms = ms_since(t0);
        turned = YES;
    });

    BOOL ok = pump_until(&committed, timeout_ms, t0);
    pump_until(&turned, timeout_ms, t0);

    out->commit_ms = commit_ms;
    out->turn_ms = turn_ms;
    out->draw_ms = g_draw_ms;
    out->draws = g_draws;
    out->err = ok ? PANEL_OK : PANEL_TIMEOUT;

    if (hold_ms > 0) {
        uint64_t th = now_ticks();
        volatile BOOL never = NO;
        pump_until(&never, hold_ms, th);
    }

    [g_panel orderOut:nil];
    // Give the dismissal its own turn, so the next cycle starts from a settled state rather than
    // measuring the tail of this one.
    __block volatile BOOL settled = NO;
    dispatch_async(dispatch_get_main_queue(), ^{ settled = YES; });
    uint64_t t2 = now_ticks();
    pump_until(&settled, 200, t2);
}

void panel_show_for(int32_t ms) {
    if (!g_panel) return;
    [[g_panel contentView] setNeedsDisplay:YES];
    [g_panel orderFrontRegardless];
    uint64_t t0 = now_ticks();
    volatile BOOL never = NO;
    pump_until(&never, ms, t0);
    [g_panel orderOut:nil];
}

int32_t panel_stole_focus(void) {
    NSRunningApplication *front = [[NSWorkspace sharedWorkspace] frontmostApplication];
    pid_t now_front = front ? [front processIdentifier] : 0;
    if (g_front_pid_at_summon == 0) return 0;
    return now_front != g_front_pid_at_summon;
}
