// Command gotab is the GoTab window switcher.
//
// `gotab -switch` is the switcher: an ⌥⇥ event tap summons the panel, ⌥ held with ⇥ cycles the
// selection, releasing ⌥ raises the chosen window, Esc dismisses. Behind it the event loop enumerates,
// orders, lays out and draws, prefetches thumbnails, and restyles with the system appearance, all on a
// live AppKit run loop. Without the Accessibility grant the tap cannot install, and -switch falls back
// to a scripted summon so the render pipeline is still demonstrable. See docs/ROADMAP.md.
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
	switchMode := flag.Bool("switch", false, "show the real switcher panel (scripted summon; ^C to stop)")
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
	if *switchMode {
		os.Exit(runSwitcher())
	}

	fmt.Fprintf(os.Stderr, "gotab %s: pass -switch for the switcher, -watch for the text loop, or -check.\n", version)
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

// watchWindows runs the event loop until interrupted. Rescans are driven by P2.3b's Accessibility
// observers — one AXObserver per regular application, posting Loop.Rescan when a window is created,
// destroyed, focused, minimized or deminiaturized. A rescan is a hint, not a diff: the loop
// re-enumerates and reconciles, and D22's depth-1 latch coalesces a burst into one pass.
//
// The slow ticker below is a backstop, not polling-as-design. P2.3b measured that NSWorkspace's
// application-launch notification is delivered only by a running MAIN run loop, which cmd/gotab does
// not run until Phase 3 hands the thread to AppKit — so until then an application launched *after*
// gotab started is never observed, and a 2 s sweep is what catches its windows. Windows of
// already-running apps need no ticker; the observers post them immediately.
//
// Without the Accessibility grant StartObservers returns ErrNotTrusted and the fallback is the old
// 500 ms poll, so `-watch` still shows something and says why.
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

	// Observers first; the ticker's only remaining job is the NSWorkspace-without-a-main-run-loop gap.
	backstop := 2 * time.Second
	if err := darwin.StartObservers(l.Rescan); err != nil {
		backstop = 500 * time.Millisecond
		fmt.Fprintf(os.Stderr, "gotab: observers unavailable (%v) — polling every %s instead\n", err, backstop)
	} else {
		defer darwin.StopObservers()
		fmt.Println("observers active — window events drive the rescan.")
	}

	go func() {
		t := time.NewTicker(backstop)
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

// runSwitcher brings the whole Phase 3 pipeline up: the panel and its appearance, the event loop, the
// Accessibility observers, a thumbnail prefetcher, and — on the main thread — the AppKit run loop that
// makes all of it composite. It scripts one summon because nothing posts one yet (P0.2's hotkey is a
// spike, and giving ⌥⇥ its own path into the loop is a task of its own, the way internal/app was).
func runSwitcher() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := darwin.CreatePanel(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		return 1
	}
	// P3.4's finding: a pre-create ApplyTheme cannot be replayed, so it is called here, right after
	// CreatePanel and before the first show. Idempotent, no IPC.
	if err := darwin.ApplyTheme(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: theme: %v\n", err)
	}

	l := app.New(128)
	l.OnError = func(err error) { fmt.Fprintf(os.Stderr, "gotab: %v\n", err) }

	pf := darwin.NewPrefetcher(64)
	r := &panelRenderer{opts: core.LayoutOpts{Scale: 2, MaxCols: 7}, pf: pf}
	l.OnState = r.onState

	// Restyle on a Light/Dark flip. onChange runs on the main thread and ApplyTheme is non-blocking.
	if err := darwin.WatchAppearance(func() { darwin.ApplyTheme() }); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: appearance watch: %v\n", err)
	}

	if err := darwin.StartObservers(l.Rescan); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: observers unavailable (%v) — the panel will not track window changes\n", err)
	}

	loopDone := make(chan error, 1)
	go func() { loopDone <- l.Run(ctx) }()

	// The real trigger: the ⌥⇥ event tap posts into the loop. gotab decides summon-vs-cycle nowhere —
	// the tap thread already did (hotkey.h). Without the Accessibility grant the tap cannot install,
	// and the scripted demo stands in so the render pipeline is still exercised.
	hotkeyOK := false
	if err := darwin.StartHotkey(func(g darwin.Gesture) { postGesture(l, g) }); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: hotkey unavailable (%v) — scripting a demo summon instead\n", err)
		go demoDriver(ctx, l)
	} else {
		hotkeyOK = true
	}

	// Shutdown ordering matters: drain the prefetcher and observers while the main queue is still
	// being serviced, hide the panel, then break the run loop.
	go func() {
		<-loopDone
		darwin.StopHotkey()
		pf.Stop()
		darwin.StopObservers()
		darwin.StopWatchingAppearance()
		darwin.OnMain(darwin.HidePanel)
		darwin.StopRunLoop()
	}()

	if hotkeyOK {
		fmt.Println("switcher ready — hold ⌥ and press ⇥ to switch; ^C to quit.")
	} else {
		fmt.Println("switcher up — scripted summon, selection cycling; ^C to quit.")
	}
	darwin.RunLoop() // blocks on this (the main) thread until StopRunLoop
	return 0
}

// panelRenderer bridges the event loop's state to the platform renderer. Its onState runs on the loop
// goroutine; every AppKit call marshals to the main thread through darwin.OnMain.
type panelRenderer struct {
	opts   core.LayoutOpts
	pf     *darwin.Prefetcher
	shown  bool
	frames []core.Rect // reused across summons; core.Layout stays 0-alloc
}

func (r *panelRenderer) onState(m *core.Model, o *core.Order, sel core.Selection, visible bool) {
	if !visible {
		if r.shown {
			r.shown = false
			r.pf.Want(nil)
			darwin.OnMain(darwin.HidePanel)
		}
		return
	}

	n := o.Len()
	res := core.Layout(n, r.opts, r.frames[:0])
	r.frames = res.Tiles

	// Fresh slices per call: they are handed to an async OnMain closure and to pf.Want, and the loop
	// goroutine would otherwise overwrite a reused backing array before the main thread reads it.
	// One small allocation per state change; the summon-path 0-alloc goal is V6.8's to enforce here.
	tiles := make([]darwin.Tile, n)
	reqs := make([]darwin.ThumbRequest, n)
	for i := 0; i < n; i++ {
		w := m.At(o.Rows[i])
		f := res.Tiles[i]
		tiles[i] = darwin.Tile{
			X: f.X, Y: f.Y, W: f.W, H: f.H,
			Selected: w.ID == sel.ID,
			Title:    w.Title,
			Subtitle: w.AppName,
		}
		reqs[i] = darwin.ThumbRequest{
			Window: w.ID, Tile: i,
			Width: f.W * res.Scale, Height: f.H * res.Scale,
		}
	}

	pw, ph := res.Panel.W, res.Panel.H
	first := !r.shown
	r.shown = true
	darwin.OnMain(func() {
		if first {
			darwin.ShowPanel(tiles, pw, ph)
		} else {
			darwin.UpdatePanel(tiles)
		}
	})
	r.pf.Want(reqs)
}

// postGesture maps one tap gesture onto loop events. It runs on the hotkey tap's thread, so every
// call here must be non-blocking: Loop.Post is (D22). The first Tab of a hold arrives as a Summon*
// gesture and becomes Summon + Cycle — the panel opens with the *next* window already selected, which
// is what a single ⌥⇥ tap should switch to.
func postGesture(l *app.Loop, g darwin.Gesture) {
	switch g {
	case darwin.GestureSummonForward:
		l.Post(app.Event{Kind: app.Summon})
		l.Post(app.Event{Kind: app.Cycle, Dir: core.Forward})
	case darwin.GestureSummonBackward:
		l.Post(app.Event{Kind: app.Summon})
		l.Post(app.Event{Kind: app.Cycle, Dir: core.Backward})
	case darwin.GestureCycleForward:
		l.Post(app.Event{Kind: app.Cycle, Dir: core.Forward})
	case darwin.GestureCycleBackward:
		l.Post(app.Event{Kind: app.Cycle, Dir: core.Backward})
	case darwin.GestureActivate:
		l.Post(app.Event{Kind: app.Activate})
	case darwin.GestureDismiss:
		l.Post(app.Event{Kind: app.Dismiss})
	}
}

// demoDriver stands in for the hotkey when the Accessibility grant is missing: wait for the first
// enumeration, show the panel, then step the selection forward on a slow tick so the update path is
// visible too. ^C (ctx cancel) ends it; it deliberately never posts Activate, so running the demo
// does not reorder the user's windows.
func demoDriver(ctx context.Context, l *app.Loop) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(600 * time.Millisecond): // let doRescan land a first window set
	}
	l.Post(app.Event{Kind: app.Summon})

	t := time.NewTicker(1500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Post(app.Event{Kind: app.Cycle, Dir: core.Forward})
		}
	}
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
