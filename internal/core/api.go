package core

// This file is the frozen contract for Phase 1 (task P1.0). Every other P1 task codes against these
// types and adds its own file; nothing else may edit this one. See docs/PARALLEL-WORK.md — agents
// working in parallel never coordinate, they only agree here.
//
// Types only, no behaviour. Methods live in the file owned by the task that implements them, so two
// agents never touch the same file. Field layout IS part of the contract: P1.1 stores windows as a
// struct-of-arrays, and the ordering, filter and search kernels read those slices directly rather
// than materialising []Window, which is what keeps the hot path allocation-free.

// ---------------------------------------------------------------------------
// Identifiers
// ---------------------------------------------------------------------------

// WindowID is the CoreGraphics window number (CGWindowID). Unique per window and stable for its
// lifetime, but NOT reused-safe: the WindowServer may hand the same number to a later window, so
// never cache anything against it past a window's removal.
type WindowID uint32

// AppID is the owning process id (pid_t). Windows of one app share it.
type AppID int32

// SpaceID identifies a macOS Space. Zero means unknown — SkyLight is a private API and can decline
// to answer, and docs/PLATFORM-LESSONS.md §7 is explicit that unknown must stay distinguishable from
// "not on a Space". Never treat 0 as a real Space.
type SpaceID uint64

// TabGroupID groups windows that are tabs of one another. Zero means the window is not tabbed.
type TabGroupID uint32

// FocusSeq orders windows by recency of focus. Deliberately a monotonic counter and NOT a
// timestamp: wall-clock time jumps (NTP, sleep, DST) and would silently scramble MRU order.
// Higher means more recently focused. Zero means never focused.
type FocusSeq uint64

// ---------------------------------------------------------------------------
// Window state
// ---------------------------------------------------------------------------

// WindowFlags is a bitfield rather than a set of bools so a filter pass is one mask-and-compare
// per window over a packed []WindowFlags, with no per-window branching on separate slices.
type WindowFlags uint16

const (
	// FlagMinimized — in the Dock. Still switchable; selecting it unminimizes.
	FlagMinimized WindowFlags = 1 << iota
	// FlagHidden — its application is hidden (⌘H). Distinct from minimized: hiding is per-app.
	FlagHidden
	// FlagOnScreen — currently on the active Space and not occluded by a fullscreen window.
	FlagOnScreen
	// FlagFullscreen — occupies its own Space.
	FlagFullscreen
	// FlagTabbed — a tab in a tab group; see TabGroupID. Non-representative tabs are normally
	// filtered out so one group shows as one entry.
	FlagTabbed
)

// Has reports whether every bit in want is set. Written here, in the frozen file, because all four
// kernels need it and duplicating it in four task files is how four subtly different versions
// appear.
func (f WindowFlags) Has(want WindowFlags) bool { return f&want == want }

// HasAny reports whether any bit in want is set.
func (f WindowFlags) HasAny(want WindowFlags) bool { return f&want != 0 }

// Window is one switchable window, as a value. This is the *external* shape — what a caller outside
// core receives. Internally the Model stores the same data as parallel slices; use Model.At to
// materialise one, and do not build a []Window on the hot path.
type Window struct {
	ID      WindowID
	App     AppID
	Group   TabGroupID
	Space   SpaceID
	Focus   FocusSeq
	Flags   WindowFlags
	Title   string
	AppName string
}

// ---------------------------------------------------------------------------
// P1.1 — the model (struct-of-arrays)
// ---------------------------------------------------------------------------

// Model holds every known window. Parallel slices, not []Window: the ordering and filter kernels
// sweep one field across all windows, and a struct-of-arrays keeps that sweep in cache and free of
// per-element pointer chasing.
//
// All slices are the same length and index-aligned: row i of every slice describes one window.
// Removal is swap-with-last (order here is storage order and carries no meaning — Order owns
// presentation order), which keeps removal O(1) and the slices packed.
//
// Not safe for concurrent use. One event-loop goroutine owns it; see docs/ARCHITECTURE.md#threading.
type Model struct {
	IDs      []WindowID
	Apps     []AppID
	Groups   []TabGroupID
	Spaces   []SpaceID
	Focuses  []FocusSeq
	Flags    []WindowFlags
	Titles   []string
	AppNames []string

	// byID maps a window to its row. Kept in sync by every mutation; a stale entry here is the
	// most likely source of a wrong-window switch, so P1.1's tests must cover swap-removal.
	byID map[WindowID]int

	// nextFocus is the counter handed out by Touch. Monotonic for the process lifetime.
	nextFocus FocusSeq
}

// ---------------------------------------------------------------------------
// P1.2 — ordering
// ---------------------------------------------------------------------------

// Order is a presentation order over the Model: a permutation of row indices, reused across
// summons so the hot path allocates nothing. Never store WindowIDs here — rows move on removal.
//
// docs/PLATFORM-LESSONS.md §3 is the constraint that matters: only an attention decision (the user
// focused something) or a structural repair (a window appeared or vanished) may reorder. A title
// change, a resize, or a redraw must NOT.
type Order struct {
	Rows []int
}

// ---------------------------------------------------------------------------
// P1.3 — filtering
// ---------------------------------------------------------------------------

// Rules decides which windows are switchable. Zero value = show everything switchable on the
// current Space, which is the behaviour a user expects before touching any setting.
type Rules struct {
	ShowMinimized  bool
	ShowHidden     bool
	ShowOtherSpace bool
	// ActiveAppOnly restricts to windows of ActiveApp. Ignored when ActiveApp is 0.
	ActiveAppOnly bool
	ActiveApp     AppID
	// CurrentSpace scopes ShowOtherSpace. Zero means "Space unknown" — when it is zero the Space
	// rule must not filter anything out, or an unavailable SkyLight silently empties the switcher.
	CurrentSpace SpaceID
	// BlockedApps are never shown. Compared by AppName, since a pid is not stable across launches.
	BlockedApps []string
}

// ---------------------------------------------------------------------------
// P1.4 — search
// ---------------------------------------------------------------------------

// Match is one scored search hit. Positions indexes into the *runes* of the matched text, not its
// bytes, so a caller can highlight without re-deriving the match; titles are user text and contain
// multi-byte characters constantly.
type Match struct {
	Row       int
	Score     int
	Positions []int
}

// ---------------------------------------------------------------------------
// P1.5 — selection
// ---------------------------------------------------------------------------

// Selection is the cursor into a presentation Order. It stores the selected window's ID as well as
// its position because the list can be rebuilt underneath it mid-summon (a window closes while the
// switcher is open); the ID is what lets selection survive that, and P1.5's tests must pin it.
type Selection struct {
	Row int
	ID  WindowID
}

// Direction is a movement request. Values are deliberately ±1 so cycling is arithmetic.
type Direction int

const (
	Backward Direction = -1
	Forward  Direction = 1
)

// ---------------------------------------------------------------------------
// P1.7 — thumbnail cache policy
// ---------------------------------------------------------------------------

// CacheKey identifies a cached thumbnail. Size is part of the key because a thumbnail rendered for
// one tile size is not reusable at another, and serving a stale size is a visible bug.
type CacheKey struct {
	Window WindowID
	Width  uint16
	Height uint16
}

// Cache is the bounded LRU *policy* — eviction order only. It holds no bitmaps and no cgo: it
// decides what to evict and the platform layer does the releasing. That split is what lets this be
// tested on any OS. See docs/ARCHITECTURE.md#the-memory-rule; the bound exists for predictable peak
// footprint and a warm cache on summon (D9, D10), not to win a memory comparison.
type Cache struct {
	// capacity is the maximum number of live entries, fixed at construction. Unexported on
	// purpose (D11): when it was exported, a caller could lower it after the fact and strand every
	// entry above the new bound with nobody ever told to release them — the exact leak this type
	// exists to prevent. Read it with Capacity().
	capacity int

	// entries is the LRU, most-recently-used first. Slice, not container/list: capacity is small
	// (tens), so a linear scan beats pointer chasing and allocates nothing.
	entries []CacheKey

	// evicted is reused across calls to report keys the caller must release. Never return a
	// freshly allocated slice from the hot path.
	evicted []CacheKey
}
