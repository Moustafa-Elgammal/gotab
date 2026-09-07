// Accessibility observers: the push half of the window model.
//
// gt_window_list and gt_ax_window_list answer "what is open right now". This file answers "something
// just changed, ask again" -- so the switcher re-reads on an event instead of on a ticker. Same
// conventions as shim.h: gt_ prefix, gt_status for anything that can fail, no allocation Go must free.
#ifndef GOTAB_DARWIN_OBSERVE_H
#define GOTAB_DARWIN_OBSERVE_H

#include "shim.h"

// Starts one AXObserver per regular application and keeps that set in step with applications
// launching and quitting. Every notification -- from any application -- ends in one call to
// goObserveChange(), which is defined in Go and is always called on this file's own observer thread.
//
// Requires the Accessibility grant: without it AXObserverCreate returns kAXErrorAPIDisabled for every
// process and the result would be a mechanism that installs cleanly and never fires. Returns
// GT_ERR_NOT_TRUSTED instead, for the same reason gt_ax_window_list does.
//
// PRECONDITION, MEASURED, AND THE CALLER'S TO MEET: applications that launch after this call are
// picked up only if the process runs a MAIN RUN LOOP (CFRunLoopRun on the main thread, or AppKit's,
// which is the same thing). NSWorkspace's launch and terminate notifications are posted by that loop
// and by nothing else; with no main run loop they are not delivered at all, so a newly launched
// application is never observed and its windows never appear. The AX observers themselves are
// unaffected -- they run on a thread this file owns and were measured firing with no main run loop
// anywhere -- so the failure is partial and silent, which is why it is stated here rather than
// returned: there is nothing to return it from, the loop may start after this call.
//
// Idempotent: a second call while running is GT_OK and changes nothing. Safe from any thread, but
// Go serialises start against stop and this file assumes that.
gt_status gt_observers_start(void);

// Removes every observer and its run-loop source, unsubscribes from the NSWorkspace notifications,
// and does not return until the observer thread has finished tearing down -- so that after this
// returns, goObserveChange() cannot still be called. Safe to call when not running, and safe twice.
void gt_observers_stop(void);

#endif
