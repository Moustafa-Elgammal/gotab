package darwin

/*
#include "timing.h"
*/
import "C"

import "sync/atomic"

// V6.3 SCAFFOLD. The bridge for the summon→first-frame latency probe: gt_panel_show, once armed by
// TimingArm, fires goTimingFrameCommitted from a CATransaction completion block when the summon frame
// reaches the render server. TimingOnCommit is where cmd/gotab hangs the "stop the clock" callback.
//
// This adds no cost to the shipped switcher — g_timing_armed starts 0 and only TimingArm sets it, and
// gotab -switch never calls TimingArm. See docs/tasks/V6.3.md.
var timingCommit atomic.Pointer[func()]

// TimingOnCommit registers fn to run when the next armed summon frame is handed to the render server
// (≈ pixels). Pass nil to clear. Safe to call from any goroutine; fn runs on the main thread.
func TimingOnCommit(fn func()) {
	if fn == nil {
		timingCommit.Store(nil)
		return
	}
	timingCommit.Store(&fn)
}

// TimingArm makes the next gt_panel_show install the one-shot completion block. Call it immediately
// before posting the Summon that should be measured.
func TimingArm() { C.gt_timing_arm() }

//export goTimingFrameCommitted
func goTimingFrameCommitted() {
	if fn := timingCommit.Load(); fn != nil {
		(*fn)()
	}
}
