// Spike P0.4a: build an instrument that can see CoreGraphics bitmap memory.
//
// docs/DECISIONS.md D4 recorded that 366 MB of CGImages showed as ~8 MB of growth, and left two
// candidate explanations: CoreGraphics never materialises the backing store, or the instruments are
// blind to it. This probe adds a third and tests all of them:
//
//	H1  the bytes are never allocated        -> CGDataProviderCopyData would return short
//	H2  the instruments cannot see them      -> vmmap would disagree with task_info
//	H3  the pages are IDENTICAL and macOS compresses them
//
// H3 is a flaw in the original test, not in the runtime: it seeded every bitmap with the same value,
// so all 200 buffers held byte-identical noise. The compressor collapses that to almost nothing, and
// phys_footprint counts compressed pages at their compressed size. This probe runs the same
// allocation twice - once with identical content, once with per-image unique content - so the two
// numbers can be compared directly.
package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <mach/mach.h>
#include <stdlib.h>

static int64_t phys(void) {
    task_vm_info_data_t i; mach_msg_type_number_t c = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&i, &c) != KERN_SUCCESS) return -1;
    return (int64_t)i.phys_footprint;
}
static int64_t resident(void) {
    task_vm_info_data_t i; mach_msg_type_number_t c = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&i, &c) != KERN_SUCCESS) return -1;
    return (int64_t)i.resident_size;
}
// compressed holds pages the memory compressor has squeezed. If H3 is right this climbs while
// phys_footprint stays flat.
static int64_t compressed(void) {
    task_vm_info_data_t i; mach_msg_type_number_t c = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&i, &c) != KERN_SUCCESS) return -1;
    return (int64_t)i.compressed;
}

static CGImageRef *slots = NULL;
static int slotCount = 0;
static int nullDataPtrs = 0;   // contexts whose CGBitmapContextGetData returned NULL
static int64_t bytesWritten = 0;

// seed varies per image when unique != 0, so the compressor cannot dedupe across buffers.
static CGImageRef makeThumb(int w, int h, unsigned int seed) {
    CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(NULL, w, h, 8, w * 4, cs,
        kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little);
    CGColorSpaceRelease(cs);
    if (!ctx) return NULL;
    unsigned char *px = (unsigned char *)CGBitmapContextGetData(ctx);
    if (px) {
        size_t n = (size_t)w * (size_t)h * 4;
        unsigned int s = seed;
        for (size_t i = 0; i < n; i++) { s = s * 1103515245u + 12345u; px[i] = (unsigned char)(s >> 16); }
        bytesWritten += (int64_t)n;
    } else {
        nullDataPtrs++;
    }
    CGImageRef img = CGBitmapContextCreateImage(ctx);
    CGContextRelease(ctx);
    return img;
}

static int allocAll(int n, int w, int h, int unique) {
    slots = (CGImageRef *)calloc(n, sizeof(CGImageRef));
    slotCount = 0;
    for (int i = 0; i < n; i++) {
        CGImageRef img = makeThumb(w, h, unique ? (unsigned int)(i * 2654435761u + 1) : 12345u);
        if (img) slots[slotCount++] = img;
    }
    return slotCount;
}

static int nullPtrCount(void) { return nullDataPtrs; }
static int64_t writtenBytes(void) { return bytesWritten; }
static void resetCounters(void) { nullDataPtrs = 0; bytesWritten = 0; }

// Sum the retrieved bytes. If the provider hands back zeros, this stays 0 while the LENGTH looks right.
static int64_t retrievedChecksum(void) {
    int64_t sum = 0;
    for (int i = 0; i < slotCount; i++) {
        if (!slots[i]) continue;
        CGDataProviderRef p = CGImageGetDataProvider(slots[i]);
        if (!p) continue;
        CFDataRef d = CGDataProviderCopyData(p);
        if (!d) continue;
        const UInt8 *b = CFDataGetBytePtr(d);
        CFIndex n = CFDataGetLength(d);
        for (CFIndex k = 0; k < n; k += 4096) sum += b[k];   // sample one byte per page
        CFRelease(d);
    }
    return sum;
}

static int64_t declaredBytes(void) {
    int64_t t = 0;
    for (int i = 0; i < slotCount; i++)
        if (slots[i]) t += (int64_t)CGImageGetBytesPerRow(slots[i]) * (int64_t)CGImageGetHeight(slots[i]);
    return t;
}

// H1: if the pixels were never materialised, this comes back short of declaredBytes().
static int64_t retrievableBytes(void) {
    int64_t t = 0;
    for (int i = 0; i < slotCount; i++) {
        if (!slots[i]) continue;
        CGDataProviderRef p = CGImageGetDataProvider(slots[i]);
        if (!p) continue;
        CFDataRef d = CGDataProviderCopyData(p);
        if (d) { t += (int64_t)CFDataGetLength(d); CFRelease(d); }
    }
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
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	count  = 200
	tw, th = 800, 600
)

func mb(b int64) float64 { return float64(b) / 1024 / 1024 }

type sample struct{ phys, resident, compressed int64 }

func measure() sample {
	runtime.GC()
	debug.FreeOSMemory()
	return sample{int64(C.phys()), int64(C.resident()), int64(C.compressed())}
}

func (s sample) String() string {
	return fmt.Sprintf("phys %6.1f MB   resident %6.1f MB   compressed %6.1f MB",
		mb(s.phys), mb(s.resident), mb(s.compressed))
}

// parseSize turns vmmap's "249.8M" / "2.7G" / "16K" into bytes.
func parseSize(s string) int64 {
	if s == "" {
		return 0
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G':
		mult, s = 1<<30, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f * float64(mult))
}

// vmmapTotals reads the process's own TOTAL row: virtual, resident, dirty.
// phys_footprint tracks DIRTY; clean-but-resident pages are charged to nobody, which is the
// distinction this whole probe exists to expose.
func vmmapTotals() (virtual, resident, dirty int64, ok bool) {
	out, err := exec.Command("vmmap", "--summary", strconv.Itoa(os.Getpid())).CombinedOutput()
	if err != nil {
		return 0, 0, 0, false
	}
	for _, l := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "TOTAL") || strings.Contains(t, "TOTAL, but") {
			continue
		}
		f := strings.Fields(t)
		if len(f) < 4 {
			continue
		}
		return parseSize(f[1]), parseSize(f[2]), parseSize(f[3]), true
	}
	return 0, 0, 0, false
}

// cgRaster reads vmmap's dedicated "CG raster data" row - the region type that holds CGImage
// backing store - plus the process's PEAK physical footprint. Instantaneous phys_footprint misses
// these bitmaps because macOS reclaims CG raster pages aggressively; peak does not.
func cgRaster() (virtual, resident int64, peak int64, ok bool) {
	out, err := exec.Command("vmmap", "--summary", strconv.Itoa(os.Getpid())).CombinedOutput()
	if err != nil {
		return 0, 0, 0, false
	}
	for _, l := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "Physical footprint (peak):") {
			peak = parseSize(strings.TrimSpace(strings.TrimPrefix(t, "Physical footprint (peak):")))
		}
		if strings.HasPrefix(t, "CG raster data") {
			f := strings.Fields(strings.TrimPrefix(t, "CG raster data"))
			if len(f) >= 2 {
				virtual, resident, ok = parseSize(f[0]), parseSize(f[1]), true
			}
		}
	}
	return virtual, resident, peak, ok
}

func vmmapLine() string {
	v, r, d, ok := vmmapTotals()
	if !ok {
		return "unavailable"
	}
	return fmt.Sprintf("virtual %7.1f MB   resident %7.1f MB   dirty %7.1f MB", mb(v), mb(r), mb(d))
}

func run(label string, unique bool) {
	fmt.Printf("\n=== %s ===\n", label)
	base := measure()
	_, baseRes, baseDirty, _ := vmmapTotals()
	fmt.Printf("  baseline  %v\n", base)
	fmt.Printf("            vmmap %s\n", vmmapLine())

	C.resetCounters()
	got := int(C.allocAll(C.int(count), C.int(tw), C.int(th), C.int(map[bool]int{true: 1, false: 0}[unique])))
	held := measure()
	_, heldRes, heldDirty, okv := vmmapTotals()
	declared := int64(C.declaredBytes())

	fmt.Printf("  allocated %d images, declared %.1f MB\n", got, mb(declared))
	fmt.Printf("  held      %v\n", held)
	fmt.Printf("            vmmap %s\n", vmmapLine())
	fmt.Printf("            growth: task_info phys %+.1f MB | vmmap resident %+.1f MB | vmmap dirty %+.1f MB\n",
		mb(held.phys-base.phys), mb(heldRes-baseRes), mb(heldDirty-baseDirty))
	if okv && heldRes-baseRes > declared*8/10 {
		fmt.Printf("  -> vmmap RESIDENT sees the bitmaps (%.0f%% of declared). task_info phys does not.\n",
			100*float64(heldRes-baseRes)/float64(declared))
	}

	if os.Getenv("DUMP") != "" {
		raw, _ := exec.Command("vmmap", "--summary", strconv.Itoa(os.Getpid())).CombinedOutput()
		fmt.Println("---- raw vmmap --summary ----")
		fmt.Println(string(raw))
		fmt.Println("---- end ----")
	}
	fmt.Printf("  contexts with NULL data pointer: %d of %d   bytes actually written: %.1f MB\n",
		int(C.nullPtrCount()), got, mb(int64(C.writtenBytes())))
	sum := int64(C.retrievedChecksum())
	if v, r, peak, okc := cgRaster(); okc {
		fmt.Printf("  vmmap \"CG raster data\": virtual %.1f MB, resident %.1f MB   (declared %.1f MB)\n",
			mb(v), mb(r), mb(declared))
		fmt.Printf("  physical footprint PEAK: %.1f MB  <- the bitmaps WERE resident; macOS reclaimed them\n", mb(peak))
	}
	fmt.Printf("  retrieved content checksum (1 byte/page): %d  %s\n", sum,
		map[bool]string{true: "<- ALL ZERO: provider returned empty buffer", false: "<- real data"}[sum == 0])
	retr := int64(C.retrievableBytes())
	fmt.Printf("  retrievable via data provider: %.1f MB of %.1f MB declared\n", mb(retr), mb(declared))
	if retr < declared*9/10 {
		fmt.Println("  -> H1 SUPPORTED: the pixels were never materialised")
	} else {
		fmt.Println("  -> H1 rejected: every declared byte is really there")
	}

	C.releaseAll()
	after := measure()
	_, afterRes, _, _ := vmmapTotals()
	fmt.Printf("  released  %v\n", after)
	fmt.Printf("            vmmap %s\n", vmmapLine())
	fmt.Printf("            vs baseline: task_info phys %+.1f MB | vmmap resident %+.1f MB\n",
		mb(after.phys-base.phys), mb(afterRes-baseRes))
}

func main() {
	fmt.Printf("%d images of %dx%d RGBA = %.1f MB declared\n",
		count, tw, th, float64(count)*float64(tw)*float64(th)*4/1024/1024)

	run("A: every bitmap IDENTICAL (what D4 accidentally measured)", false)
	run("B: every bitmap UNIQUE (compressor cannot dedupe)", true)

	fmt.Println("\nCompare A and B. If B shows the full growth and A does not, the D4 anomaly was")
	fmt.Println("the macOS memory compressor collapsing identical pages - a flaw in the test, not")
	fmt.Println("a blind instrument, and the memory rule in ARCHITECTURE.md holds as written.")
}
