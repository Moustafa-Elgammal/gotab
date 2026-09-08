package darwin

/*
#include "a11yaction.h"
*/
import "C"

import "sync/atomic"

// Press-to-raise for the panel's VoiceOver tile elements (P5.2 follow-up, D59). A VoiceOver user who
// navigates the tiles with the VO cursor can press one to raise that window, instead of the ⌥-release
// path a sighted user uses. panel.m's -[GTTileElement accessibilityPerformPress] calls
// goPanelChooseTile with the tile's index; cmd/gotab hangs a "select that index and activate"
// callback here. Inert until OnTileActivate is called, and clearing it (nil) restores the no-op.
var tileActivate atomic.Pointer[func(int)]

// OnTileActivate registers fn to run when a VoiceOver user presses a panel tile. index is the tile's
// position in the last rendered frame. Pass nil to clear. fn is invoked on the main thread and must
// not block — cmd/gotab's fn is a single Loop.Post.
func OnTileActivate(fn func(int)) {
	if fn == nil {
		tileActivate.Store(nil)
		return
	}
	tileActivate.Store(&fn)
}

//export goPanelChooseTile
func goPanelChooseTile(index C.int32_t) {
	if index < 0 {
		return
	}
	if fn := tileActivate.Load(); fn != nil {
		(*fn)(int(index))
	}
}
