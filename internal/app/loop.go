package app

import (
	"context"
	"fmt"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
	"github.com/Moustafa-Elgammal/gotab/internal/platform/darwin"
)

// Kind is what happened. A small enum in a plain struct rather than an interface: an interface value
// boxes, and these are posted from callbacks on the summon path where nothing may allocate.
type Kind uint8

const (
	// Focused — the user's attention moved to a window. One of the two things allowed to reorder.
	Focused Kind = iota
	// Summon — show the switcher.
	Summon
	// Dismiss — hide it, without changing the selection's meaning.
	Dismiss
	// Cycle — move the selection by one in Dir, while summoned.
	Cycle
	// Activate — commit to the current selection: raise that window and dismiss.
	Activate
	// Choose — commit to a specific window by ID (a VoiceOver press on a tile, D59): select it if
	// it is still listed, then the same raise-and-dismiss as Activate.
	Choose
	// Quit — stop the loop.
	Quit
)

// Event is one thing that happened. Zero size beyond its fields; posting one allocates nothing.
type Event struct {
	Kind   Kind
	Window core.WindowID  // Focused, Choose
	Dir    core.Direction // Cycle
}

// Loop owns the model. Construct with New, drive with Run, feed with Post and Rescan.
type Loop struct {
	model *core.Model
	order *core.Order
	sel   core.Selection
	enum  *darwin.Enumerator

	events chan Event
	rescan chan struct{}

	// scratch and present are reused across rescans so a steady state allocates nothing. present
	// maps a window to the generation that last saw it; comparing against gen is how windows that
	// vanished are found without clearing a map every pass. allowed is the filter kernel's output,
	// also reused. pidAlive memoizes the P7.2 liveness check within one rescan so a dead app's N
	// windows cost one syscall, not N; it is cleared, never reallocated, each pass.
	scratch  []core.Window
	present  map[core.WindowID]uint64
	allowed  []int
	pidAlive map[core.AppID]bool
	gen      uint64

	visible bool

	// Rules is the filter (P1.3): which windows the switcher shows. Set it before Run; the zero value
	// shows every switchable window on the current Space. doRescan refreshes Rules.CurrentSpace from
	// the platform each pass — that field is runtime state, not a setting. Changing the rest of Rules
	// after Run has started takes effect on the next rescan; a live settings integration would post a
	// Rescan after doing so.
	Rules core.Rules

	// OnState is called on the loop goroutine after anything changes, with the state as it now
	// stands. Nil until a renderer exists (P3.1). The callee must not retain or mutate what it is
	// handed: those are the loop's own structures, not copies.
	OnState func(m *core.Model, o *core.Order, sel core.Selection, visible bool)

	// OnError reports a rescan that failed. Nil discards. Rescans fail for ordinary reasons — the
	// WindowServer declines during fast user switching, a TCC grant is revoked mid-session — and a
	// loop that exited on those would be a switcher that stops working and does not say why.
	OnError func(error)
}

// eventQueue is deliberately generous. Posting is non-blocking and a full queue drops, so the depth
// is what stands between a burst of Accessibility notifications and a lost keystroke.
const eventQueue = 256

// New builds a loop sized for capacity windows. The model and order grow beyond it if they must; the
// number only decides how many enumerations happen before the buffers settle.
func New(capacity int) *Loop {
	return &Loop{
		model:    core.NewModel(capacity),
		order:    core.NewOrder(capacity),
		enum:     darwin.NewEnumerator(),
		events:   make(chan Event, eventQueue),
		rescan:   make(chan struct{}, 1),
		scratch:  make([]core.Window, 0, capacity),
		present:  make(map[core.WindowID]uint64, capacity),
		allowed:  make([]int, 0, capacity),
		pidAlive: make(map[core.AppID]bool, capacity),
	}
}

// Post enqueues a discrete event and returns whether it was accepted. **It never blocks**, which is
// what makes it safe to call from an Accessibility callback or an event tap: those run on threads the
// Go runtime has never seen, and one that stalls is disabled by the system
// (kCGEventTapDisabledByTimeout). A blocking send in a callback is how a hotkey dies silently.
//
// A false return means the queue was full and the event was dropped. For Focused that is survivable —
// the next rescan repairs the ordering — and for Summon or Quit it is not, so callers of those check.
func (l *Loop) Post(e Event) bool {
	select {
	case l.events <- e:
		return true
	default:
		return false
	}
}

// Rescan signals that the window set may have changed. Also never blocks, and unlike Post a dropped
// signal is **correct**: the pending one already means "re-read everything", so coalescing a burst of
// twenty Accessibility notifications into one enumeration is the behaviour wanted, not a compromise.
func (l *Loop) Rescan() {
	select {
	case l.rescan <- struct{}{}:
	default:
	}
}

// Run owns the state until ctx is cancelled or a Quit event arrives. It must be the only goroutine
// that ever calls into the model, which is what lets there be no lock on it.
func (l *Loop) Run(ctx context.Context) error {
	l.doRescan() // start from the truth rather than from an empty list
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.rescan:
			l.doRescan()
		case e := <-l.events:
			if l.handle(e) {
				return nil
			}
		}
	}
}

// handle applies one event and reports whether the loop should stop.
func (l *Loop) handle(e Event) bool {
	switch e.Kind {
	case Focused:
		// An attention decision: one of the two things PLATFORM-LESSONS section 3 permits to reorder.
		if l.model.Touch(e.Window) {
			if row, ok := l.model.Row(e.Window); ok {
				l.order.Promote(row)
			}
		}
	case Summon:
		l.visible = true
		l.sel.Reconcile(l.order, l.model)
	case Cycle:
		if !l.visible {
			return false
		}
		l.sel.Move(l.order, l.model, e.Dir, true)
	case Dismiss:
		l.visible = false
	case Activate:
		l.activateSelection()
	case Choose:
		// A VoiceOver press on the tile for window e.Window (D59). By ID, not by position: a rescan
		// can reorder the list between the frame the user is navigating and the press, and Activate
		// is only immune because it commits to l.sel.ID. Anchor a fresh cursor to the ID and let
		// Reconcile find its row; if the window is gone from the list, just dismiss — raising the
		// window that happens to sit at a clamped row is the bug this avoids. Ignored when not
		// summoned, like Cycle.
		if !l.visible {
			return false
		}
		l.sel = core.Selection{ID: e.Window, Row: -1}
		l.sel.Reconcile(l.order, l.model)
		if l.sel.ID == e.Window {
			l.activateSelection()
		} else {
			l.visible = false
		}
	case Quit:
		return true
	}
	l.notify()
	return false
}

// activateSelection commits to l.sel: raise that window, then hide. Shared by Activate (⌥ released)
// and Choose (a VoiceOver press on a specific tile — D59).
//
// P2.5: the raise is Mach IPC and can take up to the messaging timeout — acceptable on this goroutine
// (action.go) — and it must precede the hide, or the panel vanishes before anything moves. A stale
// selection (the window closed mid-summon) comes back ErrNoWindow and goes to OnError; the switcher
// still dismisses.
func (l *Loop) activateSelection() {
	if l.sel.ID != 0 {
		if err := darwin.Raise(l.sel.ID); err != nil && l.OnError != nil {
			l.OnError(fmt.Errorf("activate window %d: %w", l.sel.ID, err))
		}
	}
	l.visible = false
}

// doRescan re-reads the world and reconciles the model with it.
//
// The subtlety is that this runs on every notification and must not disturb MRU order. Upsert is
// passed a zero FocusSeq, which it reads as "preserve what you have", so a window whose title changed
// keeps its position — that is PLATFORM-LESSONS section 3's rule expressed as an argument value.
//
// The order is rebuilt every pass now, because the filter's *inputs* change without the window set
// changing — a window minimizes, the current Space flips — and the order must follow. That is not a
// reorder in section 3's sense: RebuildFrom sorts by FocusSeq, which a title change does not touch,
// so a pass where nothing filter-relevant moved produces the identical order and the renderer redraws
// the same tiles.
func (l *Loop) doRescan() {
	l.gen++

	var err error
	l.scratch, err = l.enum.Enumerate(l.scratch[:0])
	if err != nil {
		// Not fatal. The WindowServer declines during fast user switching and at the login window,
		// and a grant can be revoked mid-session. A loop that exited here would be a switcher that
		// stops working and never says why.
		if l.OnError != nil {
			l.OnError(fmt.Errorf("rescan: %w", err))
		}
		return
	}

	for _, w := range l.scratch {
		l.model.Upsert(w)
		l.present[w.ID] = l.gen
	}

	// Two ways a window leaves the model, both handled here, backwards because Remove is
	// swap-with-last: the row that moves into i has already been visited, and i only ever decreases,
	// so nothing is skipped and nothing is read out of range. At most one Remove per iteration.
	clear(l.pidAlive)
	for i := l.model.Len() - 1; i >= 0; i-- {
		id := l.model.IDs[i]
		// (1) Neither source reported it this pass. A window closed with ⌘W is gone from both
		// kAXWindowsAttribute and CGWindowList, so it is absent from l.scratch and its present entry
		// stalls at the previous generation. This is the path the roadmap notes "already is" — it
		// is, and it covers the ordinary close.
		if l.present[id] != l.gen {
			l.model.Remove(id)
			delete(l.present, id)
			continue
		}
		// (2) It is still being enumerated, but its owning process has exited (D46): a stale
		// CoreGraphics entry, or a cg-only window the join recovered whose app is already gone. The
		// not-seen check cannot catch this because the window is still in the list. One syscall.Kill
		// per distinct pid — a dead app's windows share it, hence pidAlive.
		pid := l.model.Apps[i]
		alive, known := l.pidAlive[pid]
		if !known {
			alive = darwin.ProcessAlive(int(pid))
			l.pidAlive[pid] = alive
		}
		if !alive {
			l.model.Remove(id)
			delete(l.present, id)
		}
	}

	// The Space the user is on is runtime state, not a setting — refresh it so the ShowOtherSpace
	// rule has something to compare against. 0 (SkyLight unavailable) means "do not filter by Space",
	// which core.Rules.Allows already handles.
	l.Rules.CurrentSpace = darwin.CurrentSpace()

	l.allowed = core.Filter(l.model, l.Rules, l.allowed)
	l.order.RebuildFrom(l.model, l.allowed)
	l.sel.Reconcile(l.order, l.model)
	l.notify()
}

func (l *Loop) notify() {
	if l.OnState != nil {
		l.OnState(l.model, l.order, l.sel, l.visible)
	}
}
