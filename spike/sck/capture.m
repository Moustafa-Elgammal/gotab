// Spike P0.6: the ScreenCaptureKit bridge.
//
// CGWindowListCreateImage is obsoleted in macOS 15 (D3), so capture must go through SCK, which is
// asynchronous and block-based. Go has no way to hold an Objective-C block, so the async API is
// wrapped into synchronous calls here and Go only ever sees blocking C functions.
//
// Four things in here are load-bearing and look removable:
//
//   - sck_init's CGMainDisplayID call. CoreGraphics lazily connects to the WindowServer, and a
//     process that is not an NSApplication never triggers that connection; the first SCK capture
//     then aborts on `Assertion failed: (did_initialize), CGS_REQUIRE_INIT`. Measured, not recalled
//     - see D12. It is an abort, not an error return, so there is nothing to handle after the fact.
//   - The shareable-content cache. getShareableContent costs ~46 ms, which is half the summon
//     budget; calling it per thumbnail was the first version of this file and it made every capture
//     look 2x more expensive than it is.
//   - The dispatch_semaphore timeouts. If a completion handler needed a run loop that a Go-owned
//     thread is not running, these calls would hang forever and the spike would teach nothing.
//     Timing out and saying so was the point. (It fires; see D12.)
//   - CGImageRetain inside the completion block. The image is autoreleased and owned by SCK; without
//     an explicit retain it dies when the block returns, and Go is handed a dangling pointer that
//     usually still "works" for a while.
//
// Compiled without ARC (cgo does not pass -fobjc-arc), so retain/release here is manual.

#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#include "capture.h"

// The cached window list. A spike is single-threaded from Go, so no lock: Phase 2 owns this in the
// event-loop goroutine instead, which is the same guarantee by a better route.
static SCShareableContent *g_content = nil;

static void set_msg(sck_result *out, NSString *s) {
    if (!s) return;
    strncpy(out->msg, [s UTF8String], sizeof(out->msg) - 1);
    out->msg[sizeof(out->msg) - 1] = '\0';
}

void sck_init(void) {
    // Forces CoreGraphics to establish its WindowServer connection. Any CG display call does it;
    // this is the cheapest. NSApplicationLoad() also works but drags in AppKit and a main-thread
    // requirement for a capture path that otherwise needs neither.
    (void)CGMainDisplayID();
}

// Is this a window a switcher would show? Real windows, not the 1x1 helper windows every app
// leaves lying around.
static BOOL usable(SCWindow *w) {
    return w.isOnScreen && w.owningApplication != nil
        && w.frame.size.width >= 200 && w.frame.size.height >= 200;
}

int32_t sck_refresh(int32_t timeout_ms, char *msg, int32_t msg_len) {
    @autoreleasepool {
        if (![SCShareableContent respondsToSelector:@selector(getShareableContentWithCompletionHandler:)]) {
            if (msg && msg_len > 0) strncpy(msg, "SCShareableContent unavailable on this OS", msg_len - 1);
            return -1;
        }
        __block SCShareableContent *result = nil;
        __block NSString *failure = nil;
        dispatch_semaphore_t sem = dispatch_semaphore_create(0);

        [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *content, NSError *error) {
            if (error) failure = [[error localizedDescription] copy];
            else       result = [content retain];
            dispatch_semaphore_signal(sem);
        }];

        long waited = dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeout_ms * NSEC_PER_MSEC));
        if (waited != 0) failure = @"getShareableContent timed out - completion handler never fired";

        if (!result) {
            if (msg && msg_len > 0) {
                strncpy(msg, [(failure ?: @"unknown") UTF8String], msg_len - 1);
                msg[msg_len - 1] = '\0';
            }
            return -1;
        }
        if (g_content) [g_content release];
        g_content = result;
        return (int32_t)g_content.windows.count;
    }
}

int32_t sck_list_windows(uint32_t *ids, int32_t max) {
    int32_t n = 0;
    if (!g_content) return 0;
    for (SCWindow *w in g_content.windows) {
        if (n >= max) break;
        if (!usable(w)) continue;
        ids[n++] = (uint32_t)w.windowID;
    }
    return n;
}

static SCWindow *find_window(uint32_t wanted) {
    if (!g_content) return nil;
    for (SCWindow *w in g_content.windows) {
        if (wanted == 0) { if (usable(w)) return w; continue; }
        if ((uint32_t)w.windowID == wanted) return w;
    }
    return nil;
}

// Downscale AT CAPTURE TIME (ARCHITECTURE.md memory rule #4). Capturing full-res and shrinking
// afterwards would allocate the full bitmap first, which is the cost we are trying not to pay.
static SCStreamConfiguration *config_for(SCWindow *win, int32_t max_w, int32_t max_h) {
    SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
    CGFloat sw = win.frame.size.width, sh = win.frame.size.height;
    CGFloat scale = fmin((CGFloat)max_w / sw, (CGFloat)max_h / sh);
    if (scale > 1.0) scale = 1.0;
    cfg.width = (size_t)(sw * scale);
    cfg.height = (size_t)(sh * scale);
    cfg.showsCursor = NO;
    cfg.scalesToFit = YES;
    return cfg;
}

static void fill_from_image(sck_result *out, CGImageRef img) {
    out->image = img;
    out->width = (int32_t)CGImageGetWidth(img);
    out->height = (int32_t)CGImageGetHeight(img);
    out->bytes = (int64_t)CGImageGetBytesPerRow(img) * (int64_t)CGImageGetHeight(img);
    out->err = SCK_OK;
}

void sck_capture(uint32_t window_id, int32_t max_w, int32_t max_h, int32_t timeout_ms, sck_result *out) {
    memset(out, 0, sizeof(*out));

    @autoreleasepool {
        if (NSClassFromString(@"SCScreenshotManager") == nil) {
            out->err = SCK_UNAVAILABLE;
            set_msg(out, @"SCScreenshotManager unavailable (needs macOS 14+)");
            return;
        }
        SCWindow *win = find_window(window_id);
        if (!win) {
            out->err = g_content ? SCK_NO_WINDOW : SCK_NO_CONTENT;
            set_msg(out, g_content ? @"window not in the cached list - refresh" : @"sck_refresh not called");
            return;
        }
        out->window_id = (uint32_t)win.windowID;

        SCContentFilter *filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:win];
        SCStreamConfiguration *cfg = config_for(win, max_w, max_h);

        __block CGImageRef captured = NULL;
        __block NSString *failure = nil;
        dispatch_semaphore_t sem = dispatch_semaphore_create(0);

        [SCScreenshotManager captureImageWithFilter:filter
                                      configuration:cfg
                                  completionHandler:^(CGImageRef image, NSError *error) {
            if (error) failure = [[error localizedDescription] copy];
            // The image is autoreleased and owned by SCK. Retain or Go gets a dangling pointer.
            else if (image) captured = CGImageRetain(image);
            dispatch_semaphore_signal(sem);
        }];

        long waited = dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeout_ms * NSEC_PER_MSEC));
        [filter release];
        [cfg release];

        if (waited != 0) {
            out->err = SCK_TIMEOUT;
            set_msg(out, @"captureImage timed out - completion handler never fired");
            return;
        }
        if (!captured) {
            out->err = SCK_CAPTURE_FAILED;
            set_msg(out, failure ?: @"capture returned no image");
            return;
        }
        fill_from_image(out, captured);
    }
}

void sck_capture_many(const uint32_t *ids, int32_t n, int32_t max_w, int32_t max_h,
                      int32_t timeout_ms, sck_result *out) {
    memset(out, 0, sizeof(sck_result) * (size_t)n);
    if (n <= 0) return;

    @autoreleasepool {
        dispatch_group_t group = dispatch_group_create();

        for (int32_t i = 0; i < n; i++) {
            sck_result *slot = &out[i];
            SCWindow *win = find_window(ids[i]);
            if (!win) {
                slot->err = g_content ? SCK_NO_WINDOW : SCK_NO_CONTENT;
                set_msg(slot, @"window not in the cached list");
                continue;
            }
            slot->window_id = (uint32_t)win.windowID;

            SCContentFilter *filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:win];
            SCStreamConfiguration *cfg = config_for(win, max_w, max_h);

            // Enter before issuing, leave in the handler: the group counts outstanding captures, so
            // all n are in flight at once rather than serialised behind a semaphore each.
            dispatch_group_enter(group);
            [SCScreenshotManager captureImageWithFilter:filter
                                          configuration:cfg
                                      completionHandler:^(CGImageRef image, NSError *error) {
                if (image) fill_from_image(slot, CGImageRetain(image));
                else {
                    slot->err = SCK_CAPTURE_FAILED;
                    set_msg(slot, error ? [error localizedDescription] : @"capture returned no image");
                }
                dispatch_group_leave(group);
            }];
            [filter release];
            [cfg release];
        }

        if (dispatch_group_wait(group, dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeout_ms * NSEC_PER_MSEC)) != 0) {
            // Some handler never fired. Mark only the slots still empty - the rest are real results,
            // and a partial answer here is the interesting one.
            for (int32_t i = 0; i < n; i++) {
                if (out[i].image == NULL && out[i].err == SCK_OK) {
                    out[i].err = SCK_TIMEOUT;
                    set_msg(&out[i], @"completion handler never fired");
                }
            }
        }
    }
}

void sck_release(CGImageRef image) {
    if (image) CGImageRelease(image);
}
