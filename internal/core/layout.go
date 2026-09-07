package core

// P3.2 -- the tile layout engine. Pure geometry: given a window count and the display the panel will
// appear on, compute the panel size and one frame per tile. No cgo, no macOS -- "pure Go computes
// frames, C only draws" (docs/ROADMAP.md P3.2). The renderer (P3.1) draws Layout's output verbatim
// and does no arithmetic of its own: gt_panel_show is handed Panel.W/H and each tile Rect unchanged.
//
// The engine, in order:
//  1. margin -- the gap kept between the panel and each screen edge scales with the display: a
//     fraction of Screen.W, clamped to a sane band. A fixed 80 pt is a fat border on a 1280-wide
//     laptop and a hairline on a 6016-wide Pro Display XDR; a fraction reads the same on both.
//  2. columns -- as many tiles as fit the usable width at MinTile size, then capped by MaxCols and by
//     n. Fewer than n fit -> the grid wraps and Panel.H grows by a row. The floor is enforced here
//     rather than by shrinking tiles past it: "below the minimum it wraps instead".
//  3. tile width -- the usable width divided across those columns, clamped to [MinTileW, MaxTileW].
//     A sparse row grows its tiles toward the ceiling and then simply stops reaching the screen
//     edges; it never grows past MaxTileW.
//  4. rebalance -- with the row count now fixed, spread n as evenly as possible across it so a
//     trailing row is never left nearly empty (7 tiles over 2 rows is 4+3, not 5+2).
//
// Everything is integer arithmetic with no map iteration, no time and no rand: identical inputs give
// an identical Result on every call. The only allocation is the tile slice, and only when the caller
// passes no big-enough dst -- the switcher reuses one backing slice across summons and the hot path
// is 0 allocs/op (asserted in V6.8, not here -- D23).
//
// P3.2 owns this file. It does NOT touch api.go (frozen). New shared types and fields are ADDED
// here, never reshaped or renamed -- P3.1 and the integrator code against Rect / LayoutOpts / Result.

// Rect is a frame in points, origin top-left (the render view is flipped, so y grows downward and no
// axis flip is needed between here and drawRect:).
type Rect struct {
	X, Y, W, H int
}

// LayoutOpts is the presentation input Layout cannot derive from the window count alone. The zero
// value is a usable default: Layout treats a zero field as "pick something sensible".
type LayoutOpts struct {
	// Screen is the visible frame of the display the panel will appear on, in points. Layout reads
	// its width to size the margin and bound the panel, and its height only to report overflow via
	// Result; the origin is ignored -- the renderer centres the panel on the display under the
	// mouse itself (gt_panel_show). A zero-or-negative-width Screen means "unbounded": tiles stay at
	// preferred size and only MaxCols wraps them.
	Screen Rect
	// Scale is the display's backing scale factor (1 or 2). Zero means 1. It exists so P3.3 can ask
	// P2.6 for a thumbnail at the right pixel size; Layout reports it back in Result unchanged.
	Scale int
	// TileW, TileH are the preferred tile size in points. Zero means Layout's built-in default. The
	// width Layout actually uses lands in [MinTileW, MaxTileW]; the height is the preferred value
	// clamped to [MinTileH, MaxTileH] and is constant for every tile (a row stretches horizontally,
	// a column does not).
	TileW, TileH int
	// MinTileW, MinTileH are the shrink-to-fit floor in points. Layout packs more tiles into a row
	// by narrowing them only to MinTileW; past that the row wraps instead. Zero means the built-in
	// default.
	MinTileW, MinTileH int
	// MaxTileW, MaxTileH are the grow-to-fill ceiling in points. Layout widens a sparse row's tiles
	// to take up the usable width only to MaxTileW; past that the row just doesn't reach the screen
	// edges. Zero means the built-in default.
	MaxTileW, MaxTileH int
	// MaxCols caps tiles per row before wrapping. Zero means "as many as fit".
	MaxCols int
}

// Result is what the renderer needs for one summon: the panel size and one frame per window, index
// aligned to the order the caller passed in.
type Result struct {
	// Panel is the slab size in points, origin (0,0). The renderer positions it on screen itself;
	// Layout only sizes it.
	Panel Rect
	// Tiles has exactly n entries, tile i being window i in the caller's order. Row-major: tile i
	// sits at row i/Cols, column i%Cols. Empty when n == 0.
	Tiles []Rect
	// Cols, Rows are the grid Tiles are laid out on -- the widest row has Cols tiles, there are Rows
	// rows, and the last row may be short. Reported so the integrator can move the selection by a
	// whole row without re-deriving the grid. Both 0 when n == 0.
	Cols, Rows int
	// Overflow is true when the panel is taller than Screen.H (Screen.H > 0) -- n did not fit the
	// display even wrapped, and the renderer must clip or scroll. Layout never drops a tile to
	// avoid this. Always false when Screen.H <= 0.
	Overflow bool
	// Scale is echoed from LayoutOpts (defaulted to 1) so the caller has one place to read it.
	Scale int
}

// Default geometry, in points. Named so P3.2 and P3.1's placeholder art agree on a number instead of
// each hard-coding one. The Min/Max defaults keep the 4:3 of the preferred size.
const (
	DefaultTileW = 160
	DefaultTileH = 120

	DefaultMinTileW = 96
	DefaultMinTileH = 72
	DefaultMaxTileW = 280
	DefaultMaxTileH = 210

	tileGap  = 10
	panelPad = 16

	// Margin between the panel and each screen edge: Screen.W / marginDivisor, clamped to
	// [minMargin, maxMargin]. 1280 -> 64, 2560 -> 128, 6016 -> 220 (clamped). This is what keeps
	// the panel off the bezel on a small display without swallowing a huge one.
	marginDivisor = 20
	minMargin     = 32
	maxMargin     = 220
)

// Layout computes the panel and tile frames for n windows. n may be 0 (an empty slab); Result.Tiles
// then has length 0. Otherwise it has exactly n entries, index-aligned to the caller's window order,
// each an origin-top-left frame in the panel's content view.
//
// dst is a caller-owned backing slice to write the tiles into -- pass prev[:0] to reuse it; nil is
// fine. Layout allocates only when cap(dst) < n, so the summon path that keeps one slice around
// across summons stays allocation-free.
func Layout(n int, opts LayoutOpts, dst []Rect) Result {
	scale := opts.Scale
	if scale <= 0 {
		scale = 1
	}

	if cap(dst) < n {
		dst = make([]Rect, 0, n)
	}
	dst = dst[:0]

	if n <= 0 {
		pad2 := panelPad * 2
		return Result{Panel: Rect{W: pad2, H: pad2}, Tiles: dst, Scale: scale}
	}

	// Resolve the tile-size box, defending against a caller who inverts min/max or puts the
	// preferred size outside it.
	minW, maxW := pick(opts.MinTileW, DefaultMinTileW), pick(opts.MaxTileW, DefaultMaxTileW)
	minH, maxH := pick(opts.MinTileH, DefaultMinTileH), pick(opts.MaxTileH, DefaultMaxTileH)
	maxW = max(maxW, minW)
	maxH = max(maxH, minH)
	prefW := clampInt(pick(opts.TileW, DefaultTileW), minW, maxW)
	tileH := clampInt(pick(opts.TileH, DefaultTileH), minH, maxH)

	// Usable width and the grid's inner width (inside the panel padding). A zero-or-negative Screen
	// means "no width constraint".
	hasW := opts.Screen.W > 0
	inner := 0
	if hasW {
		margin := clampInt(opts.Screen.W/marginDivisor, minMargin, maxMargin)
		inner = opts.Screen.W - 2*margin - 2*panelPad
		if inner <= 0 {
			hasW = false
		}
	}

	// Columns: at most as many MinTileW tiles as fit inner -- k tiles need
	// k*minW + (k-1)*tileGap <= inner, i.e. k <= (inner+tileGap)/(minW+tileGap). Then cap by n and
	// MaxCols. Always at least one.
	cols := n
	if hasW {
		fit := (inner + tileGap) / (minW + tileGap)
		cols = min(cols, fit)
	}
	if opts.MaxCols > 0 {
		cols = min(cols, opts.MaxCols)
	}
	cols = max(cols, 1)

	rows := (n + cols - 1) / cols
	// Rebalance: the row count is fixed now, so spread n evenly across it. ceil(n/rows) <= cols
	// always, so this only ever shrinks cols -- it cannot break MaxCols or the width fit.
	cols = (n + rows - 1) / rows

	// Tile width: divide the usable width across the columns, then hold it inside the size box.
	tileW := prefW
	if hasW {
		tileW = clampInt((inner-(cols-1)*tileGap)/cols, minW, maxW)
		// A display too narrow for even one MinTileW tile plus padding: keep the panel on screen
		// and let the tile fall below the floor rather than hang off the bezel. No real macOS
		// display hits this; it is here so a bogus tiny Screen still yields an on-screen panel.
		if grid := cols*tileW + (cols-1)*tileGap; grid > inner {
			tileW = max((inner-(cols-1)*tileGap)/cols, 1)
		}
	}

	pad2 := panelPad * 2
	panelW := pad2 + cols*tileW + (cols-1)*tileGap
	panelH := pad2 + rows*tileH + (rows-1)*tileGap

	y := panelPad
	for idx := 0; idx < n; y += tileH + tileGap {
		x := panelPad
		for c := 0; c < cols && idx < n; c++ {
			dst = append(dst, Rect{X: x, Y: y, W: tileW, H: tileH})
			x += tileW + tileGap
			idx++
		}
	}

	return Result{
		Panel:    Rect{W: panelW, H: panelH},
		Tiles:    dst,
		Cols:     cols,
		Rows:     rows,
		Overflow: opts.Screen.H > 0 && panelH > opts.Screen.H,
		Scale:    scale,
	}
}

// pick returns v when it is a usable positive dimension, otherwise the default d.
func pick(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

// clampInt holds v within [lo, hi]. lo <= hi is the caller's to guarantee.
func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }
