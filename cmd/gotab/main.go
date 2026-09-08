// Command gotab is the GoTab window switcher.
//
// `gotab -switch` is the switcher: an ⌥⇥ event tap summons the panel, ⌥ held with ⇥ cycles the
// selection, releasing ⌥ raises the chosen window, Esc dismisses. Behind it the event loop enumerates,
// orders, lays out and draws, prefetches thumbnails, and restyles with the system appearance, all on a
// live AppKit run loop. It never blocks on a permission grant: a first run shows one combined ask,
// once, and the switcher comes up regardless — ⌥⇥ arms itself once Accessibility lands (D55). See
// docs/ROADMAP.md.
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
	"math"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
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
	timingMode := flag.Bool("timing", false, "V6.3 scaffold: drive headless summons and report summon→first-frame latency, then exit")
	timingN := flag.Int("timing-n", 30, "with -timing: how many summons to measure")
	demoMode := flag.Bool("demo", false, "with -switch: skip the hotkey, script a summon that holds — for an offscreen / accessibility-tree inspection")
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
	if *switchMode || *demoMode {
		os.Exit(runSwitcher(*demoMode))
	}
	if *timingMode {
		os.Exit(runTiming(*timingN))
	}

	// Double-clicked from Finder there are no flags, and the switcher is what the user wants — a
	// usage message they cannot see would just be an app that does nothing. From a shell, print help.
	if runningInBundle() {
		os.Exit(runSwitcher(false))
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

// promptForGrants asks the user to grant what is missing, for `gotab -permissions`. From Finder (no
// TTY, window server present) it is a modal alert whose "Open System Settings" opens the pane; from a
// shell, or when there is no window server, it is a printed explanation plus opening the pane
// directly. Returns whether to proceed to the wait loop — false only if the user chose Quit.
//
// The switcher's own first-run onboarding is onboardPermissions, not this: it never blocks and its
// dismiss button is "Not Now", not "Quit".
func promptForGrants(needAX, needSR bool) bool {
	if !stderrIsTTY() {
		if shown, proceed := darwin.PromptPermissions(needAX, needSR, false); shown {
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

// prefOnboarded records that the first-run permission prompt has already been shown, so it appears
// exactly once (D55). It is cleared again whenever both grants are in place, which re-arms the prompt
// for a later revoke. Stored straight in the CFPreferences domain — it is app state, not a user
// setting, so it stays out of internal/prefs' schema.
const prefOnboarded = "PermissionsOnboarded"

// onboardPermissions is the switcher's first-run permission ask (P8.2 / D55). One combined prompt
// names the two grants GoTab needs —
//
//	Accessibility     — to enumerate windows and raise the one you pick
//	Screen Recording  — for window titles and the live thumbnails
//
// — shown once, and not again unless a grant is later lost. It never blocks and never quits the
// switcher: whatever the user does the menu bar is up, and armSwitchHotkey arms ⌥⇥ from a background
// poll the moment Accessibility lands.
//
// From a shell (stderr is a TTY) it prints the System Settings deep links rather than steal focus
// with a modal (D36). From Finder it defers the modal onto the first run-loop turn via OnMain: GoTab
// is an Accessory app and its alert does not reliably come forward before -[NSApp run] is dequeuing
// events (D52).
func onboardPermissions(perm darwin.Permissions) {
	store := darwin.Prefs{}

	if perm.OK() {
		if shown, _ := store.Bool(prefOnboarded); shown {
			store.SetBool(prefOnboarded, false)
			_ = store.Sync()
		}
		return
	}

	if stderrIsTTY() {
		fmt.Fprintln(os.Stderr, i18n.T("perm.cli.grantPrompt"))
		if !perm.Accessibility {
			fmt.Fprintln(os.Stderr, "  "+i18n.T("perm.cli.accessibilityLabel")+"  "+paneAccessibility)
		}
		if !perm.ScreenRecording {
			fmt.Fprintln(os.Stderr, "  "+i18n.T("perm.cli.screenRecordingLabel")+"  "+paneScreenRecord)
		}
		return
	}

	if shown, _ := store.Bool(prefOnboarded); shown {
		// Asked once already. Note it quietly; the menu bar and armSwitchHotkey carry recovery.
		if !perm.Accessibility {
			fmt.Fprintln(os.Stderr, i18n.T("perm.cli.waitingAccessibility"))
		} else {
			fmt.Fprintln(os.Stderr, i18n.T("perm.cli.screenRecordingOff"))
		}
		return
	}
	store.SetBool(prefOnboarded, true)
	_ = store.Sync()

	needAX, needSR := !perm.Accessibility, !perm.ScreenRecording
	darwin.OnMain(func() { darwin.PromptPermissions(needAX, needSR, true) })
}

// armSwitchHotkey installs the ⌥⇥ event tap that drives the switcher, without ever blocking startup.
//
//   - demo: skip the tap and script summons (the -demo inspection path).
//   - no Accessibility grant: StartHotkey returns ErrNotTrusted — the tap cannot install, and an
//     ungranted one would install clean and then never fire. Rather than gate the whole switcher on
//     it (the menu bar must still come up, and the panel the moment the grant lands), poll
//     CheckPermissions in the background and arm the tap as soon as Accessibility appears — no
//     relaunch (D36's recovery model, made non-blocking — D55).
//   - any other StartHotkey failure: fall back to a scripted demo so the render path still works.
func armSwitchHotkey(ctx context.Context, l *app.Loop, keyCode, modifiers int, demo bool) {
	if demo {
		fmt.Fprintln(os.Stderr, "gotab: -demo — scripted summon, no hotkey (panel summons and holds)")
		go demoDriver(ctx, l)
		return
	}

	start := func() error {
		return darwin.StartHotkey(keyCode, modifiers, func(g darwin.Gesture) { postGesture(l, g) })
	}

	switch err := start(); {
	case err == nil:
		fmt.Println("switcher ready — hold ⌥ and press ⇥ to switch; ^C to quit.")
		return
	case !errors.Is(err, darwin.ErrNotTrusted):
		fmt.Fprintf(os.Stderr, "gotab: hotkey unavailable (%v) — scripting a demo summon instead\n", err)
		go demoDriver(ctx, l)
		return
	}

	fmt.Println("switcher up — waiting for Accessibility; ⌥⇥ arms itself the moment it is granted. ^C to quit.")
	go func() {
		t := time.NewTicker(750 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !darwin.CheckPermissions().Accessibility {
					continue
				}
				if e := start(); e != nil {
					fmt.Fprintf(os.Stderr, "gotab: hotkey still unavailable after the Accessibility grant (%v)\n", e)
					return
				}
				fmt.Fprintln(os.Stderr, i18n.T("perm.cli.accessibilityGranted"))
				fmt.Println("switcher ready — hold ⌥ and press ⇥ to switch; ^C to quit.")
				return
			}
		}
	}()
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
	fmt.Printf("%d switchable windows (current Space %d)\n\n", len(ws), darwin.CurrentSpace())
	fmt.Printf("  %-8s %-7s %-6s %-5s %-5s %-24s %s\n", "ID", "PID", "SPACE", "FROM", "STATE", "APP", "TITLE")
	for i, w := range ws {
		fmt.Printf("  %-8d %-7d %-6d %-5s %-5s %-24.24s %.60s\n",
			w.ID, w.App, w.Space, origins[i], state(w.Flags), w.AppName, w.Title)
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
func runSwitcher(demo bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Neither grant gates startup any more (D55). The switcher always comes up — menu bar, panel,
	// run loop — and recovers ⌥⇥ and thumbnails without a relaunch as the grants land. perm is read
	// once here; armSwitchHotkey and the prefetcher re-check as they go.
	perm := darwin.CheckPermissions()

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

	// First-run permission onboarding: one combined ask, shown once, never blocking (D55). Queued
	// before the menu bar so its alert is the first thing on the run-loop's first turn. -demo is an
	// inspection path — no System Settings side effects.
	if !demo {
		onboardPermissions(perm)
	}

	// The real trigger: the ⌥⇥ event tap posts into the loop. gotab decides summon-vs-cycle nowhere —
	// the tap thread already did (hotkey.h). Without the Accessibility grant the tap cannot install;
	// armSwitchHotkey then arms it from a background poll once the grant lands, and for -demo it
	// scripts summons instead.
	armSwitchHotkey(ctx, l, p.HotkeyKeyCode, p.HotkeyModifiers, demo)

	// The menu-bar status item (P8.1). GoTab is LSUIElement — no Dock tile, no app menu — so without
	// this a switcher installed from Finder has no visible surface: Settings and Quit are reachable
	// only from a terminal. "Settings…" launches `gotab -settings` as its own process (the standalone
	// entry point, which owns its window / run loop / Regular policy) so this process stays a clean
	// Accessory; "Quit GoTab" cancels ctx, the same path as ^C.
	darwin.OnMain(func() {
		if err := darwin.InstallMenuBar("GoTab "+version, openSettingsProcess, func() { stop() }); err != nil {
			fmt.Fprintf(os.Stderr, "gotab: menu bar unavailable (%v)\n", err)
		}
	})

	// Shutdown ordering matters: drain the prefetcher and observers while the main queue is still
	// being serviced, hide the panel, then break the run loop.
	go func() {
		<-loopDone
		darwin.StopHotkey()
		pf.Stop()
		darwin.StopObservers()
		darwin.StopWatchingAppearance()
		darwin.OnMain(darwin.RemoveMenuBar)
		darwin.OnMain(darwin.HidePanel)
		darwin.StopRunLoop()
	}()

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

// demoDriver stands in for the hotkey on the -demo path (and if StartHotkey fails for a reason other
// than a missing grant): wait for the first enumeration, show the panel, then step the selection
// forward on a slow tick so the update path is visible too. ^C (ctx cancel) ends it; it deliberately
// never posts Activate, so running the demo does not reorder the user's windows.
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

// runTiming is the V6.3 scaffold (docs/tasks/V6.3.md): the whole switcher render pipeline —
// CreatePanel, the event loop, the prefetcher, the renderer — minus the hotkey and the permission
// gate, driven by timingDriver instead of a real ⌥⇥. It measures summon → the panel's first frame
// reaching the render server against the < 100 ms budget, then exits. Not wired into the shipped
// switcher; nothing here runs unless -timing is passed.
func runTiming(n int) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p := prefs.Load(darwin.Prefs{})

	if err := darwin.CreatePanel(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: %v\n", err)
		return 1
	}
	darwin.SetAppearance(string(p.Appearance))
	if err := darwin.ApplyTheme(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: theme: %v\n", err)
	}

	l := app.New(128)
	l.OnError = func(err error) { fmt.Fprintf(os.Stderr, "gotab: %v\n", err) }
	l.Rules = p.Rules()

	pf := darwin.NewPrefetcher(p.ThumbnailCacheSize)
	r := &panelRenderer{opts: p.LayoutOpts(), pf: pf}
	l.OnState = r.onState

	// Event-driven enumeration must be live so a summon lays out a realistic tile count — but its
	// cold cost is paid here, at launch, not on a summon (D5).
	if err := darwin.StartObservers(l.Rescan); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: observers unavailable (%v)\n", err)
	}

	loopDone := make(chan error, 1)
	go func() { loopDone <- l.Run(ctx) }()

	go timingDriver(ctx, l, n, stop)

	go func() {
		<-loopDone
		pf.Stop()
		darwin.StopObservers()
		darwin.OnMain(darwin.HidePanel)
		darwin.StopRunLoop()
	}()

	perm := darwin.CheckPermissions()
	fmt.Printf("gotab: timing %d summons (Accessibility %s, Screen Recording %s) — no ⌥⇥, driven headless\n",
		n, grantWord(perm.Accessibility), grantWord(perm.ScreenRecording))
	darwin.RunLoop()
	return 0
}

// timingDriver arms the panel, posts a Summon, waits for the render-server hand-off, dismisses, and
// repeats. t0 is taken at the Post — "gesture recognised" — so the delta covers the whole summon
// path: the loop hop, selection reconcile, core.Layout, the marshal to the main thread, and the draw
// and CA commit. Sample 0 is the cold summon; the rest are warm.
func timingDriver(ctx context.Context, l *app.Loop, n int, stop context.CancelFunc) {
	defer stop() // cancel ctx → the loop returns → the shutdown goroutine tears down the run loop

	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second): // let the first enumeration land (its cost is not a summon's)
	}

	committed := make(chan struct{}, 1)
	darwin.TimingOnCommit(func() {
		select {
		case committed <- struct{}{}:
		default:
		}
	})
	defer darwin.TimingOnCommit(nil)

	deltas := make([]time.Duration, 0, n)
	for i := 0; i < n && ctx.Err() == nil; i++ {
		select { // drop any stale signal from a previous iteration
		case <-committed:
		default:
		}
		darwin.TimingArm()
		t0 := time.Now()
		l.Post(app.Event{Kind: app.Summon})
		select {
		case <-committed:
			deltas = append(deltas, time.Since(t0))
		case <-time.After(2 * time.Second):
			fmt.Fprintf(os.Stderr, "gotab: timing: summon %d never committed\n", i+1)
		}
		l.Post(app.Event{Kind: app.Dismiss})
		time.Sleep(150 * time.Millisecond) // give the dismiss its own turn; start the next from rest
	}
	reportTiming(deltas, n)
}

// reportTiming prints the cold summon on its own and the warm distribution against the < 100 ms
// budget (V6.3). Times are Go monotonic; the "commit" edge is the CATransaction completion block,
// which is the frame handed to the render server — the same edge spike/panel used (D13).
func reportTiming(deltas []time.Duration, want int) {
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	fmt.Printf("\nV6.3 — summon → first frame (CA commit): %d/%d samples\n", len(deltas), want)
	if len(deltas) == 0 {
		fmt.Println("  no samples — every summon timed out (Accessibility granted? window server up?)")
		return
	}
	fmt.Printf("  cold (summon 1):  %6.2f ms\n", ms(deltas[0]))
	warm := append([]time.Duration(nil), deltas[1:]...)
	if len(warm) == 0 {
		return
	}
	sort.Slice(warm, func(i, j int) bool { return warm[i] < warm[j] })
	pct := func(p float64) time.Duration {
		i := int(math.Ceil(p/100*float64(len(warm)))) - 1
		if i < 0 {
			i = 0
		}
		return warm[i]
	}
	p95 := pct(95)
	fmt.Printf("  warm (%d):  min %.2f  median %.2f  p95 %.2f  max %.2f ms\n",
		len(warm), ms(warm[0]), ms(pct(50)), ms(p95), ms(warm[len(warm)-1]))
	verdict := "PASS"
	if ms(p95) >= 100 {
		verdict = "FAIL — send back to the phase that owns the cost (V6.3)"
	}
	fmt.Printf("  budget: warm p95 < 100 ms  →  %s\n", verdict)
}

func grantWord(ok bool) string {
	if ok {
		return "granted"
	}
	return "missing"
}

// openSettingsProcess launches `gotab -settings` as its own process. Settings is already a standalone
// entry point that owns its window, its run loop and its (Regular) activation policy, so spawning it
// keeps the switcher process a clean Accessory and cannot take the switcher down if it misbehaves. A
// running `gotab -switch` re-reads its settings only on the next launch — the same as `gotab
// -settings` from a terminal. Called on the main thread from the menu-bar action; Start does not
// block. Errors go to stderr — a menu click that does nothing is the worst outcome.
func openSettingsProcess() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotab: settings: %v\n", err)
		return
	}
	cmd := exec.Command(exe, "-settings")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "gotab: settings: %v\n", err)
		return
	}
	go cmd.Wait() // reap it when the settings window closes; no zombie
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
