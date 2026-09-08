// The AppKit main event loop, run and stopped explicitly so a Go goroutine can end it.
//
// Everything AppKit in this process needs this turning: the panel's CoreAnimation frames commit on
// run-loop turns (D13), P2.3b's observers get their NSWorkspace launch/quit notifications only while
// the MAIN loop runs, dispatch_get_main_queue() blocks from darwin.OnMain are serviced here, and
// P3.4's appearance KVO is delivered here. It must also keep *dequeuing* events — the menu-bar status
// item's clicks (P8.1) and the system's app-responsiveness check both depend on that (D54).
//
// cmd/gotab calls gt_run_loop() on the thread it locked in init (docs/ARCHITECTURE.md#threading) and
// gt_run_loop_stop() from its shutdown goroutine.
#ifndef GOTAB_DARWIN_RUNLOOP_H
#define GOTAB_DARWIN_RUNLOOP_H

// Runs -[NSApplication run] on the calling thread. Returns only after gt_run_loop_stop(). -run does
// its own -finishLaunching and per-turn autorelease pool; the shared NSApplication and its activation
// policy are set earlier, by gt_panel_create / gt_settings_open.
void gt_run_loop(void);

// Stops the loop started by gt_run_loop(): -[NSApp stop:] plus a no-op event to break -run out of its
// wait. Marshalled to the main thread internally, so it is safe from any goroutine and a no-op when
// the loop is not running (a later gt_run_loop() is unaffected).
void gt_run_loop_stop(void);

#endif
