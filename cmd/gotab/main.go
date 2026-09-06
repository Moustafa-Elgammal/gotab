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

func grant(ok bool) string {
	if ok {
		return "granted"
	}
	return "MISSING"
}
