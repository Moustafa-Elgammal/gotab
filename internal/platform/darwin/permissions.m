// Permissions onboarding. Compiled without ARC; the one alloc (NSAlert) is released.
//
// The recovery model (V6.9): explain, then send the user to System Settings and let them toggle the
// grant there. The caller polls gt_trusted()/gt_can_record() afterwards and proceeds the moment the
// grant appears — no relaunch. macOS itself relaunches some apps on an Accessibility grant; if it
// does, the fresh process just passes the check and never shows this.
#import <Cocoa/Cocoa.h>
#import <CoreGraphics/CoreGraphics.h>
#include <string.h>
#include "permissions.h"

static NSString *pane_url(const char *which) {
    if (which && strcmp(which, "accessibility") == 0) {
        return @"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility";
    }
    if (which && strcmp(which, "screen-recording") == 0) {
        return @"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture";
    }
    return @"x-apple.systempreferences:com.apple.preference.security?Privacy";
}

void gt_permissions_open_pane(const char *which) {
    @autoreleasepool {
        [[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:pane_url(which)]];
    }
}

gt_status gt_permissions_prompt(int32_t need_ax, int32_t need_sr) {
    @autoreleasepool {
        // No window server (headless CI, ssh with no forwarding): -[NSAlert runModal] would block
        // forever with nothing able to answer it. Say so and let the caller fall back to text.
        if ([[NSScreen screens] count] == 0) {
            return GT_ERR_INTERNAL;
        }

        [NSApplication sharedApplication];
        // Regular so the alert comes to the front like an app dialog rather than a background beep.
        if ([NSApp activationPolicy] != NSApplicationActivationPolicyRegular) {
            [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        }

        NSAlert *a = [[NSAlert alloc] init];
        a.messageText = @"GoTab needs permission to switch windows";

        NSMutableString *info = [NSMutableString string];
        if (need_ax) {
            [info appendString:@"• Accessibility — to raise and observe windows, and to see the ⌥⇥ shortcut.\n"];
        }
        if (need_sr) {
            [info appendString:@"• Screen Recording — for window thumbnails and the titles of other apps.\n"];
        }
        [info appendString:@"\nTurn these on in System Settings › Privacy & Security. GoTab notices the change on its own — there is no need to relaunch it."];
        a.informativeText = info;

        [a addButtonWithTitle:@"Open System Settings"];
        [a addButtonWithTitle:@"Quit"];

        [NSApp activateIgnoringOtherApps:YES];
        NSModalResponse r = [a runModal];
        [a release];

        if (r != NSAlertFirstButtonReturn) {
            return GT_ERR_UNAVAILABLE; // the user chose Quit
        }

        // CGRequestScreenCaptureAccess shows its own system dialog and, importantly, registers the
        // app in the Screen Recording list so the user has a row to toggle.
        if (need_sr) {
            CGRequestScreenCaptureAccess();
        }
        gt_permissions_open_pane(need_ax ? "accessibility" : "screen-recording");
        return GT_OK;
    }
}
