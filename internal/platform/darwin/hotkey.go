package darwin

/*
#include "hotkey.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Gesture is one step of the Option+Tab switch, as the event tap recognised it. cmd/gotab maps these
// onto internal/app events; this package does not know internal/app exists. The tap thread has
// already decided summon-vs-cycle, so a handler is a stateless switch.
type Gesture int

const (
	GestureSummonForward  Gesture = C.GT_HK_SUMMON_FWD
	GestureSummonBackward Gesture = C.GT_HK_SUMMON_BWD
	GestureCycleForward   Gesture = C.GT_HK_CYCLE_FWD
	GestureCycleBackward  Gesture = C.GT_HK_CYCLE_BWD
	GestureActivate       Gesture = C.GT_HK_ACTIVATE
	GestureDismiss        Gesture = C.GT_HK_DISMISS
)

// The tap callback, stored atomically because StartHotkey writes it from one goroutine and the tap
// thread reads it on every keystroke. No mutex on the read path: a lock there would let a slow
// StopHotkey stall a keystroke, which is the one thing docs/ARCHITECTURE.md#the-cgo-rule forbids.
var hotkeyCB atomic.Pointer[func(Gesture)]

// Serialises StartHotkey against StopHotkey. Never on the keystroke path.
var hotkeyMu sync.Mutex

// ErrNilHotkeyCallback is returned when fn is nil: a tap whose gestures go nowhere installs the
// mechanism and silently discards it, which is the failure this package exists to avoid.
var ErrNilHotkeyCallback = errors.New("darwin: start hotkey: fn must not be nil")

// ErrNoModifiers is returned when modifiers is 0: a bare-key chord would be swallowed session-wide.
var ErrNoModifiers = errors.New("darwin: start hotkey: the chord needs at least one modifier")

// StartHotkey installs the session event tap for the chord keyCode+modifiers (48 / Alternate is ⌥⇥)
// and calls fn for each step of the gesture. keyCode is a hardware keycode; modifiers is a CGEventFlags
// mask, whose bits match NSEvent's device-independent modifier flags. fn runs on the tap's own thread
// and MUST return immediately — Loop.Post is exactly the right shape (non-blocking always, D22). The
// chord is swallowed so the focused application never sees it; every other key passes through.
//
// Requires the Accessibility grant and returns ErrNotTrusted without it (an ungranted tap installs and
// then never fires). A second call while running keeps the first chord (StopHotkey first to change it)
// but does replace fn.
func StartHotkey(keyCode, modifiers int, fn func(Gesture)) error {
	if fn == nil {
		return ErrNilHotkeyCallback
	}
	if modifiers == 0 {
		return ErrNoModifiers
	}
	hotkeyMu.Lock()
	defer hotkeyMu.Unlock()
	// Published before the C side starts so a keystroke during gt_hotkey_start's setup is not dropped.
	hotkeyCB.Store(&fn)
	if err := statusError("start hotkey", C.gt_hotkey_start(C.uint32_t(keyCode), C.uint64_t(modifiers))); err != nil {
		hotkeyCB.Store(nil)
		return err
	}
	return nil
}

// HotkeyDisplay renders a chord as a short human string — "⌥⇥", "⌃⌥ 40" — for the settings recorder.
// Modifiers are the four device-independent symbols; a keycode without a well-known name shows as its
// number.
func HotkeyDisplay(keyCode, modifiers int) string {
	s := ""
	if modifiers&(1<<18) != 0 { // Control
		s += "⌃"
	}
	if modifiers&(1<<19) != 0 { // Option
		s += "⌥"
	}
	if modifiers&(1<<17) != 0 { // Shift
		s += "⇧"
	}
	if modifiers&(1<<20) != 0 { // Command
		s += "⌘"
	}
	switch keyCode {
	case 48:
		return s + "⇥"
	case 49:
		return s + "Space"
	case 36:
		return s + "↩"
	case 53:
		return s + "⎋"
	case 51:
		return s + "⌫"
	case 123:
		return s + "←"
	case 124:
		return s + "→"
	case 125:
		return s + "↓"
	case 126:
		return s + "↑"
	default:
		return fmt.Sprintf("%s %d", s, keyCode)
	}
}

// StopHotkey removes the tap. After it returns fn will not be called again. Safe when nothing was
// started, and safe twice.
func StopHotkey() {
	hotkeyMu.Lock()
	defer hotkeyMu.Unlock()
	C.gt_hotkey_stop()
	hotkeyCB.Store(nil)
}

//export goHotkeyGesture
func goHotkeyGesture(kind C.int) {
	// The whole Go side of the keystroke path: load the callback and call it. The first call also
	// pays the one-time cost of the Go runtime meeting the tap thread (P0.2); every call after is the
	// ~39 ns crossing D1 measured. V6.1 is where that first crossing gets a number.
	if fn := hotkeyCB.Load(); fn != nil {
		(*fn)(Gesture(kind))
	}
}
