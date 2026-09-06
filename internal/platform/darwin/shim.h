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
// Threading
// ---------------------------------------------------------------------------

// Runs a Go callback on the main queue and returns immediately. The token is a runtime/cgo.Handle;
// this file deliberately knows nothing else about it. See docs/ARCHITECTURE.md#threading: AppKit owns
// the main thread forever, and every UI mutation from a Go goroutine arrives through here.
void gt_dispatch_main(uintptr_t token);

#endif
