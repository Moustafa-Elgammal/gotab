package darwin

/*
#include "runloop.h"
*/
import "C"

// RunLoop runs the AppKit main run loop on the calling goroutine's OS thread, which MUST be the one
// main locked in init (docs/ARCHITECTURE.md#threading). It blocks until StopRunLoop is called.
//
// Nothing AppKit works without this: panel frames commit on run-loop turns (D13), P2.3b's observers
// receive NSWorkspace launch/quit notifications only while the main loop runs, OnMain's dispatched
// blocks are drained here, and P3.4's appearance KVO is delivered here.
func RunLoop() { C.gt_run_loop() }

// StopRunLoop ends the loop RunLoop is blocked in. Safe from any goroutine and safe when RunLoop is
// not running. Call it from a shutdown goroutine once the event loop has stopped and any final
// main-thread work (HidePanel, Prefetcher.Stop) has been posted.
func StopRunLoop() { C.gt_run_loop_stop() }
