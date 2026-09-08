// Permissions onboarding. Compiled without ARC; the one alloc (NSAlert) is released.
//
// The recovery model (V6.9): explain, then send the user to System Settings and let them toggle the
// grant there. The caller polls gt_trusted()/gt_can_record() afterwards and proceeds the moment the
// grant appears — no relaunch. macOS itself relaunches some apps on an Accessibility grant; if it
// does, the fresh process just passes the check and never shows this.
//
// User-facing text is resolved with NSLocalizedString from Contents/Resources/<lang>.lproj/
// Localizable.strings (P5.1). The keys mirror internal/i18n/en.json's perm.alert.* set; outside a
// bundle (tests, `go run`) NSLocalizedString returns the key, which this modal never exercises —
// the CLI fallback path uses the Go i18n.T strings instead. NSLocalizedString hands back an
// autoreleased NSString, drained by the enclosing @autoreleasepool.
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

gt_status gt_permissions_prompt(int32_t need_ax, int32_t need_sr, int32_t can_defer) {
    @autoreleasepool {
        // No window server (headless CI, ssh with no forwarding): -[NSAlert runModal] would block
        // forever with nothing able to answer it. Say so and let the caller fall back to text.
        if ([[NSScreen screens] count] == 0) {
            return GT_ERR_INTERNAL;
        }

        [NSApplication sharedApplication];
        // Regular for the modal's lifetime so the alert comes to the front like an app dialog rather
        // than a background beep an Accessory app cannot raise (D52). Restored below: by D55 this can
        // run after gt_panel_create has already set Accessory, and a switcher with a Dock tile is
        // wrong.
        NSApplicationActivationPolicy saved_policy = [NSApp activationPolicy];
        if (saved_policy != NSApplicationActivationPolicyRegular) {
            [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        }

        NSAlert *a = [[NSAlert alloc] init];
        a.messageText = NSLocalizedString(@"perm.alert.title", nil);

        NSMutableString *info = [NSMutableString string];
        if (need_ax) {
            [info appendString:NSLocalizedString(@"perm.alert.needAccessibility", nil)];
        }
        if (need_sr) {
            [info appendString:NSLocalizedString(@"perm.alert.needScreenRecording", nil)];
        }
        [info appendString:NSLocalizedString(@"perm.alert.instructions", nil)];
        a.informativeText = info;

        [a addButtonWithTitle:NSLocalizedString(@"perm.alert.openSettings", nil)];
        [a addButtonWithTitle:NSLocalizedString(
                                  can_defer ? @"perm.alert.notNow" : @"perm.alert.quit", nil)];

        [NSApp activateIgnoringOtherApps:YES];
        [a.window makeKeyAndOrderFront:nil];
        NSModalResponse r = [a runModal];
        [a release];

        if (saved_policy != NSApplicationActivationPolicyRegular) {
            [NSApp setActivationPolicy:saved_policy];
        }

        if (r != NSAlertFirstButtonReturn) {
            return GT_ERR_UNAVAILABLE; // "Not Now" / "Quit"
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
