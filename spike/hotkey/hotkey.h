// C surface for the CGEventTap spike. Kept free of Objective-C so cgo can include it.
#ifndef GOTAB_HOTKEY_HOTKEY_H
#define GOTAB_HOTKEY_HOTKEY_H

#include <stdint.h>

enum {
    HK_OK = 0,
    HK_NOT_TRUSTED = 1,  // Accessibility not granted to the RESPONSIBLE process
    HK_TAP_FAILED = 2,   // CGEventTapCreate returned NULL
    HK_TIMEOUT = 3       // no hotkey arrived in the window
};

// One captured keystroke.
typedef struct {
    // CGEventGetTimestamp -> our callback entry. What the 5 ms budget is about. SIGNED on purpose:
    // a synthetic event carries a timestamp stamped at post time on the posting thread, so this
    // comes out at or below zero and is meaningless. Only a real keypress measures the HID path,
    // and a nonpositive value here is the signal that the number must not be quoted.
    double deliver_ms;
    double roundtrip_us;  // C -> Go -> C, measured around the exported Go function
    double baseline_us;   // the same measurement around nothing, to subtract the instrument itself
    uint32_t keycode;
    uint64_t flags;
    int32_t reenabled;    // the tap had been disabled by timeout and we turned it back on
    int32_t synthetic;    // the event was posted by us, not typed
    uint64_t raw_event_ns; // both raw clocks, so the units assumption stays checkable from outside
    uint64_t raw_cb_ns;
} hk_sample;

// Is Accessibility granted? A tap installs fine without it and then never fires, which is
// indistinguishable from a broken hotkey, so this is checked and reported rather than assumed.
int32_t hk_trusted(void);

// Installs the tap on the current thread's run loop. keycode/flags select what counts as the hotkey.
int32_t hk_start(uint32_t keycode, uint64_t flags, char *msg, int32_t msg_len);

// Pumps the run loop until the hotkey fires or timeout_ms elapses.
int32_t hk_wait(int32_t timeout_ms, hk_sample *out);

// Posts a synthetic hotkey so the measurement can run unattended. Needs the same Accessibility grant.
void hk_post(uint32_t keycode, uint64_t flags);

// How many events of any kind the tap has seen, and how many were swallowed as our hotkey.
int64_t hk_seen(void);
int64_t hk_swallowed(void);

#endif
