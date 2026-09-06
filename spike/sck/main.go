// Spike P0.6: can Go capture a window thumbnail through ScreenCaptureKit?
//
// CGWindowListCreateImage is a compile error on macOS 15+ (D3), so this is the only path to a
// thumbnail. SCK is asynchronous and block-based; capture.m wraps it into blocking C calls so Go
// never has to hold an Objective-C block.
//
// The questions this answers, in the order they matter (docs/tasks/P0.6.md):
//  1. does the completion handler fire on a Go-owned thread with no NSRunLoop?
//  2. what does a capture cost, cold and warm?
//  3. does the CGImage release cleanly?
//  4. can the bitmap be downscaled at capture time rather than after?
//
// and one the task did not ask, which turned out to decide the design: a summon shows many
// thumbnails, so does issuing n captures concurrently cost n x one, or closer to one?
//
// Run it while several windows are on screen. Needs Screen Recording permission for whatever
// launched it (the terminal, not this binary) — without it, sck_refresh returns an error rather
// than an empty list, which is the signature to look for.
//
// ScreenCaptureKit is linked hard, not weakly, and that is a compromise rather than a choice: cgo
// rejects both `-weak_framework X` and `-Wl,-weak_framework,X` as invalid LDFLAGS. Weak linking does
// work, but only with CGO_LDFLAGS_ALLOW set in the environment of every build — which would break the
// plain `go run ./spike/sck` that AGENTS.md documents. A shipped binary needs the weak link (Go forces
// minos 11.0 and SCK arrives in 12.3), so `scripts/build.sh` will have to set that variable; a spike
// that only ever runs on this machine does not. See D12.
package main

/*
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics -framework ScreenCaptureKit
#include "capture.h"
#include <stdlib.h>
*/
import "C"

import (
	"flag"
	"fmt"
	"os"
	"time"
	"unsafe"
)

func mb(b int64) float64 { return float64(b) / 1024 / 1024 }

var errName = map[int32]string{
	0: "OK", 1: "UNAVAILABLE", 2: "NO_CONTENT", 3: "NO_WINDOW", 4: "CAPTURE_FAILED", 5: "TIMEOUT",
}

func capture(id uint32, w, h, timeoutMS int32) (C.sck_result, time.Duration) {
	var r C.sck_result
	t := time.Now()
	C.sck_capture(C.uint32_t(id), C.int32_t(w), C.int32_t(h), C.int32_t(timeoutMS), &r)
	return r, time.Since(t)
}

// captureMany issues len(ids) captures at once and waits for all of them.
//
// The result array is C memory, not a Go slice, on purpose: on timeout the completion blocks are
// still outstanding and will write into it whenever they do fire. Go's heap may not be written by C
// after the call returns, and freeing it would hand the blocks a dangling pointer — so a timed-out
// buffer is deliberately leaked. A spike may leak; P2.6 must instead keep the buffer alive for the
// lifetime of the capture, which is the design note this awkwardness exists to record.
func captureMany(ids []uint32, w, h, timeoutMS int32) ([]C.sck_result, time.Duration) {
	n := len(ids)
	cids := (*C.uint32_t)(C.malloc(C.size_t(n) * C.sizeof_uint32_t))
	defer C.free(unsafe.Pointer(cids))
	copy(unsafe.Slice((*uint32)(unsafe.Pointer(cids)), n), ids)

	res := (*C.sck_result)(C.malloc(C.size_t(n) * C.sizeof_sck_result))
	t := time.Now()
	C.sck_capture_many(cids, C.int32_t(n), C.int32_t(w), C.int32_t(h), C.int32_t(timeoutMS), res)
	d := time.Since(t)
	return unsafe.Slice(res, n), d
}

func main() {
	hold := flag.Int("hold", 0, "capture N thumbnails and hold them, for external measurement")
	tile := flag.Int("tile", 400, "max thumbnail edge in pixels")
	timeout := flag.Int("timeout", 10000, "ms to wait for each completion handler")
	flag.Parse()

	fmt.Printf("pid %d\n\n", os.Getpid())

	// CoreGraphics aborts the process on the first capture without this (D12). It is not optional
	// and there is no error to catch — the assertion kills the process.
	C.sck_init()

	// Step 1: can we see anything at all? A permission failure and an empty screen look nothing
	// alike, and telling them apart is most of the value of this spike.
	msg := (*C.char)(C.malloc(256))
	defer C.free(unsafe.Pointer(msg))
	t := time.Now()
	n := int32(C.sck_refresh(C.int32_t(*timeout), msg, 256))
	enum := time.Since(t)
	if n < 0 {
		fmt.Printf("sck_refresh FAILED after %v: %s\n", enum.Round(time.Millisecond), C.GoString(msg))
		fmt.Println("\nIf that mentions permission, grant Screen Recording to the terminal running this,")
		fmt.Println("then re-run. SCK reports a missing grant as an error, not as an empty window list.")
		os.Exit(1)
	}

	ids := make([]uint32, 64)
	nw := int32(C.sck_list_windows((*C.uint32_t)(unsafe.Pointer(&ids[0])), C.int32_t(len(ids))))
	ids = ids[:nw]
	fmt.Printf("enumerate        : %6.1f ms   %d shareable windows, %d worth showing\n",
		float64(enum.Microseconds())/1000, n, nw)
	if nw == 0 {
		fmt.Println("\nno capturable window on screen — open a normal window and re-run")
		os.Exit(1)
	}

	// Step 2: one capture, cold. This is the number that includes framework warm-up.
	r, cold := capture(0, int32(*tile), int32(*tile), int32(*timeout))
	if r.err != C.SCK_OK {
		fmt.Printf("\ncapture FAILED: %s — %s\n", errName[int32(r.err)], C.GoString(&r.msg[0]))
		os.Exit(1)
	}
	fmt.Printf("cold capture     : %6.1f ms   window %d -> %dx%d, %.2f MB backing store\n",
		float64(cold.Microseconds())/1000, uint32(r.window_id), int(r.width), int(r.height), mb(int64(r.bytes)))
	wid := uint32(r.window_id)
	C.sck_release(r.image)

	// Step 3: warm captures, serial. The cost of one thumbnail once the framework is up.
	const warmN = 10
	var total, worst time.Duration
	for i := 0; i < warmN; i++ {
		r2, d := capture(wid, int32(*tile), int32(*tile), int32(*timeout))
		if r2.err != C.SCK_OK {
			fmt.Printf("warm capture %d FAILED: %s — %s\n", i, errName[int32(r2.err)], C.GoString(&r2.msg[0]))
			os.Exit(1)
		}
		total += d
		if d > worst {
			worst = d
		}
		C.sck_release(r2.image)
	}
	serial := total / warmN
	fmt.Printf("warm capture     : %6.1f ms mean, %6.1f ms worst   (%d serial captures of one window)\n",
		float64(serial.Microseconds())/1000, float64(worst.Microseconds())/1000, warmN)

	// Step 4: the question that decides P2.6. A summon shows every window, so what matters is not
	// one capture but n of them. If SCK's asynchrony is real, n concurrent captures cost far less
	// than n serial ones; if it is not, the whole thumbnail-on-summon design has to change.
	fmt.Printf("\nconcurrent captures — %d serial would be ~%.0f ms:\n", nw,
		float64(int64(nw)*serial.Milliseconds()))
	for _, batch := range []int{2, 4, 8, len(ids)} {
		if batch > len(ids) {
			continue
		}
		rs, d := captureMany(ids[:batch], int32(*tile), int32(*tile), int32(*timeout))
		ok, failed := 0, ""
		for i := range rs {
			if rs[i].err == C.SCK_OK {
				ok++
				C.sck_release(rs[i].image)
			} else if failed == "" {
				failed = fmt.Sprintf("  first failure: %s — %s", errName[int32(rs[i].err)], C.GoString(&rs[i].msg[0]))
			}
		}
		fmt.Printf("  %2d at once     : %6.1f ms total, %5.1f ms each, %d/%d ok%s\n",
			batch, float64(d.Microseconds())/1000, float64(d.Microseconds())/1000/float64(batch), ok, batch, failed)
	}

	fmt.Printf("\nbudget: summon -> pixels is < 100 ms for the WHOLE path.\n")

	if *hold > 0 {
		// Hold N thumbnails so procmem can measure CG raster data from outside. Captured, not
		// synthesised: this is the real allocation shape P2.6 will produce.
		fmt.Printf("\nholding %d thumbnails for external measurement...\n", *hold)
		imgs := make([]C.CGImageRef, 0, *hold)
		var bytes int64
		for i := 0; i < *hold; i++ {
			r3, _ := capture(ids[i%len(ids)], int32(*tile), int32(*tile), int32(*timeout))
			if r3.err != C.SCK_OK {
				continue
			}
			imgs = append(imgs, r3.image)
			bytes += int64(r3.bytes)
		}
		fmt.Printf("holding %d images, %.1f MB declared. Measure now:\n", len(imgs), mb(bytes))
		fmt.Printf("  go run ./spike/procmem -pid %d -n 3 -every 2s\n", os.Getpid())
		time.Sleep(45 * time.Second)
		for _, im := range imgs {
			C.sck_release(im)
		}
		fmt.Println("released all. Measure again to see the drop.")
		time.Sleep(20 * time.Second)
	}
}
