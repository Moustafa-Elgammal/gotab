package darwin

/*
#include <stdlib.h>
#include <string.h>
#include "panel.h"
*/
import "C"

import (
	"unsafe"
)

// SKELETON for P3.1. The marshalling is real so the integrator can wire a summon end to end; the
// screen-under-the-mouse placement, the show/update split behaviour and the palette plumbing are
// P3.1's to finish in panel.m. P3.1 owns this file.

// Tile is one entry to draw. The event loop builds a slice of these from core.Model plus the frames
// core.Layout computed (P3.2); the renderer only draws them. A plain value type on purpose: the whole
// slice crosses into C in one call, never one crossing per tile (docs/ARCHITECTURE.md#the-cgo-rule).
type Tile struct {
	X, Y, W, H int
	Selected   bool
	// Image is a borrowed handle. The panel never releases it; P3.3 owns its lifecycle and must keep
	// it alive while it is a visible tile's current image.
	Image    ImageRef
	Title    string
	Subtitle string
}

// CreatePanel builds NSApp, the panel and its views. Call once, on the main thread, before any other
// panel call. Idempotent.
func CreatePanel() error { return statusError("panel create", C.gt_panel_create()) }

// ShowPanel shows the panel at w x h points with exactly these tiles, centred on the active screen.
// The summon path: it re-resolves the screen and orders the window front. Main thread only.
func ShowPanel(tiles []Tile, w, h int) error {
	ct, free := marshalTiles(tiles)
	defer free()
	return statusError("panel show", C.gt_panel_show(ct, C.int32_t(len(tiles)), C.int32_t(w), C.int32_t(h)))
}

// UpdatePanel re-populates the tiles and redraws without re-placing or re-ordering the window. The
// keystroke path, where only the selection or a just-arrived thumbnail changed. No-op when hidden.
func UpdatePanel(tiles []Tile) error {
	ct, free := marshalTiles(tiles)
	defer free()
	return statusError("panel update", C.gt_panel_update(ct, C.int32_t(len(tiles))))
}

// HidePanel orders the panel out; the panel is kept for the next ShowPanel.
func HidePanel() { C.gt_panel_hide() }

// PanelVisible reports whether the panel is on screen.
func PanelVisible() bool { return C.gt_panel_visible() != 0 }

// marshalTiles copies tiles into a C array. Returns the array and a free func; call free after the C
// call returns. The array is a single allocation regardless of tile count.
func marshalTiles(tiles []Tile) (*C.gt_tile, func()) {
	if len(tiles) == 0 {
		return nil, func() {}
	}
	n := len(tiles)
	buf := C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.gt_tile{})))
	arr := (*[1 << 20]C.gt_tile)(buf)[:n:n]
	for i, t := range tiles {
		d := &arr[i]
		d.x, d.y, d.w, d.h = C.int32_t(t.X), C.int32_t(t.Y), C.int32_t(t.W), C.int32_t(t.H)
		d.selected = 0
		if t.Selected {
			d.selected = 1
		}
		d.image = t.Image.c
		putBounded(unsafe.Pointer(&d.title[0]), C.size_t(unsafe.Sizeof(d.title)), t.Title, &d.title_len)
		putBounded(unsafe.Pointer(&d.subtitle[0]), C.size_t(unsafe.Sizeof(d.subtitle)), t.Subtitle, &d.subtitle_len)
	}
	return (*C.gt_tile)(buf), func() { C.free(buf) }
}

// putBounded copies s into a fixed C char array, truncating on a byte boundary. P3.1 may want a
// rune-boundary truncation like shim.m's copy_cfstring; titles are full of multi-byte characters.
func putBounded(dst unsafe.Pointer, cap C.size_t, s string, outLen *C.uint16_t) {
	b := []byte(s)
	if C.size_t(len(b)) > cap {
		b = b[:cap]
	}
	if len(b) > 0 {
		C.memcpy(dst, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	}
	*outLen = C.uint16_t(len(b))
}
