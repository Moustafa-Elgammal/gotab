package darwin

/*
#include "hotkey.h"
*/
import "C"

import (
	"errors"
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

// StartHotkey installs the session event tap and calls fn for each step of the Option+Tab gesture. fn
// runs on the tap's own thread and MUST return immediately — Loop.Post is exactly the right shape
// (non-blocking always, D22). The switcher's chord is swallowed so the focused application never sees
// it; every other key passes through.
//
// Requires the Accessibility grant and returns ErrNotTrusted without it (an ungranted tap installs
// and then never fires). A second call while running replaces fn and returns nil without
// re-installing anything.
func StartHotkey(fn func(Gesture)) error {
	if fn == nil {
		return ErrNilHotkeyCallback
	}
	hotkeyMu.Lock()
	defer hotkeyMu.Unlock()
	// Published before the C side starts so a keystroke during gt_hotkey_start's setup is not dropped.
	hotkeyCB.Store(&fn)
	if err := statusError("start hotkey", C.gt_hotkey_start()); err != nil {
		hotkeyCB.Store(nil)
		return err
	}
	return nil
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
