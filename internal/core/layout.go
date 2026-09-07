package core

// SKELETON for P3.2. Pure geometry: given a window count and the screen it will be shown on, compute
// the panel size and every tile frame. No cgo, no macOS -- "pure Go computes frames, C only draws"
// (docs/ROADMAP.md P3.2). The renderer (P3.1) consumes Layout's output verbatim.
//
// This skeleton is a single row of equal tiles that clamps to the screen width. P3.2 replaces the body
// of Layout with the real engine: wrapping to multiple rows past a tile count, a minimum and maximum
// tile size, margins that scale with the screen, and whatever emphasis the selected tile gets. The
// types and the Layout signature are the contract the renderer codes against -- extend them, do not
// reshape them, and add fields rather than renaming.
//
// P3.2 owns this file. It does NOT touch api.go (frozen).

// Rect is a frame in points, origin top-left (the render view is flipped, so y grows downward and no
// axis flip is needed between here and drawRect:).
type Rect struct {
	X, Y, W, H int
}

// LayoutOpts is the presentation input Layout cannot derive from the window count alone. The zero
// value is a usable default: Layout treats a zero field as "pick something sensible".
type LayoutOpts struct {
	// Screen is the visible frame of the display the panel will appear on, in points. Layout keeps
	// the panel inside it with a margin.
	Screen Rect
	// Scale is the display's backing scale factor (1 or 2). Zero means 1. It exists so P3.3 can ask
	// P2.6 for a thumbnail at the right pixel size; Layout reports it back in Result unchanged.
	Scale int
	// TileW, TileH are the preferred tile size in points. Zero means Layout's built-in default.
	TileW, TileH int
	// MaxCols caps tiles per row before wrapping. Zero means "as many as fit".
	MaxCols int
}

// Result is what the renderer needs for one summon: the panel size and one frame per window, index
// aligned to the order the caller passed in.
type Result struct {
	Panel Rect
	Tiles []Rect
	// Scale is echoed from LayoutOpts (defaulted to 1) so the caller has one place to read it.
	Scale int
}

// Default tile geometry, in points. Named so P3.2's tests and P3.1's placeholder art agree on a
// number instead of each hard-coding one.
const (
	DefaultTileW = 160
	DefaultTileH = 120
	tileGap      = 10
	panelPad     = 16
)

// Layout computes the panel and tile frames for n windows. n may be 0 (an empty slab). The returned
// Result.Tiles has exactly n entries.
//
// SKELETON: one row, equal widths, clamped to the screen. Deterministic and allocation-light -- the
// caller may pass a reusable backing slice via dst (dst[:0]); nil is fine.
func Layout(n int, opts LayoutOpts, dst []Rect) Result {
	scale := opts.Scale
	if scale <= 0 {
		scale = 1
	}
	tw := opts.TileW
	if tw <= 0 {
		tw = DefaultTileW
	}
	th := opts.TileH
	if th <= 0 {
		th = DefaultTileH
	}

	if dst == nil {
		dst = make([]Rect, 0, n)
	}
	dst = dst[:0]

	gaps := n - 1
	if gaps < 0 {
		gaps = 0
	}
	panelW := panelPad*2 + n*tw + gaps*tileGap
	if maxW := opts.Screen.W - 80; opts.Screen.W > 0 && panelW > maxW {
		panelW = maxW
		if n > 0 {
			tw = (panelW - panelPad*2 - (n-1)*tileGap) / n
		}
	}
	panelH := panelPad*2 + th

	x := panelPad
	for i := 0; i < n; i++ {
		dst = append(dst, Rect{X: x, Y: panelPad, W: tw, H: th})
		x += tw + tileGap
	}

	return Result{
		Panel: Rect{X: 0, Y: 0, W: panelW, H: panelH},
		Tiles: dst,
		Scale: scale,
	}
}
