// Press-to-raise for the panel's synthetic tile elements (P5.2 follow-up, D59).
//
// GTTileElement.accessibilityPerformPress (panel.m) was a deliberate no-op: panel.h is frozen by the
// Phase 3 carve and exposes no way to raise a window from the panel, so a VoiceOver user committed
// only by releasing the ⌥ chord. That is awkward with VoiceOver running — the cursor navigates the
// tiles, and "press this one" is the natural gesture. This header is the one extra hook, kept out of
// panel.h exactly as timing.h is: panel.m calls goPanelChooseTile(index) from the press handler and
// Go turns it into an absolute-select-then-activate on the event loop.
//
// THREADING: called on the main thread, from -[GTTileElement accessibilityPerformPress]. The Go side
// only does a non-blocking Loop.Post, so it is safe there.
#ifndef GOTAB_DARWIN_A11YACTION_H
#define GOTAB_DARWIN_A11YACTION_H

#include <stdint.h>

// Defined in Go (a11yaction.go). index is the tile's position in the last frame (0-based, matching
// the order the renderer built). A negative index is ignored by the Go side.
extern void goPanelChooseTile(int32_t index);

#endif
