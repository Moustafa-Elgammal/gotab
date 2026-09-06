// The C surface of the platform layer. Kept free of Objective-C so cgo can include it directly.
//
// Every function here is prefixed gt_ and is callable from any thread unless its comment says
// otherwise. Conventions fixed by P2.1 and followed by every later Phase 2/3 task:
//
//   - Anything that can fail returns gt_status. C cannot return a Go error, and an out-parameter
//     errno would be a second thing to keep in sync; a small closed enum maps to a Go sentinel error
//     in exactly one place (shim.go statusError).
//   - Anything that returns bulk data fills a caller-allocated buffer and reports how many items it
//     wrote, so one crossing serves N items. See docs/ARCHITECTURE.md#the-cgo-rule.
//   - Nothing here allocates memory that Go is expected to free with free(). Bitmaps are handles.
#ifndef GOTAB_DARWIN_SHIM_H
#define GOTAB_DARWIN_SHIM_H

#include <stdint.h>

typedef int32_t gt_status;

enum {
    GT_OK = 0,
    GT_ERR_NOT_TRUSTED = 1,  // Accessibility not granted to the responsible process
    GT_ERR_NO_RECORDING = 2, // Screen Recording not granted to the responsible process
    GT_ERR_UNAVAILABLE = 3,  // the OS declined to answer; not a bug, not retryable in the same way
    GT_ERR_TIMEOUT = 4,      // the WindowServer did not answer in the time allowed (D12: SCK can hang)
    GT_ERR_INTERNAL = 5      // a framework returned something this shim does not model
};

// One-time process setup. Idempotent, safe from any thread, and must run before any other gt_ call.
gt_status gt_init(void);

// TCC state. Both are about the RESPONSIBLE process, which under `go run` is the terminal and not
// this binary (docs/ALTTAB-LESSONS.md section 5). Neither prompts the user.
int32_t gt_trusted(void);     // Accessibility
int32_t gt_can_record(void);  // Screen Recording

// ---------------------------------------------------------------------------
// Bitmap handles
// ---------------------------------------------------------------------------

// An opaque bitmap. Go never sees a CGImageRef it might mistake for memory the runtime understands;
// it sees this. Nothing produces one until P2.6 — the ownership rule is established here so that the
// task which finally allocates megabytes is not also the task inventing how they are freed.
typedef struct gt_image *gt_image_ref;

// Releases a bitmap. Safe on NULL, and NOT safe twice: the second call is a use-after-free, which is
// exactly why Go's wrapper clears its pointer rather than trusting the caller.
void gt_image_release(gt_image_ref img);

// How many handles are alive right now. The leak assertion for V6.4 — after a summon completes and
// the cache has evicted, this returns to the cache bound and not to something larger.
int64_t gt_image_live(void);

// ---------------------------------------------------------------------------
// Window enumeration
// ---------------------------------------------------------------------------

enum {
    // Titles are inline rather than pointers so the whole result is one flat block: no per-window
    // malloc in C, no second crossing to fetch strings, and nothing for Go to free. The cost is a
    // fixed 384 bytes per window, which at a few hundred windows is well under a megabyte.
    //
    // Truncation is on a character boundary, not a byte -- see copy_cfstring in shim.m. A title long
    // enough to hit this is already too long to render in a switcher tile.
    GT_TITLE_MAX = 256,
    GT_APPNAME_MAX = 128
};

// One window, as CGWindowList knows it. Deliberately NOT a mirror of core.Window: this struct carries
// what the WindowServer reports, and Go composes the core types from it. In particular there are no
// flag bits here. core.WindowFlags is frozen in internal/core/api.go and defining a second copy of
// those values in C is exactly the drift AGENTS.md warns about, so C reports facts (is it on screen,
// what layer, what alpha) and Go decides what they mean.
typedef struct {
    uint32_t id;         // CGWindowID. Not reuse-safe -- see core.WindowID.
    int32_t pid;         // owning process
    uint32_t layer;      // kCGWindowLayer. 0 is an ordinary window; see gt_window_list.
    float alpha;         // 0 means fully transparent, which is how some apps park a window
    int32_t on_screen;   // kCGWindowIsOnscreen
    uint16_t title_len;  // bytes in title, excluding the terminator. 0 means absent OR unpermitted.
    uint16_t app_len;
    char title[GT_TITLE_MAX];
    char app[GT_APPNAME_MAX];
} gt_window;

// Fills buf with up to cap windows in ONE crossing, and writes how many it stored to *out_n.
//
// *out_total receives how many windows there were, which is not always *out_n: when the buffer is too
// small the excess is dropped rather than truncated silently, and the caller grows and retries. A
// switcher that quietly forgets the window you were reaching for is worse than a slow one.
//
// Windows with kCGWindowLayer != 0 are excluded and are not counted in either number. That is the
// menubar, the Dock, shadows, overlays and the desktop -- things the API returns that are not windows
// anyone can switch to. Excluding them is a fact about this API, not a user preference; user
// preferences are core.Rules and were settled in P1.3.
//
// Titles require the Screen Recording grant. Without it kCGWindowName is absent for other
// applications' windows and title_len comes back 0 for all of them. That is not an error and is not
// reported as one: gt_can_record() is how a caller tells "no title" from "not allowed to see it".
gt_status gt_window_list(gt_window *buf, int32_t cap, int32_t *out_n, int32_t *out_total);

// ---------------------------------------------------------------------------
// Threading
// ---------------------------------------------------------------------------

// Runs a Go callback on the main queue and returns immediately. The token is a runtime/cgo.Handle;
// this file deliberately knows nothing else about it. See docs/ARCHITECTURE.md#threading: AppKit owns
// the main thread forever, and every UI mutation from a Go goroutine arrives through here.
void gt_dispatch_main(uintptr_t token);

#endif
