// The Objective-C side of the accessibility observers.
//
// Compiled WITHOUT ARC, like shim.m: every CFRetain/CFRelease here is manual and paired.
//
// ---------------------------------------------------------------------------
// Where the run loop lives, and why it is not the main one
// ---------------------------------------------------------------------------
//
// An AXObserver delivers nothing on its own. AXObserverGetRunLoopSource has to be added to a run loop
// that is actually running; scheduled on a loop nobody runs, the observer registers without error,
// reports success, and is silent forever. That failure mode is invisible, so the choice of loop is the
// whole design of this file.
//
// The obvious candidate is the main run loop, which AppKit runs (docs/ARCHITECTURE.md#threading). It
// is rejected for two reasons:
//
//   1. It is not ours. main() hands that thread to AppKit and everything drawn goes through it,
//      including the summon path that has a 100 ms budget. AX notifications arrive in bursts -- one
//      measured run saw eleven kAXUIElementDestroyed from a single application in a row -- and putting
//      that traffic in front of the pixels the user is waiting for inverts the priority
//      ARCHITECTURE.md's "draw first, do bookkeeping after" sets.
//   2. It is not running when this starts. StartObservers can be called from a Go goroutine before
//      NSApp takes the main thread. Depending on someone else having started a loop is depending on
//      the one thing that fails silently.
//
// So this file owns a thread whose only job is to run a run loop. It is created here, it runs
// CFRunLoopRun until stop, and nothing else is ever scheduled on it. Every AX call -- create, add
// notification, tear down -- happens on it too, so the observer state needs no lock: it is reachable
// from exactly one thread, and other threads reach it by posting a block (run_on_observer_thread).
// goObserveChange() is called from that thread and from nowhere else, including on the NSWorkspace
// paths, which hand over rather than crossing into Go on the thread they were posted on.
//
// ---------------------------------------------------------------------------
// The one dependency on the main run loop, which is measured and not assumed
// ---------------------------------------------------------------------------
//
// NSWorkspace's launch and terminate notifications are delivered by the MAIN run loop. Measured on
// this machine: with a process that starts observers and then never runs a main run loop, launching an
// application produces no NSWorkspaceDidLaunchApplicationNotification at all -- not late, not on
// another thread, never -- so the new application is never observed. With CFRunLoopRun() on the main
// thread the same test delivers it immediately, on the main thread. Passing queue:nil rather than
// [NSOperationQueue mainQueue] changes nothing: the block still runs on the main thread, because the
// posting is what needs the loop, not the hop. queue:nil is kept only because the hop bought nothing.
//
// The AX observers themselves do NOT depend on this: they were measured firing normally with no main
// run loop anywhere. So a host that never runs one still sees window events from every application
// that existed at start, and silently misses every application launched afterwards. That is a
// precondition on the caller, stated in observe.h, not something this file can paper over.
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdatomic.h>
#include "observe.h"

// Defined in Go (observe.go). Called only from the observer thread, never with a lock held, and it
// must return immediately -- see the callback body below.
extern void goObserveChange(void);

// Seconds an application gets to answer one AX request. Same reasoning as shim.m's copy: an app that
// is beachballing or stopped in a debugger will not answer at all, and the default is to wait
// forever. Here the cost of waiting is worse than there -- a wedged app would stall the run loop that
// every other application's notifications are delivered on.
static const float GT_OBSERVE_TIMEOUT_SEC = 0.25f;

// A freshly launched application is not ready to register all five notifications, and this is the
// finding that shaped the retry: measured against TextEdit one dispatch after its launch
// notification, kAXWindowCreated, kAXWindowMiniaturized and kAXWindowDeminiaturized registered
// (kAXErrorSuccess) while kAXUIElementDestroyed and kAXFocusedWindowChanged both returned
// kAXErrorCannotComplete (-25204) -- the two that matter most to a switcher. A retry that gave up as
// soon as ANY notification registered would have left that application permanently half-observed, so
// the gate is "all five", not "at least one". Milliseconds from the launch notification; the first
// entry is 0 because the common case is that it is ready.
static const int64_t GT_OBSERVE_RETRY_MS[] = {0, 250, 500, 1000, 2000, 4000};

// One bit per notification, in the order observe_notifications fills. A per-application mask is what
// lets a retry ask only for the ones still missing instead of re-registering the whole set.
enum {
    GT_N_ALL = 0x1F,
    GT_N_COUNT = 5
};

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

// The observer thread's run loop, retained on creation because blocks posted from other threads read
// it. Released by gt_observers_stop, but only once no reader can still be holding it: g_posting
// counts the threads currently inside run_on_observer_thread, and stop drains that to zero first.
//
// The obvious alternative -- never release it -- was measured and rejected. `leaks` against a process
// that ran twenty start/stop cycles reported twenty ROOT LEAK <CFRunLoop> at 3072 bytes each and
// nothing else of ours; the same run reported no leaked AXObserverRef and no leaked run-loop source.
// Per process that would be nothing, but it is per cycle, and a leak an instrument names is not a
// deliberate cost.
static _Atomic(CFRunLoopRef) g_loop = NULL;
static atomic_int g_posting = 0;

// True between start and stop. Gates every cross-thread post, so a notification in flight during stop
// is dropped rather than racing the teardown.
static atomic_bool g_active = false;
static atomic_bool g_stopping = false;

// pid -> AXObserverRef, pid -> the application AXUIElementRef it was registered on, and pid -> the
// mask of notifications actually registered so far. Touched ONLY on the observer thread, which is why
// none of them is guarded. The element is kept because AXObserverAddNotification is not documented to
// retain it, and one CFRetain per application is cheaper than finding out that it does not.
static CFMutableDictionaryRef g_observers = NULL;
static CFMutableDictionaryRef g_elements = NULL;
static CFMutableDictionaryRef g_masks = NULL;

static CFRunLoopSourceRef g_keepalive = NULL;  // observer thread only
static dispatch_semaphore_t g_done = NULL;     // start/stop only, which Go serialises
static id g_launch_token = nil;                // NSWorkspace observer handles
static id g_quit_token = nil;

// ---------------------------------------------------------------------------
// The callback
// ---------------------------------------------------------------------------

// Nothing but the crossing. No allocation, no logging, no reading of the notification name, no
// deciding whether this particular event mattered -- all of that is the event loop's job on the Go
// side (docs/ARCHITECTURE.md#the-cgo-rule). A callback that stalls stalls the run loop that delivers
// every other application's notifications, and the mechanism dies without an error anywhere.
static void observe_callback(AXObserverRef observer, AXUIElementRef element,
                             CFStringRef notification, void *refcon) {
    goObserveChange();
}

// ---------------------------------------------------------------------------
// Registration (observer thread only)
// ---------------------------------------------------------------------------

static CFNumberRef pid_key(pid_t pid) {
    int32_t v = (int32_t)pid;
    return CFNumberCreate(NULL, kCFNumberSInt32Type, &v);
}

// The five the switcher's window set can change under. Built here rather than at file scope because
// the kAX* names are `extern const CFStringRef`, not compile-time constants. The order is the bit
// order of the mask.
static void observe_notifications(CFStringRef out[GT_N_COUNT]) {
    out[0] = kAXWindowCreatedNotification;
    out[1] = kAXUIElementDestroyedNotification;
    out[2] = kAXFocusedWindowChangedNotification;
    out[3] = kAXWindowMiniaturizedNotification;
    out[4] = kAXWindowDeminiaturizedNotification;
}

static int32_t observers_mask(CFNumberRef key) {
    int32_t have = 0;
    CFNumberRef n = g_masks ? (CFNumberRef)CFDictionaryGetValue(g_masks, key) : NULL;
    if (n) CFNumberGetValue(n, kCFNumberSInt32Type, &have);
    return have;
}

// Registers whatever is still missing for pid and returns the mask registered afterwards. GT_N_ALL
// means finished; anything less means "ask again later", which is what drives the retry. Called only
// on the observer thread.
static int32_t observers_add_app(pid_t pid) {
    if (pid <= 0 || !g_observers) return GT_N_ALL;  // nothing to retry towards

    CFNumberRef key = pid_key(pid);
    if (!key) return GT_N_ALL;
    int32_t have = observers_mask(key);
    if (have == GT_N_ALL) {
        CFRelease(key);
        return GT_N_ALL;
    }

    // Second and later attempts reuse the observer already installed, so a partially registered
    // application gains the missing notifications rather than a second observer.
    AXObserverRef obs = (AXObserverRef)CFDictionaryGetValue(g_observers, key);
    AXUIElementRef appEl = (AXUIElementRef)CFDictionaryGetValue(g_elements, key);
    const bool fresh = (obs == NULL);
    if (fresh) {
        if (AXObserverCreate(pid, observe_callback, &obs) != kAXErrorSuccess || !obs) {
            CFRelease(key);
            return have;
        }
        appEl = AXUIElementCreateApplication(pid);
        if (!appEl) {
            CFRelease(obs);
            CFRelease(key);
            return have;
        }
        AXUIElementSetMessagingTimeout(appEl, GT_OBSERVE_TIMEOUT_SEC);
    }

    CFStringRef notes[GT_N_COUNT];
    observe_notifications(notes);
    for (int i = 0; i < GT_N_COUNT; i++) {
        if (have & (1 << i)) continue;
        AXError e = AXObserverAddNotification(obs, appEl, notes[i], NULL);
        if (e == kAXErrorSuccess || e == kAXErrorNotificationAlreadyRegistered) have |= (1 << i);
    }

    if (have == 0) {
        // The application answered nothing: not ready, or already gone. Either way there is nothing
        // worth keeping; the caller retries on a backoff and gives up rather than accumulating dead
        // state for every short-lived process on the machine.
        if (fresh) {
            CFRelease(appEl);
            CFRelease(obs);
        }
        CFRelease(key);
        return 0;
    }

    if (fresh) {
        CFRunLoopAddSource(CFRunLoopGetCurrent(), AXObserverGetRunLoopSource(obs),
                           kCFRunLoopDefaultMode);
        CFDictionarySetValue(g_observers, key, obs);  // the dictionaries hold the only strong refs
        CFDictionarySetValue(g_elements, key, appEl);
        CFRelease(appEl);
        CFRelease(obs);
    }
    CFNumberRef m = CFNumberCreate(NULL, kCFNumberSInt32Type, &have);
    if (m) {
        CFDictionarySetValue(g_masks, key, m);
        CFRelease(m);
    }
    CFRelease(key);
    return have;
}

// Returns whether pid was being observed, which is what tells a terminating process that could have
// owned a window from one that could not.
static bool observers_remove_app(pid_t pid) {
    if (!g_observers) return false;
    CFNumberRef key = pid_key(pid);
    if (!key) return false;

    AXObserverRef obs = (AXObserverRef)CFDictionaryGetValue(g_observers, key);
    bool tracked = false;
    if (obs) {
        // No AXObserverRemoveNotification. This is reached mostly because the application QUIT, and
        // each of the five removals would then be a Mach round trip to a process that is gone --
        // five messaging timeouts, 1.25 s, on the thread every other application's notifications
        // arrive on. Dropping the source and releasing the observer frees the same things.
        CFRunLoopRemoveSource(CFRunLoopGetCurrent(), AXObserverGetRunLoopSource(obs),
                              kCFRunLoopDefaultMode);
        CFDictionaryRemoveValue(g_observers, key);
        CFDictionaryRemoveValue(g_elements, key);
        CFDictionaryRemoveValue(g_masks, key);
        tracked = true;
    }
    CFRelease(key);
    return tracked;
}

static void observers_remove_one(const void *key, const void *value, void *ctx) {
    AXObserverRef obs = (AXObserverRef)value;
    CFRunLoopRemoveSource(CFRunLoopGetCurrent(), AXObserverGetRunLoopSource(obs), kCFRunLoopDefaultMode);
}

// ---------------------------------------------------------------------------
// Getting onto the observer thread from anywhere else
// ---------------------------------------------------------------------------

// The counter is incremented BEFORE g_loop is read and decremented after the last use of it, so a
// stop that sees zero knows no thread is between the load and the call.
static void run_on_observer_thread(void (^block)(void)) {
    atomic_fetch_add(&g_posting, 1);
    if (atomic_load(&g_active)) {
        CFRunLoopRef loop = atomic_load(&g_loop);
        if (loop) {
            CFRunLoopPerformBlock(loop, kCFRunLoopDefaultMode, block);
            // Without this the block waits for the loop's next wake, which may be never.
            CFRunLoopWakeUp(loop);
        }
    }
    atomic_fetch_sub(&g_posting, 1);
}

// Keeps asking until all five notifications are registered, then stops. Each attempt that registers
// something new also signals a change, because windows created between the launch and the successful
// registration posted their kAXWindowCreated to nobody -- the enumeration is the only way to find them.
static void observers_track(pid_t pid, int attempt) {
    const int attempts = (int)(sizeof(GT_OBSERVE_RETRY_MS) / sizeof(GT_OBSERVE_RETRY_MS[0]));
    if (attempt >= attempts) return;
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, GT_OBSERVE_RETRY_MS[attempt] * NSEC_PER_MSEC),
                   dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
        run_on_observer_thread(^{
            CFNumberRef key = pid_key(pid);
            int32_t before = key ? observers_mask(key) : GT_N_ALL;
            if (key) CFRelease(key);
            int32_t have = observers_add_app(pid);
            if (have != before) goObserveChange();
            if (have != GT_N_ALL) observers_track(pid, attempt + 1);
        });
    });
}

// ---------------------------------------------------------------------------
// The observer thread
// ---------------------------------------------------------------------------

static void keepalive_perform(void *info) {}

static void observer_thread_main(dispatch_semaphore_t ready, dispatch_semaphore_t done) {
    @autoreleasepool {
        CFRunLoopRef loop = CFRunLoopGetCurrent();
        CFRetain(loop);
        atomic_store(&g_loop, loop);

        // CFRunLoopRun returns immediately on a loop with no sources, and would spin. This source
        // never fires; it exists so the loop has a reason to block.
        CFRunLoopSourceContext ctx;
        memset(&ctx, 0, sizeof(ctx));
        ctx.perform = keepalive_perform;
        g_keepalive = CFRunLoopSourceCreate(NULL, 0, &ctx);
        CFRunLoopAddSource(loop, g_keepalive, kCFRunLoopDefaultMode);

        g_observers = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks,
                                                &kCFTypeDictionaryValueCallBacks);
        g_elements = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks,
                                               &kCFTypeDictionaryValueCallBacks);
        g_masks = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks,
                                            &kCFTypeDictionaryValueCallBacks);

        // Released before the initial sweep, not after: the sweep is one AX round trip per running
        // application and gt_observers_start has no reason to block on it.
        dispatch_semaphore_signal(ready);

        for (NSRunningApplication *app in [[NSWorkspace sharedWorkspace] runningApplications]) {
            // Same filter as gt_ax_window_list, and the same known miss (D20): an .Accessory
            // application can own an ordinary window. Widening it here would register observers on
            // every XPC service on the machine to catch a case AX reports no windows for anyway.
            if (app.activationPolicy != NSApplicationActivationPolicyRegular) continue;
            // No goObserveChange for the sweep: whoever called StartObservers is about to enumerate.
            if (observers_add_app(app.processIdentifier) != GT_N_ALL) {
                observers_track(app.processIdentifier, 1);  // attempt 0 was the call above
            }
        }

        while (!atomic_load(&g_stopping)) {
            @autoreleasepool {
                CFRunLoopRun();
            }
        }

        // Teardown, on the thread that owns the state. CFRunLoopRemoveSource for each observer, then
        // the dictionary release drops the last reference to the AXObserverRef and the element.
        CFDictionaryApplyFunction(g_observers, observers_remove_one, NULL);
        CFRelease(g_observers);
        g_observers = NULL;
        CFRelease(g_elements);
        g_elements = NULL;
        CFRelease(g_masks);
        g_masks = NULL;

        CFRunLoopRemoveSource(loop, g_keepalive, kCFRunLoopDefaultMode);
        CFRelease(g_keepalive);
        g_keepalive = NULL;

        dispatch_semaphore_signal(done);
    }
    // The pair of gt_observers_start's dispatch_retain. Explicit rather than relying on the block
    // capture, because whether a block copy retains a dispatch object depends on OS_OBJECT_USE_OBJC.
    dispatch_release(ready);
    dispatch_release(done);
}

// ---------------------------------------------------------------------------
// Public surface
// ---------------------------------------------------------------------------

static void workspace_subscribe(void) {
    @autoreleasepool {
    NSNotificationCenter *nc = [[NSWorkspace sharedWorkspace] notificationCenter];
    // queue:nil, so the block runs wherever NSWorkspace posts -- measured to be the main thread. It
    // does not matter which thread it is, because the block reads two fields and hands over; every
    // decision, every AX call and the crossing into Go happen on the observer thread.
    g_launch_token = [[nc addObserverForName:NSWorkspaceDidLaunchApplicationNotification
                                      object:nil
                                       queue:nil
                                  usingBlock:^(NSNotification *note) {
        NSRunningApplication *app = note.userInfo[NSWorkspaceApplicationKey];
        if (!app || app.activationPolicy != NSApplicationActivationPolicyRegular) return;
        pid_t pid = app.processIdentifier;
        observers_track(pid, 0);
        // A launched application can already own a window by the time this arrives -- its
        // kAXWindowCreated was posted before there was an observer to hear it.
        run_on_observer_thread(^{ goObserveChange(); });
    }] retain];

    g_quit_token = [[nc addObserverForName:NSWorkspaceDidTerminateApplicationNotification
                                    object:nil
                                     queue:nil
                                usingBlock:^(NSNotification *note) {
        NSRunningApplication *app = note.userInfo[NSWorkspaceApplicationKey];
        if (!app) return;
        pid_t pid = app.processIdentifier;
        // Read here rather than on the observer thread: a terminated NSRunningApplication's
        // properties are not promised to stay readable, and this block runs while it is still fresh.
        const bool regular = (app.activationPolicy == NSApplicationActivationPolicyRegular);
        run_on_observer_thread(^{
            // A dead process posts no kAXUIElementDestroyed, so this is the only notice that its
            // windows are gone. Gated on "we were observing it, or it was a regular application"
            // because NSWorkspace reports every osascript and helper process too: firing on those
            // measurably added two spurious rescans per shell command during testing, and a rescan
            // is an AX round trip per application.
            const bool tracked = observers_remove_app(pid);
            if (tracked || regular) goObserveChange();
        });
    }] retain];
    }
}

static void workspace_unsubscribe(void) {
    @autoreleasepool {
    NSNotificationCenter *nc = [[NSWorkspace sharedWorkspace] notificationCenter];
    if (g_launch_token) {
        [nc removeObserver:g_launch_token];
        [g_launch_token release];
        g_launch_token = nil;
    }
    if (g_quit_token) {
        [nc removeObserver:g_quit_token];
        [g_quit_token release];
        g_quit_token = nil;
    }
    }
}

gt_status gt_observers_start(void) {
    // Checked before anything is created: ungranted, AXObserverCreate fails for every process and
    // what is left is a mechanism that installed cleanly and never fires.
    if (!AXIsProcessTrusted()) return GT_ERR_NOT_TRUSTED;
    if (atomic_exchange(&g_active, true)) return GT_OK;  // already running

    atomic_store(&g_stopping, false);
    // Captured by the thread's block rather than read from a global, so that a start following a stop
    // that timed out cannot hand the old thread the new run's semaphores.
    dispatch_semaphore_t ready = dispatch_semaphore_create(0);
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    g_done = done;
    dispatch_retain(ready);  // released by the thread; see observer_thread_main
    dispatch_retain(done);

    [NSThread detachNewThreadWithBlock:^{
        [[NSThread currentThread] setName:@"gotab.ax-observers"];
        observer_thread_main(ready, done);
    }];

    // The thread signals as soon as its run loop exists, which is immediate. A bound rather than
    // DISPATCH_TIME_FOREVER so a failure to start is reported instead of hanging the caller.
    if (dispatch_semaphore_wait(ready, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) != 0) {
        // Not GT_ERR_TIMEOUT: that status means the WindowServer did not answer, and a thread that
        // does not start in five seconds is this shim being wrong, not the OS being slow.
        atomic_store(&g_active, false);
        dispatch_release(ready);  // the thread that never started still owns the retains above
        return GT_ERR_INTERNAL;
    }
    dispatch_release(ready);  // the thread's block holds its own reference

    workspace_subscribe();
    return GT_OK;
}

void gt_observers_stop(void) {
    if (!atomic_exchange(&g_active, false)) return;  // not running

    workspace_unsubscribe();
    atomic_store(&g_stopping, true);

    CFRunLoopRef loop = atomic_load(&g_loop);
    // Cleared before it is stopped: a later start creates a new thread with a new loop, and a stale
    // pointer here would send its blocks to a loop nobody runs.
    atomic_store(&g_loop, NULL);
    if (loop) CFRunLoopStop(loop);

    // Waits, so that after this returns goObserveChange() cannot still be called from a source this
    // file installed -- which is what lets Go drop the callback without a race.
    dispatch_semaphore_t done = g_done;
    g_done = NULL;
    if (done) {
        if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) == 0) {
            dispatch_release(done);
        }
        // On timeout the reference is deliberately abandoned: the thread still owns its own and may
        // yet signal, and releasing here would be the one bug worse than leaking one semaphore.
    }

    // Now the loop itself. Readers were shut out by clearing g_active and g_loop above; this waits
    // for the ones that were already past those checks. Each is two non-blocking CF calls, so the
    // wait is microseconds -- and if it somehow is not, the loop is leaked rather than freed under a
    // thread still using it. 40 ms is the point at which leaking 3 KB is the better bug.
    if (loop) {
        for (int i = 0; i < 200 && atomic_load(&g_posting) != 0; i++) usleep(200);
        if (atomic_load(&g_posting) == 0) CFRelease(loop);
    }
}
