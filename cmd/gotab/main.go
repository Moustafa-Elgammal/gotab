// Command gotab is the GoTab window switcher.
//
// Phase 2: the platform layer links and can report what the process is allowed to do. It does not
// switch windows yet. See docs/ROADMAP.md for what is built and what is next.
package main

import (
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
		os.Exit(listWindows())
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

// listWindows drives P2.2 and prints what came back. This is what verification looks like for the
// platform layer: it is the humble object, it is not unit-tested (ARCHITECTURE.md#testing), and since
// D16 defers the rest to Phase 6, running it and reading the output is the check.
func listWindows() int {
	ws, err := darwin.NewLister().List(make([]core.Window, 0, 128))
	// Not `if err != nil { return }`: List returns the windows it did get alongside ErrTruncated, and
	// throwing away a partial list because it was labelled partial would be the wrong way round.
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
	}

	titled := 0
	for _, w := range ws {
		if w.Title != "" {
			titled++
		}
	}

	fmt.Printf("%d windows\n\n", len(ws))
	fmt.Printf("  %-8s %-7s %-3s %-24s %s\n", "ID", "PID", "ON", "APP", "TITLE")
	for _, w := range ws {
		fmt.Printf("  %-8d %-7d %-3s %-24.24s %.60s\n",
			w.ID, w.App, onScreen(w.Flags), w.AppName, w.Title)
	}

	// The distinction the header of gt_window_list exists to preserve: no title and not allowed to
	// read the title look identical in the data, and only the grant tells them apart.
	if titled == 0 && len(ws) > 0 && !darwin.CheckPermissions().ScreenRecording {
		fmt.Fprintln(os.Stderr, "\nEvery title is empty and Screen Recording is not granted — that is why.")
		fmt.Fprintln(os.Stderr, "Titles of other apps' windows need it; AX titles are the fallback (P2.3).")
	}
	if err != nil {
		return 1
	}
	return 0
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
