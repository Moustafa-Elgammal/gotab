// C surface for the ScreenCaptureKit bridge. Kept free of Objective-C so cgo can include it.
#ifndef GOTAB_SCK_CAPTURE_H
#define GOTAB_SCK_CAPTURE_H

#include <stdint.h>
#include <CoreGraphics/CoreGraphics.h>

enum {
    SCK_OK = 0,
    SCK_UNAVAILABLE = 1,  // framework or API missing on this OS
    SCK_NO_CONTENT = 2,   // getShareableContent failed - usually Screen Recording not granted
    SCK_NO_WINDOW = 3,    // no suitable window to capture
    SCK_CAPTURE_FAILED = 4,
    SCK_TIMEOUT = 5       // the completion handler never fired
};

typedef struct sck_result {
    CGImageRef image;   // caller owns it; release with sck_release
    int64_t bytes;      // bytes_per_row * height, the real backing store size
    int32_t width;
    int32_t height;
    uint32_t window_id; // which window was actually captured
    int32_t err;
    char msg[256];
} sck_result;

// MUST be called once before anything else, from any thread. CoreGraphics aborts the process with
// `Assertion failed: (did_initialize), CGS_REQUIRE_INIT` on the first capture otherwise - see D12.
void sck_init(void);

// Fetches the shareable window list and caches it. Every sck_capture reads that cache, so the
// enumeration cost is paid here and not per thumbnail. Returns the window count, or -1 on failure.
// Not thread-safe: the cache is a single global, refreshed from the caller's thread.
int32_t sck_refresh(int32_t timeout_ms, char *msg, int32_t msg_len);

// Fills ids with up to max windows from the cached list that are worth showing in a switcher.
// Returns how many were written.
int32_t sck_list_windows(uint32_t *ids, int32_t max);

// Captures one cached window, downscaled to at most max_w x max_h, and blocks until it completes or
// timeout_ms elapses. window_id == 0 means "the first suitable window in the cached list".
void sck_capture(uint32_t window_id, int32_t max_w, int32_t max_h, int32_t timeout_ms, sck_result *out);

// Issues n captures without waiting between them, then waits for all of them. SCK is asynchronous;
// this asks whether that asynchrony can actually be spent to hide latency, which decides whether a
// summon showing n thumbnails costs n x one capture or something closer to one.
void sck_capture_many(const uint32_t *ids, int32_t n, int32_t max_w, int32_t max_h,
                      int32_t timeout_ms, sck_result *out);

void sck_release(CGImageRef image);

#endif
