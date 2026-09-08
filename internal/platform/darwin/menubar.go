package darwin

/*
#include <stdlib.h>
#include "menubar.h"
*/
import "C"

import (
	"errors"
	"sync/atomic"
	"unsafe"
)

// The menu-bar status item (P8.1). GoTab is LSUIElement, so without this a Finder-installed switcher
// has no visible surface — no way to reach Settings or to quit except a terminal. One status item,
// two actions, both handed back to Go so cmd/gotab decides what "Settings" and "Quit" mean.

var (
	menubarSettings atomic.Pointer[func()]
	menubarQuit     atomic.Pointer[func()]
)

// ErrNilMenubarCallback is returned by InstallMenuBar when either callback is nil: a menu whose items
// do nothing is worse than no menu.
var ErrNilMenubarCallback = errors.New("darwin: install menu bar: onSettings and onQuit must not be nil")

// InstallMenuBar adds the status item. onSettings runs when "Settings…" is chosen, onQuit when
// "Quit GoTab" is. Both are invoked on the main thread. header is a disabled first row (e.g.
// "GoTab 0.3.2"); pass "" to omit it. Main thread only; the AppKit run loop must be running (call it
// right before darwin.RunLoop, from OnMain). A second call is a no-op.
func InstallMenuBar(header string, onSettings, onQuit func()) error {
	if onSettings == nil || onQuit == nil {
		return ErrNilMenubarCallback
	}
	menubarSettings.Store(&onSettings)
	menubarQuit.Store(&onQuit)

	var ctitle *C.char
	if header != "" {
		ctitle = C.CString(header)
		defer C.free(unsafe.Pointer(ctitle))
	}
	if err := statusError("install menu bar", C.gt_menubar_install(ctitle)); err != nil {
		menubarSettings.Store(nil)
		menubarQuit.Store(nil)
		return err
	}
	return nil
}

// RemoveMenuBar takes the status item down. Safe when nothing was installed, and safe twice.
func RemoveMenuBar() {
	C.gt_menubar_remove()
	menubarSettings.Store(nil)
	menubarQuit.Store(nil)
}

//export goMenubarSettings
func goMenubarSettings() {
	if fn := menubarSettings.Load(); fn != nil {
		(*fn)()
	}
}

//export goMenubarQuit
func goMenubarQuit() {
	if fn := menubarQuit.Load(); fn != nil {
		(*fn)()
	}
}
