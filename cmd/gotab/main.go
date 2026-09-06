// Command gotab is the GoTab window switcher.
//
// Phase 2: the platform layer links and can report what the process is allowed to do. It does not
// switch windows yet. See docs/ROADMAP.md for what is built and what is next.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
	"github.com/Moustafa-Elgammal/gotab/internal/platform/darwin"
)

// version is injected by scripts/build.sh via -ldflags.
var version = "dev"

func init() {
	// AppKit must own the process's original thread for its whole life, and Go only guarantees we are
	// on that thread inside main's init. Everything else runs on other goroutines and marshals back.
	// See docs/ARCHITECTURE.md#threading.
	runtime.LockOSThread()
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	check := flag.Bool("check", false, "report what this process is permitted to do, and exit")
	list := flag.Bool("list", false, "enumerate windows once and print them, then exit")
	raw := flag.Bool("raw", false, "with -list: use the CGWindowList candidate set instead of Accessibility")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	// Before any other platform call. gt_init touches the display subsystem, which CoreGraphics
	// requires of a non-AppKit process before its first capture (D12) — the failure mode is abort(),
	// not an error, so it is not something a later caller can handle.
	if err := darwin.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		os.Exit(1)
	}

	if *check {
		os.Exit(reportPermissions())
	}
	if *list {
		os.Exit(listWindows(*raw))
	}

	fmt.Fprintf(os.Stderr, "gotab %s: Phase 2 — the switcher is not wired up yet.\n", version)
	fmt.Fprintln(os.Stderr, "Try `gotab -check`, or see docs/ROADMAP.md for the current task.")
	os.Exit(1)
}

// reportPermissions prints the TCC state and returns the process exit code. Named grants rather than a
// single yes/no because the two degrade differently: without Screen Recording the switcher still works
// on titles and icons, and without Accessibility it cannot raise a window at all.
func reportPermissions() int {
	p := darwin.CheckPermissions()
	fmt.Printf("gotab %s\n", version)
	fmt.Printf("  Accessibility     %s   observe and raise windows\n", grant(p.Accessibility))
	fmt.Printf("  Screen Recording  %s   thumbnails, and titles of other apps\n", grant(p.ScreenRecording))
	if p.OK() {
		return 0
	}
	// The grant belongs to the *responsible* process, so running this under `go run` reports the
	// terminal's permissions rather than gotab's. Saying so here saves the next person the hour
	// ALTTAB-LESSONS section 5 documents.
	fmt.Fprintln(os.Stderr, "\nGrant these in System Settings > Privacy & Security.")
	fmt.Fprintln(os.Stderr, "Run from a built .app: under `go run` these report the terminal's grants, not gotab's.")
	return 1
}

// listWindows drives the enumeration and prints what came back. This is what verification looks like
// for the platform layer: it is the humble object, it is not unit-tested (ARCHITECTURE.md#testing), and
// since D16 defers the rest to Phase 6, running it and reading the output is the check.
func listWindows(raw bool) int {
	l := darwin.NewLister()
	src := darwin.FromAX
	if raw {
		src = darwin.FromCoreGraphics
	}

	ws, err := l.List(make([]core.Window, 0, 128), src)
	// Not `if err != nil { return }`: List returns the windows it did get alongside ErrTruncated, and
	// throwing away a partial list because it was labelled partial would be the wrong way round.
	if err != nil && !errors.Is(err, darwin.ErrTruncated) {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		if errors.Is(err, darwin.ErrNotTrusted) {
			fmt.Fprintln(os.Stderr, "Accessibility is what knows which windows are switchable (D19).")
			fmt.Fprintln(os.Stderr, "Try `gotab -list -raw` for the CoreGraphics candidate set instead.")
		}
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
	}

	fmt.Printf("%d windows (%s)\n\n", len(ws), sourceName(raw))
	fmt.Printf("  %-8s %-7s %-5s %-24s %s\n", "ID", "PID", "STATE", "APP", "TITLE")
	titled := 0
	for i, w := range ws {
		if w.Title != "" {
			titled++
		}
		fmt.Printf("  %-8d %-7d %-5s %-24.24s %.60s\n",
			w.ID, w.App, state(w.Flags, l, i, raw), w.AppName, w.Title)
	}

	// The distinction gt_window_list's header exists to preserve: no title and not allowed to read the
	// title look identical in the data, and only the grant tells them apart.
	if raw && titled == 0 && len(ws) > 0 && !darwin.CheckPermissions().ScreenRecording {
		fmt.Fprintln(os.Stderr, "\nEvery title is empty and Screen Recording is not granted — that is why.")
	}
	if !raw {
		reportOverlap(l, ws)
	}
	return 0
}

// reportOverlap cross-checks the two enumerations. Both key on CGWindowID, and the only reason the AX
// list has one at all is the private _AXUIElementGetWindow — so this is the check that the private
// symbol still returns numbers the rest of the system agrees with, rather than plausible garbage.
func reportOverlap(l *darwin.Lister, ax []core.Window) {
	cg, err := l.List(make([]core.Window, 0, 128), darwin.FromCoreGraphics)
	if err != nil {
		return
	}
	known := make(map[core.WindowID]bool, len(cg))
	for _, w := range cg {
		known[w.ID] = true
	}
	matched := 0
	for _, w := range ax {
		if known[w.ID] {
			matched++
		}
	}
	fmt.Printf("\n%d of %d AX windows carry an ID CoreGraphics also reports (of %d candidates).\n",
		matched, len(ax), len(cg))
}

func sourceName(raw bool) string {
	if raw {
		return "CoreGraphics candidates — not the switchable set, see D19"
	}
	return "Accessibility — the switchable set"
}

// state packs the flags into one column. "-" is not "false", it is "this source does not know".
func state(f core.WindowFlags, l *darwin.Lister, i int, raw bool) string {
	s := ""
	if f.Has(core.FlagOnScreen) {
		s += "o"
	}
	if f.Has(core.FlagMinimized) {
		s += "m"
	}
	if f.Has(core.FlagHidden) {
		s += "h"
	}
	if !raw && !l.Standard(i) {
		s += "d" // dialog, sheet or palette rather than a standard window
	}
	if s == "" {
		return "-"
	}
	return s
}

func onScreen(f core.WindowFlags) string {
	if f.Has(core.FlagOnScreen) {
		return "yes"
	}
	return "-"
}

func grant(ok bool) string {
	if ok {
		return "granted"
	}
	return "MISSING"
}
