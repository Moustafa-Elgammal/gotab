// The AppKit main run loop. Compiled without ARC like the rest of this package.
#import <Cocoa/Cocoa.h>
#include "runloop.h"

// Set on the main thread inside gt_run_loop, cleared when -[NSApp run] returns. gt_run_loop_stop reads
// it (on the main thread, via the dispatch below) so a stop with no loop running is a clean no-op and
// a stale stop cannot ambush a later gt_run_loop.
static BOOL g_running = NO;

void gt_run_loop(void) {
    NSApplication *app = [NSApplication sharedApplication];
    // -[NSApp run] rather than CFRunLoopRun(): the process must keep *dequeuing* AppKit events, not
    // just turning the run loop. Two things depend on it that a bare CFRunLoopRun() does not give:
    //   1. The system's app-responsiveness check. macOS marks a process "Not Responding" when its
    //      main thread stops servicing the event queue; CFRunLoopRun() turns the loop (OnMain,
    //      NSWorkspace notifications, CA commits all still work) but never calls
    //      nextEventMatchingMask:, so the WindowServer's ping goes unanswered. (D54)
    //   2. The menu-bar status item (P8.1). A click on an NSStatusItem is a mouse event dispatched
    //      through -[NSApp sendEvent:], which only runs inside -[NSApp run]'s loop. Under
    //      CFRunLoopRun() the click is never delivered and the menu never opens.
    // -run calls -finishLaunching itself and installs a per-iteration autorelease pool, so neither is
    // needed here. gt_panel_create / gt_settings_open already created the shared app and set the
    // activation policy. libdispatch's main-queue source, the NSWorkspace notification source and the
    // CA transaction observer are all on this same loop and are still serviced.
    g_running = YES;
    [app run];
    g_running = NO;
}

void gt_run_loop_stop(void) {
    // -[NSApplication stop:] and -postEvent:atStart: are not thread-safe and this is called from a Go
    // shutdown goroutine, so hop to the main thread. The main queue is serviced by -[NSApp run], so
    // the block runs on the next turn.
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!g_running) return; // nothing to stop; also guards a stop queued before the loop starts
        [NSApp stop:nil];
        // -stop: only takes effect after -run processes one more event, and with an empty queue -run
        // sits in nextEventMatchingMask: on distantFuture. Post a no-op event to force that turn.
        NSEvent *nudge = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
                                            location:NSMakePoint(0, 0)
                                       modifierFlags:0
                                           timestamp:0
                                        windowNumber:0
                                             context:nil
                                             subtype:0
                                               data1:0
                                               data2:0];
        [NSApp postEvent:nudge atStart:YES];
    });
}
