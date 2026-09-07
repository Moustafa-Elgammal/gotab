// The AppKit main run loop. Compiled without ARC like the rest of this package.
#import <Cocoa/Cocoa.h>
#include "runloop.h"

void gt_run_loop(void) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        // gt_panel_create already set the Accessory activation policy. finishLaunching puts AppKit
        // into the state the rest of the frameworks expect (menu, services, the "app is running"
        // flag) without the event-dispatch machinery of -[NSApp run].
        [app finishLaunching];
    }
    // CFRunLoopRun rather than -[NSApp run]: the panel is ordered in with orderFrontRegardless and
    // never becomes key, so there is no NSApp event-dispatch state depended on, and CFRunLoopStop
    // from another thread breaks this cleanly where -[NSApp run] would wait for the next event.
    // libdispatch's main-queue source, the NSWorkspace notification source, and the CA transaction
    // observer are all attached to this same run loop, so they are all serviced here.
    CFRunLoopRun();
}

void gt_run_loop_stop(void) {
    CFRunLoopRef main = CFRunLoopGetMain();
    if (!main) return;
    CFRunLoopStop(main);
    CFRunLoopWakeUp(main); // in case it is between turns
}
