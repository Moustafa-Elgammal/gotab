// The AppKit main run loop, run and stopped explicitly so a Go goroutine can end it.
//
// Everything AppKit in this process needs this turning: the panel's CoreAnimation frames commit on
// run-loop turns (D13), P2.3b's observers get their NSWorkspace launch/quit notifications only while
// the MAIN loop runs, dispatch_get_main_queue() blocks from darwin.OnMain are serviced here, and
// P3.4's appearance KVO is delivered here.
//
// cmd/gotab calls gt_run_loop() on the thread it locked in init (docs/ARCHITECTURE.md#threading) and
// gt_run_loop_stop() from its shutdown goroutine.
#ifndef GOTAB_DARWIN_RUNLOOP_H
#define GOTAB_DARWIN_RUNLOOP_H

// Runs the main run loop on the calling thread. Returns only after gt_run_loop_stop(). Calls
// -[NSApplication finishLaunching] once so AppKit is in a consistent state first; it does NOT call
// -[NSApp run], because that swallows a stop request until the next event and this needs a clean
// stop from another thread.
void gt_run_loop(void);

// Stops the run loop started by gt_run_loop(). Safe from any thread, and safe if the loop is not
// running (a later gt_run_loop() is unaffected).
void gt_run_loop_stop(void);

#endif
