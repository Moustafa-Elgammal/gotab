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
	// Quit — stop the loop.
	Quit
)

// Event is one thing that happened. Zero size beyond its fields; posting one allocates nothing.
type Event struct {
	Kind   Kind
	Window core.WindowID // Focused
	Dir    core.Direction
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
	// vanished are found without clearing a map every pass.
	scratch []core.Window
	present map[core.WindowID]uint64
	gen     uint64

	visible bool

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
		model:   core.NewModel(capacity),
		order:   core.NewOrder(capacity),
		enum:    darwin.NewEnumerator(),
		events:  make(chan Event, eventQueue),
		rescan:  make(chan struct{}, 1),
		scratch: make([]core.Window, 0, capacity),
		present: make(map[core.WindowID]uint64, capacity),
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
		// An attention decision: one of the two things ALTTAB-LESSONS section 3 permits to reorder.
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
		// P2.5 landed, so this is a real switch now: raise the selected window, then hide. The raise
		// is Mach IPC and can take up to the messaging timeout — acceptable on this goroutine
		// (action.go), and it must precede the hide or the panel vanishes before anything moves. A
		// stale selection (window closed mid-summon) comes back ErrNoWindow and goes to OnError; the
		// switcher still dismisses.
		if l.sel.ID != 0 {
			if err := darwin.Raise(l.sel.ID); err != nil && l.OnError != nil {
				l.OnError(fmt.Errorf("activate window %d: %w", l.sel.ID, err))
			}
		}
		l.visible = false
	case Quit:
		return true
	}
	l.notify()
	return false
}

// doRescan re-reads the world and reconciles the model with it.
//
// The subtlety is that this runs on every notification and must not disturb MRU order. Upsert is
// passed a zero FocusSeq, which it reads as "preserve what you have", so a window whose title changed
// keeps its position — that is ALTTAB-LESSONS section 3's rule expressed as an argument value. The
// order is rebuilt only when membership actually changed, because a rebuild IS a reorder.
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

	changed := false
	for _, w := range l.scratch {
		if _, known := l.model.Row(w.ID); !known {
			changed = true
		}
		l.model.Upsert(w)
		l.present[w.ID] = l.gen
	}

	// Backwards, because Remove is swap-with-last: the row that moves into i has already been
	// visited, and i only ever decreases, so nothing is skipped and nothing is read out of range.
	for i := l.model.Len() - 1; i >= 0; i-- {
		id := l.model.IDs[i]
		if l.present[id] != l.gen {
			l.model.Remove(id)
			delete(l.present, id)
			changed = true
		}
	}

	if changed {
		// A structural repair, which section 3 does permit to reorder.
		l.order.Rebuild(l.model)
		l.sel.Reconcile(l.order, l.model)
	}
	l.notify()
}

func (l *Loop) notify() {
	if l.OnState != nil {
		l.OnState(l.model, l.order, l.sel, l.visible)
	}
}
