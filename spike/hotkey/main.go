// Spike P0.2: can Go capture ⌥⇥ with a CGEventTap, and how late is the keystroke?
//
// The last Phase 0 unknown. Budget is < 5 ms for the callback: a switcher whose hotkey feels dropped
// is worse than no switcher.
//
// The questions this answers (docs/tasks/P0.2.md):
//  1. can a tap be installed and driven from Go, ahead of the focused app?
//  2. how late is the event when we see it? (CGEventGetTimestamp makes this measurable, not inferred)
//  3. what does C -> Go cost on a tap callback thread the Go runtime has never seen?
//  4. can the event be swallowed so the app underneath never gets it?
//
// Accessibility is granted to the RESPONSIBLE process, which for `go run` is the terminal, not this
// binary (PLATFORM-LESSONS section 5). An ungranted tap installs fine and then never fires, so the grant
// is checked up front rather than left to look like a broken hotkey.
package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework ApplicationServices -framework CoreGraphics
#include "hotkey.h"
#include <stdlib.h>
*/
import "C"

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"
)

func init() {
	// The tap's run loop lives on this thread for the life of the process.
	runtime.LockOSThread()
}

// goEntered counts crossings. The exported function must stay trivial: it is the thing being measured.
var goEntered atomic.Int64

//export goHotkeyEntered
func goHotkeyEntered() { goEntered.Add(1) }

const (
	keyTab     = 48
	flagOption = 1 << 19 // kCGEventFlagMaskAlternate
	budgetMS   = 5.0
	synthPause = 60 * time.Millisecond
)

var errName = map[int32]string{0: "OK", 1: "NOT_TRUSTED", 2: "TAP_FAILED", 3: "TIMEOUT"}

func summarize(v []float64) (mean, p50, worst float64) {
	if len(v) == 0 {
		return 0, 0, 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	return sum / float64(len(s)), s[len(s)/2], s[len(s)-1]
}

func main() {
	n := flag.Int("n", 20, "hotkey presses to measure")
	manual := flag.Bool("manual", false, "wait for a human to press ⌥⇥ instead of synthesising it")
	timeout := flag.Int("timeout", 30000, "ms to wait for each press")
	flag.Parse()

	if C.hk_trusted() == 0 {
		fmt.Println("Accessibility is NOT granted to the responsible process.")
		fmt.Println("A tap would install and then never fire, which looks exactly like a broken hotkey.")
		fmt.Println("\nGrant it to the app running this (the terminal, not this binary):")
		fmt.Println("  System Settings > Privacy & Security > Accessibility")
		os.Exit(1)
	}
	fmt.Println("Accessibility: granted")

	msg := (*C.char)(C.malloc(256))
	defer C.free(unsafe.Pointer(msg))
	rc := int32(C.hk_start(C.uint32_t(keyTab), C.uint64_t(flagOption), msg, 256))
	if rc != C.HK_OK {
		fmt.Printf("hk_start failed: %s — %s\n", errName[rc], C.GoString(msg))
		os.Exit(1)
	}
	fmt.Printf("tap installed: session level, head insert, ⌥⇥ (keycode %d + Option)\n\n", keyTab)

	if *manual {
		fmt.Printf("Press ⌥⇥ (Option+Tab) %d times. The key is swallowed, so nothing else will see it.\n", *n)
		fmt.Printf("The first press is discarded — it carries one-off costs the user never feels twice.\n\n")
	}

	var deliver, rt, base []float64
	var firstRT float64
	reenabled, synthetic := 0, 0

	for i := 0; i < *n; i++ {
		if !*manual {
			// Synthetic presses let this run unattended. They enter through the same session tap as
			// a real key, so the delivery path being measured is the real one.
			go func() { time.Sleep(synthPause); C.hk_post(C.uint32_t(keyTab), C.uint64_t(flagOption)) }()
		}
		var s C.hk_sample
		if rc := int32(C.hk_wait(C.int32_t(*timeout), &s)); rc != C.HK_OK {
			fmt.Printf("press %d: %s\n", i, errName[rc])
			os.Exit(1)
		}
		if *manual {
			tag := "real"
			if s.synthetic != 0 {
				tag = "SYNTHETIC"
			}
			fmt.Printf("  %2d/%d  event -> callback %7.3f ms   C->Go->C %7.2f µs   (%s)\n",
				i+1, *n, float64(s.deliver_ms), float64(s.roundtrip_us), tag)
		}
		if i == 0 {
			firstRT = float64(s.roundtrip_us)
		} else {
			deliver = append(deliver, float64(s.deliver_ms))
			rt = append(rt, float64(s.roundtrip_us))
			base = append(base, float64(s.baseline_us))
		}
		if s.synthetic != 0 {
			synthetic++
		}
		if s.reenabled != 0 {
			reenabled++
		}
	}

	dm, dp, dw := summarize(deliver)
	rm, rp, rw := summarize(rt)
	bm, _, _ := summarize(base)

	fmt.Printf("captured %d presses, swallowed %d, tap saw %d events total\n",
		*n, int64(C.hk_swallowed()), int64(C.hk_seen()))
	fmt.Printf("crossings into Go: %d\n\n", goEntered.Load())

	fmt.Printf("  %-26s %8s %8s %8s\n", "", "mean", "p50", "worst")
	fmt.Printf("  %-26s %8.3f %8.3f %8.3f   (ms, budget %.0f)\n", "event -> callback", dm, dp, dw, budgetMS)
	fmt.Printf("  %-26s %8.3f %8.3f %8.3f   (µs)\n", "C -> Go -> C", rm, rp, rw)
	fmt.Printf("  %-26s %8.3f %8s %8s   (µs, two clock reads)\n", "  instrument baseline", bm, "-", "-")
	fmt.Printf("  %-26s %8.3f %8s %8s   (µs, first ever crossing)\n", "first crossing", firstRT, "-", "-")

	if reenabled > 0 {
		fmt.Printf("\ntap was disabled by the system and re-enabled %d time(s)\n", reenabled)
	}

	fmt.Printf("\nbudget: hotkey callback < %.0f ms\n", budgetMS)
	switch {
	case synthetic > 0:
		// A synthetic event's timestamp is stamped by the poster, not by the HID path, so the
		// delivery number measures our own two lines of code and nothing else. Refusing to score it
		// is the point: AGENTS.md records that Phase 0 already produced one false GATE PASS.
		fmt.Printf("INCONCLUSIVE against the budget: %d of %d events were synthetic.\n", synthetic, *n)
		fmt.Printf("  The %.3f ms mean above is real, but it is post -> tap ROUTING only: the event is\n", dm)
		fmt.Printf("  stamped in this process immediately before CGEventPost, so hardware, driver and the\n")
		fmt.Printf("  HID path are all excluded. Treat it as a lower bound on delivery, not as delivery.\n")
		fmt.Printf("  For the number the budget is about, type the key yourself:\n")
		fmt.Printf("      go run ./spike/hotkey -manual -n %d\n", *n)
		fmt.Printf("  The C -> Go crossing IS valid either way — same code path for both.\n")
	case dw < budgetMS:
		fmt.Printf("PASS: %.3f ms worst case, %.1f%% of budget\n", dw, dw/budgetMS*100)
	default:
		fmt.Printf("OVER BUDGET: %.3f ms worst case\n", dw)
	}
}
