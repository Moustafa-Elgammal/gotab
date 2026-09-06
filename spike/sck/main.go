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
	"os/exec"
	"strconv"
	"strings"
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

// vmstat is the P0.4b instrument, pointed at this process.
//
// spike/procmem is the general version and reads the same four rows; this is a deliberate third copy
// of the parsing, for the same reason procmem duplicates memprobe's: spikes are throwaway probes, not
// a library. Sampling from inside is not a convenience here, it is required — a capture/release cycle
// is ~50 ms and an external sampler cannot be told when a cycle boundary happened, so it would read
// the middle of one. From in here every sample is taken with zero images held, by construction.
type vmstat struct {
	ioVirtual, ioResident int64
	cgVirtual             int64
	footprint, peak       int64
}

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

func readVM() (vmstat, error) {
	var v vmstat
	out, err := exec.Command("vmmap", "--summary", strconv.Itoa(os.Getpid())).CombinedOutput()
	if err != nil {
		return v, fmt.Errorf("vmmap: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	for _, l := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "Physical footprint (peak):"):
			v.peak = parseSize(strings.TrimSpace(strings.TrimPrefix(t, "Physical footprint (peak):")))
		case strings.HasPrefix(t, "Physical footprint:"):
			v.footprint = parseSize(strings.TrimSpace(strings.TrimPrefix(t, "Physical footprint:")))
		case strings.HasPrefix(t, "CG raster data"):
			if f := strings.Fields(strings.TrimPrefix(t, "CG raster data")); len(f) >= 2 {
				v.cgVirtual = parseSize(f[0])
			}
		case strings.HasPrefix(t, "IOSurface"):
			// Columns: VIRTUAL RESIDENT DIRTY SWAPPED VOLATILE NONVOL EMPTY COUNT.
			if f := strings.Fields(t); f[0] == "IOSurface" && len(f) >= 3 {
				v.ioVirtual, v.ioResident = parseSize(f[1]), parseSize(f[2])
			}
		}
	}
	return v, nil
}

// runCycles is P0.4b: capture and release n times, and see whether anything is left behind.
//
// The acceptance criterion is no net growth, but "growth from the very first sample" is the wrong
// reading and would fail a healthy process: the first capture brings up ScreenCaptureKit, its XPC
// connection and the shared surface pool, and none of that is a leak. So two numbers are reported —
// growth including that one-off warm-up, and growth after it, which is the one that means anything.
// A leak of one thumbnail per cycle would be tens of MB by cycle 100 and cannot hide in either.
//
// It ends with a positive control, because "no growth" and "measuring the wrong row" print the same
// zeros. See the comment on that block.
func runCycles(ids []uint32, n, tile, timeoutMS int) {
	const (
		warmup      = 10 // cycles charged to framework start-up rather than to the steady state
		controlHold = 20 // images held at once by the positive control, after the cycles are done
	)

	fmt.Printf("\nP0.4b — %d capture/release cycles, rotating over %d window(s).\n", n, len(ids))
	fmt.Printf("Numbered rows are sampled with zero images held, so a rising IOSurface row is a leak.\n")
	fmt.Printf("The two rows after them are the control, and hold images on purpose.\n\n")
	fmt.Printf("%7s  %19s  %10s  %19s\n", "", "IOSurface", "CG raster", "footprint")
	fmt.Printf("%7s  %9s %9s  %10s  %9s %9s\n", "cycle", "virtual", "resident", "virtual", "now", "peak")

	samples := map[string]vmstat{}
	sample := func(label string) (vmstat, bool) {
		v, err := readVM()
		if err != nil {
			fmt.Printf("%7s  -- unreadable: %v\n", label, err)
			return v, false
		}
		samples[label] = v
		fmt.Printf("%7s  %8.1fM %8.1fM  %9.1fM  %8.1fM %8.1fM\n", label,
			mb(v.ioVirtual), mb(v.ioResident), mb(v.cgVirtual), mb(v.footprint), mb(v.peak))
		return v, true
	}
	cycleSample := func(c int) { sample(strconv.Itoa(c)) }

	every := n / 10
	if every < 1 {
		every = 1
	}
	cycleSample(0)

	failed := 0
	for i := 1; i <= n; i++ {
		r, _ := capture(ids[(i-1)%len(ids)], int32(tile), int32(tile), int32(timeoutMS))
		if r.err != C.SCK_OK {
			// D12 saw one capture time out unreproducibly in ~10 runs. That is SCK being SCK; it
			// must not abort a 100-cycle run, but it does have to be counted and reported.
			failed++
			continue
		}
		C.sck_release(r.image)
		if i == warmup || i%every == 0 || i == n {
			cycleSample(i)
		}
	}

	fmt.Printf("\n%d cycles completed, %d captures failed\n", n-failed, failed)

	first, haveFirst := samples[strconv.Itoa(0)]
	last, haveLast := samples[strconv.Itoa(n)]
	base, haveBase := samples[strconv.Itoa(warmup)]
	if !haveFirst || !haveLast || !haveBase {
		// Differencing a missing sample against a real one would read as a confident several-hundred-MB
		// swing in whichever direction the gap happens to fall. Refuse instead.
		fmt.Println("INCONCLUSIVE: a sample needed for the comparison was not readable")
		return
	}
	fmt.Printf("including framework warm-up (cycle 0 -> %d): IOSurface %+.1f MB, footprint %+.1f MB\n",
		n, mb(last.ioVirtual-first.ioVirtual), mb(last.footprint-first.footprint))
	growth := last.ioVirtual - base.ioVirtual
	fmt.Printf("steady state    (cycle %d -> %d): IOSurface %+.1f MB, footprint %+.1f MB\n",
		warmup, n, mb(growth), mb(last.footprint-base.footprint))

	// The positive control, and the reason this run is allowed to conclude anything at all.
	//
	// Every numbered row above reads 0.0M, which is what a clean release looks like — and is also
	// exactly what a blind instrument looks like. That is not a hypothetical: D12's finding was that
	// P0.4a's instrument watched a row SCK output never touches, and cheerfully reported a leak-free
	// 7 MB process that was holding 330 MB. So hold thumbnails deliberately and require the row to
	// move. If it does not, the no-growth result above is measuring nothing, whatever its deltas say.
	imgs := make([]C.CGImageRef, 0, controlHold)
	var declared int64
	for i := 0; i < controlHold; i++ {
		r, _ := capture(ids[i%len(ids)], int32(tile), int32(tile), int32(timeoutMS))
		if r.err != C.SCK_OK {
			continue
		}
		imgs = append(imgs, r.image)
		declared += int64(r.bytes)
	}
	held, okHeld := sample("held")
	for _, im := range imgs {
		C.sck_release(im)
	}
	freed, okFreed := sample("freed")

	moved := held.ioVirtual - last.ioVirtual
	fmt.Printf("\ncontrol: %d images held, %.1f MB declared — IOSurface %+.1f MB, then %+.1f MB on release\n",
		len(imgs), mb(declared), mb(moved), mb(freed.ioVirtual-held.ioVirtual))

	switch {
	case failed > 0 && n-failed < n/2:
		fmt.Printf("\nINCONCLUSIVE: only %d of %d cycles actually captured anything\n", n-failed, n)
	case len(imgs) == 0 || !okHeld || !okFreed:
		fmt.Println("\nINCONCLUSIVE: the control could not be taken, so the instrument is unproven here")
	case moved <= 0:
		fmt.Printf("\nINCONCLUSIVE: holding %d images (%.1f MB declared) did not move the IOSurface row.\n",
			len(imgs), mb(declared))
		fmt.Println("  This instrument cannot see ScreenCaptureKit output — the same way P0.4a's could not")
		fmt.Println("  (D12) — so the no-growth result above says nothing about leaks.")
	case growth > 0:
		fmt.Printf("\nLEAK: IOSurface grew %.1f MB over %d cycles after warm-up\n", mb(growth), n-warmup)
	default:
		fmt.Printf("\nPASS: no IOSurface growth across %d cycles after warm-up (%+.1f MB)\n",
			n-warmup, mb(growth))
	}
}

func main() {
	cycles := flag.Int("cycles", 0, "P0.4b: capture/release N times and check for growth, then exit")
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

	if *cycles > 0 {
		// Before the timing steps below, not after: their captures would land in the cycle-0
		// baseline and hide exactly the growth this is looking for.
		runCycles(ids, *cycles, *tile, *timeout)
		return
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
