// Scratch instrument for V6.5 / Phase 7 (D46).
//
// Default (no flag): enumerate, then call darwin.Raise on every window and report the result per
// origin — the D46 sweep that made the cg-only gap concrete.
//
// -v65: the parts of V6.5 an agent can drive without a human — the failure sentinels, and a
// minimized-window and hidden-app raise against throwaway TextEdit windows this program opens and
// closes. It steals focus briefly (activates Finder / TextEdit). Prints PASS/FAIL per check.
//
// CAVEAT (PLATFORM-LESSONS §5): TCC judges the *responsible process*. This spike is not
// build/GoTab.app, so a `go run` build — a fresh unsigned binary each time — is not the `app.gotab`
// identity that holds the grants; its AX messaging then times out instead of resolving, and the
// sentinels come back ErrTimeout rather than ErrNoWindow. Run it as `build/GoTab.app`'s peer (a
// stable binary that has itself been granted Accessibility) or treat -v65 as a human's turnkey, not
// an agent's. The default sweep needs only enumeration + a raise of AX-visible windows and is less
// sensitive.
//
// Kept through Phase 7; delete when V6.13 closes.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
	"github.com/Moustafa-Elgammal/gotab/internal/platform/darwin"
)

func main() {
	v65 := flag.Bool("v65", false, "run the agent-drivable half of V6.5: sentinels + minimized + hidden-app raise")
	flag.Parse()

	if err := darwin.Init(); err != nil {
		fmt.Println("init:", err)
		os.Exit(1)
	}
	if *v65 {
		os.Exit(runV65())
	}
	runSweep()
}

func runSweep() {
	e := darwin.NewEnumerator()
	ws, err := e.Enumerate(make([]core.Window, 0, 64))
	if err != nil {
		fmt.Println("enumerate:", err)
		return
	}
	origins := e.Origins()
	fmt.Printf("%d switchable windows\n\n", len(ws))
	for i, w := range ws {
		o := "?"
		if i < len(origins) {
			o = origins[i].String()
		}
		onscreen := "-"
		if w.Flags&core.FlagOnScreen != 0 {
			onscreen = "o"
		}
		rerr := darwin.Raise(w.ID)
		res := "OK — raised"
		if rerr != nil {
			res = rerr.Error()
		}
		fmt.Printf("  id=%-6d origin=%-4s screen=%s  %-18s | %s\n", w.ID, o, onscreen, w.AppName, res)
	}
}

// ---------------------------------------------------------------------------
// V6.5 — the agent-drivable checks
// ---------------------------------------------------------------------------

var pass, fail int

func check(name string, ok bool, detail string) {
	tag := "PASS"
	if !ok {
		tag = "FAIL"
		fail++
	} else {
		pass++
	}
	fmt.Printf("  [%s] %-34s %s\n", tag, name, detail)
}

func runV65() int {
	fmt.Print("V6.5 (agent-drivable half) — sentinels, minimized raise, hidden-app raise\n\n")

	p := darwin.CheckPermissions()
	fmt.Printf("this binary: Accessibility=%v ScreenRecording=%v — if AX calls time out below, it is\n"+
		"the TCC responsible-process trap (see the file header), not a code bug.\n\n",
		p.Accessibility, p.ScreenRecording)

	sentinels()
	minimizedRaise()
	hiddenAppRaise()

	fmt.Printf("\n%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		return 1
	}
	return 0
}

// sentinels: every action on an id no live window answers to must return ErrNoWindow (never panic,
// never a different sentinel). Uses 0, 1, and a large value that has never been a real window number.
func sentinels() {
	fmt.Println("failure sentinels (bogus ids → ErrNoWindow, no panic):")
	bogus := []core.WindowID{0, 1, 2, 999999999}
	acts := []struct {
		name string
		fn   func(core.WindowID) error
	}{
		{"Raise", darwin.Raise},
		{"Minimize", darwin.Minimize},
		{"Unminimize", darwin.Unminimize},
		{"Close", darwin.Close},
	}
	for _, a := range acts {
		allNoWindow := true
		var got error
		for _, id := range bogus {
			err := a.fn(id)
			if err == nil || !isErr(err, darwin.ErrNoWindow) {
				allNoWindow = false
				got = err
			}
		}
		detail := "all of {0,1,2,999999999} → ErrNoWindow"
		if !allNoWindow {
			detail = fmt.Sprintf("got %v", got)
		}
		check(a.name+" on a dead id", allNoWindow, detail)
	}
}

func minimizedRaise() {
	fmt.Println("\nminimized-window raise (unminimize first, then front):")
	defer closeTextEdit()
	if !openTextEditWindows(2) {
		check("minimized raise", false, "could not open TextEdit windows")
		return
	}
	// Minimize TextEdit's front window, then find which window that was by its AX FlagMinimized.
	osa(`tell application "TextEdit" to activate`)
	settle()
	osa(`tell application "TextEdit" to set miniaturized of front window to true`)
	settle()

	w, ok := findTextEditWindow(func(f core.WindowFlags) bool { return f&core.FlagMinimized != 0 })
	if !ok {
		check("minimized raise", false, "no minimized TextEdit window enumerated after miniaturize")
		return
	}
	// Make something else frontmost so 'raise + activate' is a real change (D25: two halves).
	osa(`tell application "Finder" to activate`)
	settle()

	err := darwin.Raise(w.ID)
	settle()
	stillMin := windowHasFlag(w.ID, core.FlagMinimized)
	front := frontApp()
	check("Raise(minimized) error", err == nil, errStr(err))
	check("window unminimized", !stillMin, fmt.Sprintf("FlagMinimized now %v", stillMin))
	check("TextEdit is frontmost", front == "TextEdit", "frontmost = "+front)
}

func hiddenAppRaise() {
	fmt.Println("\nhidden-app raise (unhide first, then front):")
	defer closeTextEdit()
	if !openTextEditWindows(1) {
		check("hidden-app raise", false, "could not open a TextEdit window")
		return
	}
	osa(`tell application "TextEdit" to activate`)
	settle()
	w, ok := findTextEditWindow(func(f core.WindowFlags) bool { return f&core.FlagMinimized == 0 })
	if !ok {
		check("hidden-app raise", false, "no visible TextEdit window to target")
		return
	}
	// Hide TextEdit (⌘H), then activate another app so it is genuinely backgrounded.
	osa(`tell application "System Events" to set visible of process "TextEdit" to false`)
	osa(`tell application "Finder" to activate`)
	settle()
	hidden := osaResult(`tell application "System Events" to get visible of process "TextEdit"`)
	check("TextEdit is hidden before raise", hidden == "false", "visible = "+hidden)

	err := darwin.Raise(w.ID)
	settle()
	vis := osaResult(`tell application "System Events" to get visible of process "TextEdit"`)
	front := frontApp()
	check("Raise(hidden-app) error", err == nil, errStr(err))
	check("TextEdit unhidden", vis == "true", "visible = "+vis)
	check("TextEdit is frontmost", front == "TextEdit", "frontmost = "+front)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func isErr(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error())
}

func errStr(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func settle() { time.Sleep(700 * time.Millisecond) }

func osa(script string) { _ = exec.Command("osascript", "-e", script).Run() }

func osaResult(script string) string {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "<err:" + err.Error() + ">"
	}
	return strings.TrimSpace(string(out))
}

func frontApp() string {
	return osaResult(`tell application "System Events" to get name of first process whose frontmost is true`)
}

func openTextEditWindows(n int) bool {
	osa(`tell application "TextEdit" to activate`)
	for i := 0; i < n; i++ {
		osa(`tell application "TextEdit" to make new document`)
	}
	settle()
	_, ok := findTextEditWindow(func(core.WindowFlags) bool { return true })
	return ok
}

func closeTextEdit() {
	osa(`tell application "TextEdit" to close every document saving no`)
}

func enumerate() []core.Window {
	e := darwin.NewEnumerator()
	ws, err := e.Enumerate(make([]core.Window, 0, 64))
	if err != nil {
		fmt.Println("enumerate:", err)
		return nil
	}
	return ws
}

func findTextEditWindow(pred func(core.WindowFlags) bool) (core.Window, bool) {
	for _, w := range enumerate() {
		if w.AppName == "TextEdit" && pred(w.Flags) {
			return w, true
		}
	}
	return core.Window{}, false
}

func windowHasFlag(id core.WindowID, flag core.WindowFlags) bool {
	for _, w := range enumerate() {
		if w.ID == id {
			return w.Flags&flag != 0
		}
	}
	return false // gone from the list — treat as "flag not set"
}
