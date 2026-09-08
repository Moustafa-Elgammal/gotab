// The write half of the platform layer. Compiled WITHOUT ARC, like shim.m -- cgo does not pass
// -fobjc-arc, and the CoreFoundation objects this file spends its time on are outside ARC's remit
// anyway. Every CFRetain here has a visible CFRelease.
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <CoreGraphics/CoreGraphics.h>
#include "action.h"

// PRIVATE API. Declared again here rather than shared with shim.m: shim.m is frozen and exports no
// header of its own, and a duplicate extern declaration of the same symbol is free. See the long
// comment above shim.m's copy for why the dependency is worth taking -- Accessibility exposes no
// public way to get from an AXUIElement to the CGWindowID everything else in this project is keyed by.
extern AXError _AXUIElementGetWindow(AXUIElementRef element, CGWindowID *out);

// Deliberately the same number as shim.m's GT_AX_TIMEOUT_SEC, and deliberately a second copy of it:
// that one is static to a frozen file. If they ever need to differ it will be because an action's
// budget is not an enumeration's, and that is a decision to record rather than a constant to share.
//
// The consequence to know about: the fallback scan below can pay this once per application, so a
// machine full of wedged apps makes a failed lookup slow. It cannot make a successful one slow --
// the common path asks CoreGraphics who owns the window and talks to exactly that process.
static const float GT_ACTION_TIMEOUT_SEC = 0.25f;

// One AXError becomes one gt_status, in one place.
//
// kAXErrorCannotComplete is the messaging timeout and also the generic "the app did not answer", which
// is the same thing from the caller's side: the process is not responding. kAXErrorInvalidUIElement
// means the element refers to a window that no longer exists -- the race this whole file is written
// around. Unsupported attribute/action is not a failure at all: it is a window saying it has no such
// gesture, which GT_ERR_UNAVAILABLE already means.
static gt_status status_for_ax(AXError err) {
    switch (err) {
        case kAXErrorSuccess:              return GT_OK;
        case kAXErrorAPIDisabled:          return GT_ERR_NOT_TRUSTED;
        case kAXErrorCannotComplete:       return GT_ERR_TIMEOUT;
        case kAXErrorInvalidUIElement:
        case kAXErrorInvalidUIElementObserver: return GT_ERR_NO_WINDOW;
        case kAXErrorAttributeUnsupported:
        case kAXErrorActionUnsupported:
        case kAXErrorNotImplemented:
        case kAXErrorNoValue:              return GT_ERR_UNAVAILABLE;
        default:                           return GT_ERR_INTERNAL;
    }
}

// The owning process of a window id, or 0.
//
// kCGWindowListOptionIncludingWindow, not a scan of the whole list: this asks the WindowServer about
// one window and gets back at most one dictionary. It is also the cheapest possible existence check --
// an empty result means the window is gone, which is the answer this file needs most often.
// OnScreenOnly is not passed, so a minimized window still resolves.
static pid_t pid_for_window(CGWindowID wid) {
    CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, wid);
    if (!list) return 0;
    pid_t pid = 0;
    if (CFArrayGetCount(list) > 0) {
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, 0);
        CFNumberRef n = d ? (CFNumberRef)CFDictionaryGetValue(d, kCGWindowOwnerPID) : NULL;
        int32_t v = 0;
        if (n && CFNumberGetValue(n, kCFNumberSInt32Type, &v)) pid = (pid_t)v;
    }
    CFRelease(list);
    return pid;
}

// Searches one application's AX window list for wid. Returns a RETAINED element, or NULL, and reports
// through *out_err why it found nothing so the caller can tell "this app does not own it" from "this
// app is not answering".
static AXUIElementRef copy_window_in_app(pid_t pid, CGWindowID wid, AXError *out_err) {
    *out_err = kAXErrorSuccess;
    if (pid <= 0) return NULL;

    AXUIElementRef appEl = AXUIElementCreateApplication(pid);
    if (!appEl) return NULL;
    // Same reason as enumeration: an app that is beachballing or paused in a debugger will not answer,
    // and the default is to wait forever. A switcher that hangs on a wedged app is worse than one that
    // reports it could not raise the window.
    AXUIElementSetMessagingTimeout(appEl, GT_ACTION_TIMEOUT_SEC);

    CFTypeRef val = NULL;
    AXError err = AXUIElementCopyAttributeValue(appEl, kAXWindowsAttribute, &val);
    CFRelease(appEl);
    if (err != kAXErrorSuccess || !val) {
        if (val) CFRelease(val);
        *out_err = err;
        return NULL;
    }

    AXUIElementRef found = NULL;
    CFArrayRef windows = (CFArrayRef)val;
    CFIndex n = CFArrayGetCount(windows);
    for (CFIndex i = 0; i < n; i++) {
        AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
        if (!win) continue;
        CGWindowID got = 0;
        if (_AXUIElementGetWindow(win, &got) == kAXErrorSuccess && got == wid) {
            found = (AXUIElementRef)CFRetain(win);
            break;
        }
    }
    CFRelease(val);
    if (found) AXUIElementSetMessagingTimeout(found, GT_ACTION_TIMEOUT_SEC);
    return found;
}

// Resolves a CGWindowID to a RETAINED AXUIElement, and to the pid that owns it.
//
// Two passes, because neither enumeration is complete (D20). The first asks CoreGraphics who owns the
// window and searches only that process, which is one AX round trip and the case that happens. The
// second is for a window Accessibility knows about and the WindowServer does not -- window.go calls
// that OriginAXOnly -- and costs one round trip per regular application, so it runs only when the
// cheap answer came back empty.
static AXUIElementRef copy_window_element(CGWindowID wid, pid_t *out_pid, gt_status *out_status) {
    *out_pid = 0;
    *out_status = GT_ERR_NO_WINDOW;

    pid_t owner = pid_for_window(wid);
    // Hand back the owning pid even when no element resolves. A cg-only window (window.go's
    // OriginCGOnly) has no AXUIElement to return here, but gt_window_raise's P7.1 fallback still needs
    // to know whose application to bring forward. A hit on the all-applications scan below overwrites
    // this with the pid that actually answered.
    *out_pid = owner;
    if (owner > 0) {
        AXError err = kAXErrorSuccess;
        AXUIElementRef win = copy_window_in_app(owner, wid, &err);
        if (win) {
            *out_pid = owner;
            *out_status = GT_OK;
            return win;
        }
        if (err != kAXErrorSuccess) {
            // The owner is known and did not answer. Scanning every other application would only
            // repeat the same silence in a slower way.
            *out_status = status_for_ax(err);
            return NULL;
        }
    }

    @autoreleasepool {
        for (NSRunningApplication *app in [[NSWorkspace sharedWorkspace] runningApplications]) {
            if (app.activationPolicy != NSApplicationActivationPolicyRegular) continue;
            pid_t pid = app.processIdentifier;
            if (pid <= 0 || pid == owner) continue;
            AXError err = kAXErrorSuccess;
            AXUIElementRef win = copy_window_in_app(pid, wid, &err);
            if (win) {
                *out_pid = pid;
                *out_status = GT_OK;
                return win;
            }
        }
    }
    return NULL;  // *out_status is still GT_ERR_NO_WINDOW: nobody owns this id.
}

static bool window_is_minimized(AXUIElementRef win) {
    CFTypeRef v = NULL;
    if (AXUIElementCopyAttributeValue(win, kAXMinimizedAttribute, &v) != kAXErrorSuccess || !v) {
        return false;
    }
    bool r = CFGetTypeID(v) == CFBooleanGetTypeID() && CFBooleanGetValue((CFBooleanRef)v);
    CFRelease(v);
    return r;
}

static AXError set_minimized(AXUIElementRef win, bool value) {
    return AXUIElementSetAttributeValue(win, kAXMinimizedAttribute,
                                        value ? kCFBooleanTrue : kCFBooleanFalse);
}

// Makes the application frontmost.
//
// The options are chosen at runtime, and the reason is a deprecation with teeth.
// NSApplicationActivateIgnoringOtherApps is what made activation reliable when some other app was
// frontmost -- which, for a switcher, is every single invocation -- and macOS 14 both deprecated it
// and made it do nothing. Below 14 it is still needed, and the deployment target is 12.0 (D17), so
// both branches are live code and the deprecation warning is silenced around the one line that earns
// it rather than file-wide.
//
// NSApplicationActivateAllWindows is deliberately never passed: it brings every window of the
// application forward and undoes the ordering the raise just established.
static void activate_app(pid_t pid) {
    @autoreleasepool {
        NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if (!app) return;
        // A hidden application (Cmd-H) keeps its windows out of sight no matter what is raised inside
        // it, so unhiding is part of activating and not a separate feature.
        if (app.isHidden) [app unhide];

        NSApplicationActivationOptions opts = 0;
        if (@available(macOS 14.0, *)) {
            // 0 is exactly what ignoringOtherApps degraded to here.
        } else {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
            opts = NSApplicationActivateIgnoringOtherApps;
#pragma clang diagnostic pop
        }
        [app activateWithOptions:opts];
    }
}

// P7.1 (D46): is pid a running application we could still bring forward? A cg-only window
// (window.go's OriginCGOnly) has no AXUIElement on the current Space, so gt_window_raise cannot order
// the specific window -- but if the owning application is alive, activating it puts the user where
// they were headed, which beats returning "no such window" for a tile the enumeration join
// deliberately offered. Once the process is gone this returns false and the caller reports
// GT_ERR_NO_WINDOW so P7.2 can prune the entry.
//
// runningApplicationWithProcessIdentifier: returns nil for a pid that is not a live GUI application
// -- terminated, or never one -- which is exactly the line wanted. kill(pid, 0) would also spot a
// live pid but not whether activate_app could do anything with it.
static bool app_is_alive(pid_t pid) {
    if (pid <= 0) return false;
    @autoreleasepool {
        NSRunningApplication *app =
            [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        return app != nil && !app.isTerminated;
    }
}

// Every entry point has the same shape: refuse early without the grant, resolve the id to an element,
// act, release. Written out rather than hidden behind a macro because there are four of them and the
// middle of each one differs.
static gt_status guard(void) {
    // Without the grant every AX call below returns kAXErrorAPIDisabled and the action silently does
    // nothing. "Not allowed" and "did not work" are different answers to the user.
    return AXIsProcessTrusted() ? GT_OK : GT_ERR_NOT_TRUSTED;
}

// Seconds the raise gets when the window was just restored from the Dock. The one number in this file
// that came out of a measurement rather than a copy.
//
// MEASURED on macOS 26.6.2, four trials per variant against a Finder window that was minimized while
// another application was frontmost. kAXRaiseAction issued while the Dock's restore animation is
// running never answers: it burns the whole GT_ACTION_TIMEOUT_SEC and returns kAXErrorCannotComplete
// (-25204). The unminimize and the two attribute writes around it cost 0.2-0.4 ms each; the raise cost
// 251-255 ms, for a 258-292 ms call. The action still lands -- the window was correctly frontmost
// inside its application in 4/4 -- so the only thing the wait buys is the reply.
//
// Dropping the timeout to 20 ms for that one call gave the same 4/4 in 28-35 ms. Skipping the raise
// altogether was tried too and is 7-12 ms and wrong: restoring from the Dock does NOT put the window
// at the front of its application (0/4 raised, though 4/4 did make the app frontmost). Five trials on
// the code as it stands: 5/5 both halves, 33-39 ms.
static const float GT_RESTORE_RAISE_TIMEOUT_SEC = 0.02f;

gt_status gt_window_raise(uint32_t wid) {
    gt_status g = guard();
    if (g != GT_OK) return g;

    pid_t pid = 0;
    gt_status st = GT_OK;
    AXUIElementRef win = copy_window_element((CGWindowID)wid, &pid, &st);
    if (!win) {
        // P7.1 (D46): the enumeration join (window.go) deliberately offers windows Accessibility
        // cannot see -- typically on another Space -- and this is the action path inheriting D20's
        // blind spot: kAXWindowsAttribute does not list them, so there is no element to raise. If the
        // owning application is still running, activate it: the specific window is not reordered, but
        // the user reaches the app they aimed at, which is the visible half of the switch. Only
        // GT_ERR_NO_WINDOW takes this path -- a timeout or a revoked grant is a different answer and
        // is returned as-is. A dead owner stays GT_ERR_NO_WINDOW for P7.2 to prune.
        if (st == GT_ERR_NO_WINDOW && app_is_alive(pid)) {
            activate_app(pid);
            return GT_OK;
        }
        return st;
    }

    // Unminimize first: everything below is a no-op on a window that is still in the Dock, and a
    // switcher that selects a minimized window and does nothing visible is broken.
    bool restored = false;
    if (window_is_minimized(win)) {
        AXError err = set_minimized(win, false);
        if (err != kAXErrorSuccess) {
            CFRelease(win);
            return status_for_ax(err);
        }
        restored = true;
    }

    // How the application itself learns which of its windows the user chose. Non-fatal: a window that
    // refuses them still comes forward, and only the raise below decides the ordering.
    AXUIElementSetAttributeValue(win, kAXMainAttribute, kCFBooleanTrue);
    AXUIElementSetAttributeValue(win, kAXFocusedAttribute, kCFBooleanTrue);

    if (restored) AXUIElementSetMessagingTimeout(win, GT_RESTORE_RAISE_TIMEOUT_SEC);
    AXError raiseErr = AXUIElementPerformAction(win, kAXRaiseAction);
    CFRelease(win);

    // Activation is unconditional, and that is not tidiness. A raise that failed inside an application
    // nobody is looking at leaves the user staring at the app they were trying to leave; making the
    // app frontmost is the half of the switch they can see, so it does not hang off the half they
    // cannot. It is also cheap: 25 ms in the instrumented run, and none of it our own IPC.
    activate_app(pid);

    if (restored && raiseErr == kAXErrorCannotComplete) {
        // Expected, not a failure: see GT_RESTORE_RAISE_TIMEOUT_SEC. The window answered the
        // unminimize a moment ago, so this is the animation and not an unresponsive application --
        // and the application already proved it is alive by answering everything before this.
        return GT_OK;
    }
    return status_for_ax(raiseErr);
}

gt_status gt_window_minimize(uint32_t wid) {
    gt_status g = guard();
    if (g != GT_OK) return g;

    pid_t pid = 0;
    gt_status st = GT_OK;
    AXUIElementRef win = copy_window_element((CGWindowID)wid, &pid, &st);
    if (!win) return st;

    AXError err = window_is_minimized(win) ? kAXErrorSuccess : set_minimized(win, true);
    CFRelease(win);
    return status_for_ax(err);
}

gt_status gt_window_unminimize(uint32_t wid) {
    gt_status g = guard();
    if (g != GT_OK) return g;

    pid_t pid = 0;
    gt_status st = GT_OK;
    AXUIElementRef win = copy_window_element((CGWindowID)wid, &pid, &st);
    if (!win) return st;

    AXError err = window_is_minimized(win) ? set_minimized(win, false) : kAXErrorSuccess;
    CFRelease(win);
    return status_for_ax(err);
}

gt_status gt_window_close(uint32_t wid) {
    gt_status g = guard();
    if (g != GT_OK) return g;

    pid_t pid = 0;
    gt_status st = GT_OK;
    AXUIElementRef win = copy_window_element((CGWindowID)wid, &pid, &st);
    if (!win) return st;

    // Accessibility has no close action on a window. The gesture a user performs is a press of the
    // red button, and that button is a child element -- so this is what "close" means here, including
    // the save sheet an application is entitled to put up instead of closing.
    CFTypeRef button = NULL;
    AXError err = AXUIElementCopyAttributeValue(win, kAXCloseButtonAttribute, &button);
    CFRelease(win);
    if (err != kAXErrorSuccess || !button) {
        if (button) CFRelease(button);
        // No close button is a fact about the window (a sheet, some palettes), not a failure.
        return err == kAXErrorSuccess ? GT_ERR_UNAVAILABLE : status_for_ax(err);
    }

    err = AXUIElementPerformAction((AXUIElementRef)button, kAXPressAction);
    CFRelease(button);
    return status_for_ax(err);
}
