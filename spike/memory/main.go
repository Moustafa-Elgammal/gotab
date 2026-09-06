// Spike P0.3 + P0.4: can Go hold window thumbnails and give the memory back?
//
// Tests the central claim in docs/ARCHITECTURE.md#the-memory-rule: CGImage bitmaps live outside
// the Go heap, so the Go GC cannot reclaim them and we must release them explicitly. If phys_footprint
// does not return to baseline after releaseAll(), the memory goal is not reachable this way.
package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#cgo CFLAGS: -Wno-deprecated-declarations
#include <CoreGraphics/CoreGraphics.h>
#include <mach/mach.h>
#include <stdlib.h>

// phys_footprint is what Activity Monitor shows as "Memory".
static int64_t footprint(void) {
    task_vm_info_data_t info;
    mach_msg_type_number_t count = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&info, &count) != KERN_SUCCESS) return -1;
    return (int64_t)info.phys_footprint;
}

static int64_t resident(void) {
    task_vm_info_data_t info;
    mach_msg_type_number_t count = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&info, &count) != KERN_SUCCESS) return -1;
    return (int64_t)info.resident_size;
}

// ONE crossing returns every window id: the batching discipline, not a call per window.
static int listWindows(uint32_t *out, int max) {
    CFArrayRef list = CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
    if (!list) return -1;
    int n = (int)CFArrayGetCount(list), k = 0;
    for (int i = 0; i < n && k < max; i++) {
        CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
        CFNumberRef num = (CFNumberRef)CFDictionaryGetValue(d, kCGWindowNumber);
        if (!num) continue;
        int wid = 0;
        CFNumberGetValue(num, kCFNumberIntType, &wid);
        out[k++] = (uint32_t)wid;
    }
    CFRelease(list);
    return k;
}

static CGImageRef *slots = NULL;
static int slotCount = 0;

// NOTE: CGWindowListCreateImage is *obsoleted* in macOS 15 (unavailable, not just deprecated), so real
// capture must go through ScreenCaptureKit. That is async/block-based and is its own task (P2.6).
// For THIS spike the question is only whether explicit release returns memory to the OS, and the origin
// of a CGImage does not affect that. So we allocate bitmaps of realistic thumbnail size instead.
static CGImageRef makeThumb(int w, int h) {
    CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(NULL, w, h, 8, w * 4, cs,
        kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little);
    CGColorSpaceRelease(cs);
    if (!ctx) return NULL;
    // Write incompressible noise straight into the backing store. A uniform fill lets CoreGraphics
    // share or compress the pages, which makes reclaim look free when it isn't.
    unsigned char *px = (unsigned char *)CGBitmapContextGetData(ctx);
    if (px) {
        size_t nbytes = (size_t)w * (size_t)h * 4;
        unsigned int s = 12345 + (unsigned int)w;
        for (size_t i = 0; i < nbytes; i++) { s = s * 1103515245u + 12345u; px[i] = (unsigned char)(s >> 16); }
    }
    CGImageRef img = CGBitmapContextCreateImage(ctx);
    CGContextRelease(ctx);
    return img;
}

static int captureAll(int n, int w, int h) {
    slots = (CGImageRef *)calloc(n, sizeof(CGImageRef));
    slotCount = 0;
    for (int i = 0; i < n; i++) {
        CGImageRef img = makeThumb(w, h);
        if (img) slots[slotCount++] = img;
    }
    return slotCount;
}

static int64_t bitmapBytes(void) {
    int64_t t = 0;
    for (int i = 0; i < slotCount; i++)
        if (slots[i]) t += (int64_t)CGImageGetBytesPerRow(slots[i]) * (int64_t)CGImageGetHeight(slots[i]);
    return t;
}

static void releaseAll(void) {
    for (int i = 0; i < slotCount; i++) if (slots[i]) CGImageRelease(slots[i]);
    free(slots); slots = NULL; slotCount = 0;
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"time"
)

func mb(b int64) float64 { return float64(b) / 1024 / 1024 }

func resident() int64 { return int64(C.resident()) }

func footprint() int64 {
	runtime.GC()
	debug.FreeOSMemory()
	return int64(C.footprint())
}

func main() {
	base := footprint()
	fmt.Printf("baseline footprint        : %6.1f MB   (Go runtime + cgo, no windows yet)\n", mb(base))

	const max = 512
	ids := make([]C.uint32_t, max)
	t := time.Now()
	n := int(C.listWindows(&ids[0], C.int(max)))
	enum := time.Since(t)
	if n < 0 {
		fmt.Println("window enumeration failed")
		return
	}
	// The first call pays framework load + a cold WindowServer round trip. The summon path only ever
	// sees the warm cost, so measure both or the budget is meaningless.
	var warm time.Duration
	for i := 0; i < 20; i++ {
		w := time.Now()
		C.listWindows(&ids[0], C.int(max))
		warm += time.Since(w)
	}
	warm /= 20
	fmt.Printf("enumerated %d windows      : %6.2f ms cold, %6.2f ms warm   (ONE cgo call)\n",
		n, float64(enum.Microseconds())/1000, float64(warm.Microseconds())/1000)

	// Retina thumbnail: 400x300 points at 2x = 800x600 RGBA ~= 1.9 MB each, AltTab's rough tile scale.
	const tw, th = 800, 600
	const count = 200 // ~366 MB: far too large to hide in measurement noise
	t = time.Now()
	got := int(C.captureAll(C.int(count), C.int(tw), C.int(th)))
	capt := time.Since(t)
	held := footprint()
	bmp := int64(C.bitmapBytes())

	fmt.Printf("allocated %d thumbnails    : %6.1f ms   (%dx%d RGBA each)\n",
		got, float64(capt.Microseconds())/1000, tw, th)
	fmt.Printf("  bitmap bytes (CG-owned) : %6.1f MB   <- invisible to the Go GC\n", mb(bmp))
	fmt.Printf("  footprint while held    : %6.1f MB   (+%.1f MB over baseline)\n", mb(held), mb(held-base))
	fmt.Printf("  resident  while held    : %6.1f MB\n", mb(resident()))

	C.releaseAll()
	after := footprint()
	delta := mb(after - base)
	fmt.Printf("after explicit release    : %6.1f MB footprint, %6.1f MB resident  (%+.1f MB vs baseline)\n",
		mb(after), mb(resident()), delta)

	fmt.Println()
	fmt.Println("INCONCLUSIVE - see docs/DECISIONS.md D4.")
	fmt.Printf("  %.1f MB of bitmaps produced only %.1f MB of measured growth, so this instrument\n", mb(bmp), mb(held-base))
	fmt.Println("  cannot see CoreGraphics memory. Release ran cleanly, but reclaim is UNPROVEN.")
	fmt.Println("  Blocked on P0.4a: build an instrument that reports the real number first.")
	_ = delta
}
