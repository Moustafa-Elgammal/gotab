// Command gotab is the GoTab window switcher.
//
// `gotab -switch` is the switcher: an ⌥⇥ event tap summons the panel, ⌥ held with ⇥ cycles the
// selection, releasing ⌥ raises the chosen window, Esc dismisses. Behind it the event loop enumerates,
// orders, lays out and draws, prefetches thumbnails, and restyles with the system appearance, all on a
// live AppKit run loop. Without the Accessibility grant the tap cannot install, and -switch falls back
// to a scripted summon so the render pipeline is still demonstrable. See docs/ROADMAP.md.
//
// The other flags are utilities: -settings, -permissions, -watch, -prefs, -check, and -check-update
// (ask the release feed whether a newer build is out). User-facing onboarding strings resolve through
// internal/i18n, keyed off GOTAB_LOCALE / LANG.
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
	"github.com/Moustafa-Elgammal/gotab/internal/i18n"
	"github.com/Moustafa-Elgammal/gotab/internal/platform/darwin"
	"github.com/Moustafa-Elgammal/gotab/internal/prefs"
	"github.com/Moustafa-Elgammal/gotab/internal/update"
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
	switchMode := flag.Bool("switch", false, "run the switcher: ⌥⇥ summons, ⌥ held cycles, release raises; ^C to stop")
	prefsMode := flag.Bool("prefs", false, "print effective preferences; with Key=Value args, set them and exit")
	settingsMode := flag.Bool("settings", false, "open the settings window")
	permMode := flag.Bool("permissions", false, "check the grants, explain any that are missing, and wait for them")
	checkUpdate := flag.Bool("check-update", false, "ask the release feed whether a newer GoTab is out, and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	// Locale before any user-facing string. Pure Go, no platform dependency, so
	// it does not matter that this runs before darwin.Init. See docs/DECISIONS.md D40.
	initLocale()

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
	if *checkUpdate {
		os.Exit(runCheckUpdate())
	}
	if *permMode {
		os.Exit(runPermissions())
	}
	if *list || *raw || *axOnly {
		os.Exit(listWindows(*raw, *axOnly))
	}
	if *watch {
		os.Exit(watchWindows())
	}
	if *prefsMode {
		os.Exit(runPrefs(flag.Args()))
	}
	if *settingsMode {
		os.Exit(runSettings())
	}
	if *switchMode {
		os.Exit(runSwitcher())
	}

	// Double-clicked from Finder there are no flags, and the switcher is what the user wants — a
	// usage message they cannot see would just be an app that does nothing. From a shell, print help.
	if runningInBundle() {
		os.Exit(runSwitcher())
	}
	fmt.Fprintf(os.Stderr, "gotab %s: pass -switch, -settings, -permissions, -watch, -prefs, -check or -check-update.\n", version)
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
	// PLATFORM-LESSONS section 5 documents.
	fmt.Fprintln(os.Stderr, "\nGrant these in System Settings > Privacy & Security:")
	if !p.Accessibility {
		fmt.Fprintln(os.Stderr, "  "+paneAccessibility)
	}
	if !p.ScreenRecording {
		fmt.Fprintln(os.Stderr, "  "+paneScreenRecord)
	}
	fmt.Fprintln(os.Stderr, "`gotab -permissions` walks you through it and waits for the grant.")
	fmt.Fprintln(os.Stderr, "Run from a built .app: under `go run` these report the terminal's grants, not gotab's.")
	return 1
}

// runCheckUpdate is `gotab -check-update`: ask the release feed whether a newer build exists and
// print the answer. A network problem never fails the process — a feed that cannot be reached just
// means "not now". A development build is skipped outright: "git describe" output and the literal
// "dev" have nothing to compare against a release number. See internal/update and D39.
func runCheckUpdate() int {
	if version == "dev" {
		fmt.Println(i18n.T("update.devBuild", version))
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	res, err := update.Check(ctx, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("update.checkFailed", err))
		return 0
	}
	if res.Available {
		fmt.Println(i18n.T("update.available", res.Latest, res.Current, res.URL))
		if res.Notes != "" {
			fmt.Println("  " + res.Notes)
		}
		return 0
	}
	fmt.Println(i18n.T("update.upToDate", version))
	return 0
}

// stderrIsTTY reports whether stderr is a terminal. gotab launched from Finder has no terminal, and a
// modal alert is the only channel; from a shell the alert would steal focus for information the user
// can read inline, so the terminal path prints instead.
func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runningInBundle reports whether this executable is the one inside GoTab.app, as opposed to a bare
// `go build` binary or `go run`. It decides the no-flags default: the switcher for a Finder launch, a
// usage message for a shell.
func runningInBundle() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

// resourcesDir is where the bundled locale files live: Contents/Resources for the binary inside
// GoTab.app, and ./resources from a source checkout. i18n.Load reads <dir>/<locale>.lproj/gotab.json.
func resourcesDir() string {
	if exe, err := os.Executable(); err == nil {
		if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
			return exe[:i] + ".app/Contents/Resources"
		}
	}
	return "resources"
}

// initLocale picks the UI language and loads its catalog. GOTAB_LOCALE wins; otherwise LANG, which on
// a login shell reads like "de_DE.UTF-8" (i18n normalizes it). Proper macOS system-locale detection
// wants CFLocale — cgo the i18n package deliberately does without — so it is a later addition on the
// darwin side (D40). English needs no file and is always present, so a load error is only a warning.
func initLocale() {
	tag := os.Getenv("GOTAB_LOCALE")
	if tag == "" {
		tag = os.Getenv("LANG")
	}
	i18n.SetLocale(tag)
	if err := i18n.Load(resourcesDir()); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: locale %s: %v\n", i18n.Locale(), err)
	}
}

const (
	paneAccessibility = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	paneScreenRecord  = "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
)

// promptForGrants asks the user to grant what is missing. From Finder (no TTY, window server present)
// it is a modal alert whose "Open System Settings" opens the pane; from a shell, or when there is no
// window server, it is a printed explanation plus opening the pane directly. Returns whether to
// proceed to the wait loop — false only if the user quit the modal.
func promptForGrants(needAX, needSR bool) bool {
	if !stderrIsTTY() {
		if shown, proceed := darwin.PromptPermissions(needAX, needSR); shown {
			return proceed
		}
		// No window server — fall through to the printed path.
	}
	fmt.Fprintln(os.Stderr, i18n.T("perm.cli.grantPrompt"))
	if needAX {
		fmt.Fprintln(os.Stderr, "  "+i18n.T("perm.cli.accessibilityLabel")+"  "+paneAccessibility)
	}
	if needSR {
		fmt.Fprintln(os.Stderr, "  "+i18n.T("perm.cli.screenRecordingLabel")+"  "+paneScreenRecord)
	}
	if needAX {
		darwin.OpenPrivacyPane("accessibility")
	} else {
		darwin.OpenPrivacyPane("screen-recording")
	}
	return true
}

// runPermissions is `gotab -permissions`: report the grants, and for any that are missing prompt,
// open System Settings, and wait — recovering the moment the grant appears, no relaunch. ^C stops the
// wait.
func runPermissions() int {
	p := darwin.CheckPermissions()
	fmt.Printf("gotab %s\n", version)
	fmt.Printf("  Accessibility     %s\n", grant(p.Accessibility))
	fmt.Printf("  Screen Recording  %s\n", grant(p.ScreenRecording))
	if p.OK() {
		fmt.Println("\nboth grants are in place.")
		return 0
	}

	if !promptForGrants(!p.Accessibility, !p.ScreenRecording) {
		return 1 // user chose Quit
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Println("\nwaiting for the grant(s) — toggle them in System Settings; ^C to stop.")
	for last := p; ctx.Err() == nil; {
		time.Sleep(750 * time.Millisecond)
		q := darwin.CheckPermissions()
		if q.Accessibility && !last.Accessibility {
			fmt.Println("  Accessibility granted.")
		}
		if q.ScreenRecording && !last.ScreenRecording {
			fmt.Println("  Screen Recording granted.")
		}
		last = q
		if q.OK() {
			fmt.Println("done — both grants are in place.")
			return 0
		}
	}
	return 1
}

// ensurePermissions gates the switcher on the grants it needs. Accessibility is mandatory — without it
// gotab cannot enumerate, raise, or tap the hotkey. Screen Recording is optional (a warning, then it
// runs degraded). A missing mandatory grant is prompted, System Settings is opened, and gotab polls
// until it appears — recovering without a relaunch. macOS may relaunch gotab itself on an
// Accessibility grant; the fresh process then passes here and never prompts.
func ensurePermissions(ctx context.Context) bool {
	p := darwin.CheckPermissions()
	if p.Accessibility {
		if !p.ScreenRecording {
			fmt.Fprintln(os.Stderr, i18n.T("perm.cli.screenRecordingOff"))
		}
		return true
	}

	if !promptForGrants(true, !p.ScreenRecording) {
		return false // user chose Quit
	}

	fmt.Fprintln(os.Stderr, i18n.T("perm.cli.waitingAccessibility"))
	deadline := time.Now().Add(5 * time.Minute)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(750 * time.Millisecond)
		if darwin.CheckPermissions().Accessibility {
			fmt.Fprintln(os.Stderr, i18n.T("perm.cli.accessibilityGranted"))
			return true
		}
	}
	if ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, i18n.T("perm.cli.accessibilityStillMissing"))
	}
	return false
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
	// -watch is a raw view of the enumeration and the loop, so it shows everything the switcher's
	// filter (P4.4) would hide. `-switch` is the filtered one.
	l.Rules = core.Rules{ShowMinimized: true, ShowHidden: true, ShowOtherSpace: true}

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

// runSwitcher brings the whole switcher up: the panel and its appearance, the event loop, the
// Accessibility observers, a thumbnail prefetcher, the ⌥⇥ tap, and — on the main thread — the AppKit
// run loop that makes all of it composite. Settings come from the CFPreferences domain (P4.1).
func runSwitcher() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Accessibility is mandatory; without it every part below degrades to nothing. Onboard and wait
	// rather than starting a switcher that cannot switch (P4.3).
	if !ensurePermissions(ctx) {
		return 1
	}

	p := prefs.Load(darwin.Prefs{})

	if err := darwin.CreatePanel(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		return 1
	}
	// P3.4's finding: a pre-create ApplyTheme cannot be replayed, so it is called here, right after
	// CreatePanel and before the first show. Idempotent, no IPC. SetAppearance first so a forced
	// Light/Dark from prefs takes effect on this first apply.
	darwin.SetAppearance(string(p.Appearance))
	if err := darwin.ApplyTheme(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: theme: %v\n", err)
	}

	l := app.New(128)
	l.OnError = func(err error) { fmt.Fprintf(os.Stderr, "gotab: %v\n", err) }
	l.Rules = p.Rules() // the filter checkboxes from the settings window take effect here (P4.4)

	pf := darwin.NewPrefetcher(p.ThumbnailCacheSize)
	r := &panelRenderer{opts: p.LayoutOpts(), pf: pf}
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
	if err := darwin.StartHotkey(p.HotkeyKeyCode, p.HotkeyModifiers, func(g darwin.Gesture) { postGesture(l, g) }); err != nil {
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

	// Size the layout for the display the panel will actually land on — the one under the mouse,
	// which gt_panel_show also uses — so the margin scales right and thumbnails are captured at the
	// display's real backing scale (P4.4; this retires the hard-coded Scale: 2 of P4.1).
	sw, sh, scale := darwin.ActiveScreen()
	r.opts.Screen = core.Rect{W: sw, H: sh}
	r.opts.Scale = scale

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

// runSettings is `gotab -settings`: a native window over the same CFPreferences domain the CLI and
// `defaults` use. Each control change is one "Key=Value" that prefs.Set parses and Save persists. It
// is its own process — a running `gotab -switch` re-reads its settings only on the next launch.
func runSettings() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store := darwin.Prefs{}
	p := prefs.Load(store)

	onAssign := func(kv string) {
		if err := p.Set(kv); err != nil {
			fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
			return
		}
		if err := p.Save(store); err != nil {
			fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
			return
		}
		// The recorder sends HotkeyKeyCode then HotkeyModifiers; refresh the label once, after both.
		if strings.HasPrefix(kv, "HotkeyModifiers") {
			darwin.SettingsSetHotkey(darwin.HotkeyDisplay(p.HotkeyKeyCode, p.HotkeyModifiers))
		}
	}

	v := darwin.SettingsValues{
		ShowMinimized:  p.ShowMinimized,
		ShowHidden:     p.ShowHidden,
		ShowOtherSpace: p.ShowOtherSpace,
		ActiveAppOnly:  p.ActiveAppOnly,
		BlockedApps:    strings.Join(p.BlockedApps, ", "),
		Appearance:     string(p.Appearance),
		MaxColumns:     p.MaxColumns,
		ThumbnailCache: p.ThumbnailCacheSize,
		HotkeyDisplay:  darwin.HotkeyDisplay(p.HotkeyKeyCode, p.HotkeyModifiers),
	}

	darwin.OnSettingsClosed(darwin.StopRunLoop)
	if err := darwin.OpenSettings(v, onAssign); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		return 1
	}

	go func() {
		<-ctx.Done()
		darwin.OnMain(darwin.CloseSettings)
		darwin.StopRunLoop()
	}()

	fmt.Println("settings open — close the window or ^C to finish.")
	darwin.RunLoop()
	return 0
}

// runPrefs is `gotab -prefs`: with no arguments it prints the effective settings (defaults overlaid
// with whatever the CFPreferences domain holds); with Key=Value arguments it applies them, writes the
// whole record back, and prints the result. The domain is the same one `defaults read/write app.gotab`
// addresses, so either tool can drive it until the settings UI (P4.2) exists.
func runPrefs(assignments []string) int {
	store := darwin.Prefs{}
	p := prefs.Load(store)

	if len(assignments) == 0 {
		fmt.Printf("gotab %s — effective preferences:\n%s", version, p)
		fmt.Fprintln(os.Stderr, "\nset one:  gotab -prefs MaxColumns=5 Appearance=dark")
		return 0
	}

	for _, a := range assignments {
		if err := p.Set(a); err != nil {
			fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
			return 1
		}
	}
	if err := p.Save(store); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		return 1
	}
	fmt.Printf("saved. effective preferences:\n%s", p)
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
