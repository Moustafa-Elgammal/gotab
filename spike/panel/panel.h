// C surface for the NSPanel spike. Kept free of Objective-C so cgo can include it.
#ifndef GOTAB_PANEL_PANEL_H
#define GOTAB_PANEL_PANEL_H

#include <stdint.h>

enum {
    PANEL_OK = 0,
    PANEL_NO_SCREEN = 1,
    PANEL_TIMEOUT = 2   // the frame never committed
};

// One summon, measured three ways. All milliseconds from the moment the summon began.
//
// The three exist because they are not the same number and the difference is the finding: AppKit
// returning from an order-front call says nothing about pixels (PLATFORM-LESSONS section 5 -
// CoreAnimation commits at the end of the runloop turn).
typedef struct panel_sample {
    double call_ms;    // makeKeyAndOrderFront returned
    double draw_ms;    // drawRect: actually ran - 0 if the frame was served from the backing store
    double commit_ms;  // the CATransaction completion block fired - handed to the render server
    double turn_ms;    // the next main-queue turn ran - CA has certainly committed by here
    int32_t draws;     // how many times drawRect: ran during this summon
    int32_t err;
} panel_sample;

// Creates NSApp and the panel. Must run on the thread Go locked in init. Returns a PANEL_* code.
int32_t panel_init(int32_t tiles);

// One summon/dismiss cycle, filling *out. Blocks, pumping the main run loop, until the frame commits
// or timeout_ms elapses.
void panel_cycle(int32_t timeout_ms, int32_t hold_ms, panel_sample *out);

// Leaves the panel up so a human can look at it. Pumps the run loop for ms.
void panel_show_for(int32_t ms);

// True if the panel is on screen and the app that was frontmost still is.
int32_t panel_stole_focus(void);

#endif
