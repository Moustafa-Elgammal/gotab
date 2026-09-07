package darwin

/*
#include "settings.h"
*/
import "C"

import (
	"sync"
	"sync/atomic"
)

// SettingsValues seeds the settings window's controls. Primitives only, so this package does not
// import internal/prefs — the caller maps a prefs.Prefs onto this and applies each "Key=Value" change
// back through prefs.Set. BlockedApps is already comma-joined; Appearance is "system"/"light"/"dark".
type SettingsValues struct {
	ShowMinimized  bool
	ShowHidden     bool
	ShowOtherSpace bool
	ActiveAppOnly  bool
	BlockedApps    string
	Appearance     string
	MaxColumns     int
	ThumbnailCache int
	HotkeyDisplay  string
}

// settingsAssignCB and settingsClosedCB are set from the goroutine that calls OpenSettings and read on
// the main thread from the control actions / window delegate — atomic, no lock on the notification
// path, the same shape as observe.go and theme.go.
var (
	settingsAssignCB atomic.Pointer[func(string)]
	settingsClosedCB atomic.Pointer[func()]
	settingsMu       sync.Mutex
)

// OpenSettings builds and shows the settings window, seeds its controls from v, and routes every
// change a user makes to onAssign("Key=Value") — the same form prefs.Set parses. Main thread only,
// and the AppKit run loop (RunLoop) must be running. Idempotent: a second call re-focuses the window
// and re-seeds it.
func OpenSettings(v SettingsValues, onAssign func(kv string)) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	if onAssign != nil {
		settingsAssignCB.Store(&onAssign)
	}
	if err := statusError("open settings", C.gt_settings_open()); err != nil {
		return err
	}

	setBool := func(key string, on bool) {
		k, free := prefsCStr(key)
		defer free()
		n := C.int32_t(0)
		if on {
			n = 1
		}
		C.gt_settings_set_bool(k, n)
	}
	setInt := func(key string, val int) {
		k, free := prefsCStr(key)
		defer free()
		C.gt_settings_set_int(k, C.int32_t(val))
	}
	setStr := func(fn func(*C.char, *C.char), key, val string) {
		k, freeK := prefsCStr(key)
		defer freeK()
		p, freeP := prefsCStr(val)
		defer freeP()
		fn(k, p)
	}

	setBool("ShowMinimized", v.ShowMinimized)
	setBool("ShowHidden", v.ShowHidden)
	setBool("ShowOtherSpace", v.ShowOtherSpace)
	setBool("ActiveAppOnly", v.ActiveAppOnly)
	setInt("MaxColumns", v.MaxColumns)
	setInt("ThumbnailCacheSize", v.ThumbnailCache)
	setStr(func(k, p *C.char) { C.gt_settings_set_choice(k, p) }, "Appearance", v.Appearance)
	setStr(func(k, p *C.char) { C.gt_settings_set_text(k, p) }, "BlockedApps", v.BlockedApps)

	d, free := prefsCStr(v.HotkeyDisplay)
	C.gt_settings_set_hotkey(d)
	free()
	return nil
}

// SettingsSetHotkey refreshes the recorder button's label after a rebind. The caller invokes it from
// its onAssign handler once it has recomputed the display string. Main thread only.
func SettingsSetHotkey(display string) {
	d, free := prefsCStr(display)
	defer free()
	C.gt_settings_set_hotkey(d)
}

// CloseSettings closes the window. The window's own close button does the same.
func CloseSettings() { C.gt_settings_close() }

// OnSettingsClosed registers a callback fired when the settings window closes — the caller stops the
// run loop from it.
func OnSettingsClosed(fn func()) { settingsClosedCB.Store(&fn) }

//export goSettingsAssign
func goSettingsAssign(kv *C.char) {
	s := C.GoString(kv)
	if fn := settingsAssignCB.Load(); fn != nil {
		(*fn)(s)
	}
}

//export goSettingsClosed
func goSettingsClosed() {
	if fn := settingsClosedCB.Load(); fn != nil {
		(*fn)()
	}
}
