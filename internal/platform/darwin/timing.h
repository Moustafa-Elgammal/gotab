// V6.3 SCAFFOLD — summon → first-frame-on-screen latency, and nothing else. Not part of the shipped
// switcher: gt_panel_show only does the extra work when gt_timing_arm() has armed it, and the switcher
// never calls that. The measurement technique is spike/panel's (D13) — a CATransaction completion
// block is the closest reachable edge to "pixels" without a display link — moved onto the assembled
// app, which already has a live main run loop so no nested pump is needed.
//
// Kept in its own header so panel.h (frozen by the Phase 3 carve) is not touched.
#ifndef GOTAB_DARWIN_TIMING_H
#define GOTAB_DARWIN_TIMING_H

// Arm the NEXT gt_panel_show: it wraps its orderFrontRegardless in a CATransaction whose completion
// block calls goTimingFrameCommitted() once, when that frame is handed to the render server. One-shot
// — it disarms itself. Unarmed (the shipped state) gt_panel_show does nothing extra.
void gt_timing_arm(void);

// Defined in Go (timing.go). Runs on the main thread, inside the completion block.
extern void goTimingFrameCommitted(void);

#endif
