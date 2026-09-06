// Command gotab is the GoTab window switcher.
//
// Phase 2: the platform layer links and can report what the process is allowed to do. It does not
// switch windows yet. See docs/ROADMAP.md for what is built and what is next.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/Moustafa-Elgammal/gotab/internal/app"
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
	raw := flag.Bool("raw", false, "list only the CoreGraphics candidate set (implies -list)")
	axOnly := flag.Bool("ax", false, "list only the Accessibility set (implies -list)")
	watch := flag.Bool("watch", false, "run the event loop and print the list as it changes; ^C to stop")
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
	if *list || *raw || *axOnly {
		os.Exit(listWindows(*raw, *axOnly))
	}
	if *watch {
		os.Exit(watchWindows())
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
func listWindows(raw, axOnly bool) int {
	if raw || axOnly {
		return listOneSource(raw)
	}

	e := darwin.NewEnumerator()
	ws, err := e.Enumerate(make([]core.Window, 0, 128))
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		if errors.Is(err, darwin.ErrNotTrusted) {
			fmt.Fprintln(os.Stderr, "Accessibility is what knows which windows are switchable (D19),")
			fmt.Fprintln(os.Stderr, "and without it the switcher could not raise one anyway.")
			fmt.Fprintln(os.Stderr, "`gotab -list -raw` shows the CoreGraphics candidates regardless.")
		}
		return 1
	}

	origins := e.Origins()
	fmt.Printf("%d switchable windows\n\n", len(ws))
	fmt.Printf("  %-8s %-7s %-5s %-5s %-24s %s\n", "ID", "PID", "FROM", "STATE", "APP", "TITLE")
	for i, w := range ws {
		fmt.Printf("  %-8d %-7d %-5s %-5s %-24.24s %.60s\n",
			w.ID, w.App, origins[i], state(w.Flags), w.AppName, w.Title)
	}

	if e.MissingRecovery() {
		fmt.Fprintln(os.Stderr, "\nScreen Recording is not granted, so windows Accessibility cannot see")
		fmt.Fprintln(os.Stderr, "(another Space, some accessory apps) are missing from this list. See D21.")
	}

	// P2.3c's contract: every titled layer-0 window is either in the list or excluded for a reason
	// the code can name. Printing the reasons is what makes that checkable rather than asserted.
	if ex := e.Excluded(); len(ex) > 0 {
		fmt.Printf("\n%d CoreGraphics windows excluded:\n", len(ex))
		byReason := map[string]int{}
		for _, x := range ex {
			byReason[x.Reason]++
		}
		for reason, n := range byReason {
			fmt.Printf("  %3d  %s\n", n, reason)
		}
		// The titled ones are the ones worth eyeballing: an untitled XPC service being dropped is
		// unremarkable, a titled window being dropped is a bug report waiting to happen.
		for _, x := range ex {
			if x.Title != "" {
				fmt.Printf("       titled, still excluded: %d %s — %s\n", x.ID, x.AppName, x.Title)
			}
		}
	}
	return 0
}

// listOneSource shows a single enumeration, for comparing against the join.
func listOneSource(raw bool) int {
	l := darwin.NewLister()
	src := darwin.FromAX
	name := "Accessibility — switchable, but misses windows it cannot see (D20)"
	if raw {
		src = darwin.FromCoreGraphics
		name = "CoreGraphics candidates — not the switchable set (D19)"
	}

	ws, err := l.List(make([]core.Window, 0, 128), src)
	if err != nil && !errors.Is(err, darwin.ErrTruncated) {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
	}

	fmt.Printf("%d windows (%s)\n\n", len(ws), name)
	fmt.Printf("  %-8s %-7s %-5s %-24s %s\n", "ID", "PID", "STATE", "APP", "TITLE")
	titled := 0
	for _, w := range ws {
		if w.Title != "" {
			titled++
		}
		fmt.Printf("  %-8d %-7d %-5s %-24.24s %.60s\n",
			w.ID, w.App, state(w.Flags), w.AppName, w.Title)
	}
	// The distinction gt_window_list's header exists to preserve: no title and not allowed to read the
	// title look identical in the data, and only the grant tells them apart.
	if raw && titled == 0 && len(ws) > 0 && !darwin.CheckPermissions().ScreenRecording {
		fmt.Fprintln(os.Stderr, "\nEvery title is empty and Screen Recording is not granted — that is why.")
	}
	return 0
}

// watchWindows runs the event loop until interrupted. Rescans are driven by a ticker, which is a
// stand-in and says so: P2.3b's Accessibility observers are what should post them, and they are
// blocked until this package exists. The ticker is deliberately in the command and not in the loop —
// polling is not the design, it is scaffolding for looking at the loop before the observers land.
//
// It doubles as P2.7's acceptance check for MRU stability: rescans run continuously, and if a
// re-enumeration disturbed the order the printed list would visibly churn. It does not.
func watchWindows() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	l := app.New(128)
	l.OnError = func(err error) { fmt.Fprintf(os.Stderr, "gotab: %v\n", err) }

	rescans := 0
	last := ""
	l.OnState = func(m *core.Model, o *core.Order, sel core.Selection, _ bool) {
		rescans++
		var b strings.Builder
		for i := 0; i < o.Len(); i++ {
			w := m.At(o.Rows[i])
			marker := " "
			if w.ID == sel.ID {
				marker = ">"
			}
			fmt.Fprintf(&b, "  %s %-8d %-24.24s %.50s\n", marker, w.ID, w.AppName, w.Title)
		}
		// Print only on change. A ticker that reprints an identical list every half second proves
		// nothing; a list that changes exactly when a window opens or closes proves the loop works.
		if s := b.String(); s != last {
			last = s
			fmt.Printf("\n[rescan %d] %d windows\n%s", rescans, m.Len(), s)
		}
	}

	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				l.Rescan()
			}
		}
	}()

	fmt.Println("watching — open or close a window to see the loop react. ^C to stop.")
	if err := l.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		return 1
	}
	fmt.Printf("\nstopped after %d state updates\n", rescans)
	return 0
}

// state packs the flags into one column. "-" is not "false", it is "no source knew".
func state(f core.WindowFlags) string {
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
	if s == "" {
		return "-"
	}
	return s
}

func grant(ok bool) string {
	if ok {
		return "granted"
	}
	return "MISSING"
}
