// Spike P0.1: can Go put a borderless switcher panel on screen, and what does it cost?
//
// The < 100 ms summon budget is the one the product feel rests on, and it is the only Phase 0 target
// that a user would ever notice directly. P3.1 is designed against whatever this finds.
//
// The questions this answers (docs/tasks/P0.1.md):
//  1. does a borderless, non-activating panel show from a Go process that is not a bundled .app?
//  2. what does summon -> pixels cost, cold and warm?
//  3. is "the call returned" the same as "the frame is on screen"? (No. That is the point.)
//  4. does summoning steal focus from the app being switched away from?
//
// AppKit owns the process's first thread for its whole life, so main is locked to it in init and the
// measurement loop runs there — see docs/ARCHITECTURE.md#threading. This spike therefore does the
// opposite of the real app: it drives a nested run loop from Go rather than handing the thread to
// AppKit and marshalling back. That is fine for measuring and wrong for building.
package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore
#include "panel.h"
*/
import "C"

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
)

func init() {
	// AppKit must own the process's original thread, and Go only guarantees we are on it here.
	runtime.LockOSThread()
}

var errName = map[int32]string{0: "OK", 1: "NO_SCREEN", 2: "TIMEOUT"}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p / 100 * float64(len(sorted)-1))
	return sorted[i]
}

type stat struct{ mean, p50, worst float64 }

func summarize(v []float64) stat {
	if len(v) == 0 {
		return stat{}
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	return stat{sum / float64(len(s)), percentile(s, 50), s[len(s)-1]}
}

func main() {
	n := flag.Int("n", 20, "warm summon cycles to measure")
	tiles := flag.Int("tiles", 8, "tiles drawn in the single content view")
	hold := flag.Int("hold", 0, "ms to leave the panel up each cycle (use with -n 1 to look at it)")
	timeout := flag.Int("timeout", 2000, "ms to wait for a frame to commit")
	flag.Parse()

	if rc := int32(C.panel_init(C.int32_t(*tiles))); rc != C.PANEL_OK {
		fmt.Printf("panel_init failed: %s\n", errName[rc])
		os.Exit(1)
	}
	fmt.Printf("panel created: borderless, non-activating, %d tiles in one view\n\n", *tiles)

	// Cold: the first summon carries first-ever draw, font loading and the window's first backing
	// store. The real app pays this once at launch, not on the user's first ⌥⇥ — provided it summons
	// once at startup, which is the same conclusion D5 reached for enumeration.
	var s C.panel_sample
	C.panel_cycle(C.int32_t(*timeout), C.int32_t(*hold), &s)
	if s.err != C.PANEL_OK {
		fmt.Printf("cold summon failed: %s\n", errName[int32(s.err)])
		os.Exit(1)
	}
	fmt.Printf("cold summon      : call %6.2f ms   draw %6.2f ms   commit %6.2f ms   next-turn %6.2f ms   (%d draws)\n",
		float64(s.call_ms), float64(s.draw_ms), float64(s.commit_ms), float64(s.turn_ms), int(s.draws))

	var call, draw, commit, turn []float64
	stolen, noDraw := 0, 0
	for i := 0; i < *n; i++ {
		var w C.panel_sample
		C.panel_cycle(C.int32_t(*timeout), C.int32_t(*hold), &w)
		if w.err != C.PANEL_OK {
			fmt.Printf("warm summon %d failed: %s\n", i, errName[int32(w.err)])
			os.Exit(1)
		}
		call = append(call, float64(w.call_ms))
		draw = append(draw, float64(w.draw_ms))
		if w.draws == 0 {
			noDraw++
		}
		commit = append(commit, float64(w.commit_ms))
		turn = append(turn, float64(w.turn_ms))
		if C.panel_stole_focus() != 0 {
			stolen++
		}
	}

	cs, ds, ms, ts := summarize(call), summarize(draw), summarize(commit), summarize(turn)
	fmt.Printf("\nwarm summons (%d cycles)\n", *n)
	fmt.Printf("  %-22s %7s %7s %7s\n", "", "mean", "p50", "worst")
	fmt.Printf("  %-22s %7.2f %7.2f %7.2f\n", "call returned", cs.mean, cs.p50, cs.worst)
	fmt.Printf("  %-22s %7.2f %7.2f %7.2f\n", "drawRect: ran", ds.mean, ds.p50, ds.worst)
	fmt.Printf("  %-22s %7.2f %7.2f %7.2f\n", "frame committed", ms.mean, ms.p50, ms.worst)
	fmt.Printf("  %-22s %7.2f %7.2f %7.2f\n", "next main-queue turn", ts.mean, ts.p50, ts.worst)

	fmt.Printf("\nfocus: the frontmost app changed on %d of %d summons\n", stolen, *n)
	// If this is not 0, the warm number is the cost of re-ordering a cached backing store, not of
	// producing a frame — a different and much easier question than the one the budget asks.
	fmt.Printf("redraw: %d of %d warm summons produced no drawRect: call\n", noDraw, *n)

	// The honest verdict uses the committed frame, not the call. Worst case, not mean: a switcher
	// that is usually fast and occasionally not is one the user stops trusting.
	budget := 100.0
	fmt.Printf("\nbudget: summon -> pixels < %.0f ms for the WHOLE path; this is the panel alone.\n", budget)
	if ms.worst < budget {
		fmt.Printf("panel cost is %.2f ms worst case — %.0f%% of budget, %.1f ms left for everything else.\n",
			ms.worst, ms.worst/budget*100, budget-ms.worst)
	} else {
		fmt.Printf("OVER BUDGET on its own: %.2f ms worst case.\n", ms.worst)
	}
}
