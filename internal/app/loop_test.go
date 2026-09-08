package app

import (
	"fmt"
	"testing"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
)

// The gesture-handling half of the event loop, whose tests Phase 2/3 deferred (D16, D23). handle is
// the code path a keystroke-while-cycling takes; V6.8 is the allocation guarantee on it, V6.6 the
// behaviour.

// loadedLoop returns a Loop with n windows in the model and the order built, as it stands the
// instant before a summon. It does not touch the platform: doRescan (which crosses into cgo) is
// never called.
func loadedLoop(n int) *Loop {
	l := New(n + 8)
	for i := 0; i < n; i++ {
		l.model.Upsert(core.Window{
			ID:      core.WindowID(i + 1),
			App:     core.AppID(100 + i),
			Focus:   core.FocusSeq(i + 1),
			Flags:   core.FlagOnScreen,
			Title:   fmt.Sprintf("Window %d", i+1),
			AppName: fmt.Sprintf("App %d", i+1),
		})
	}
	l.allowed = core.Filter(l.model, l.Rules, l.allowed[:0])
	l.order.RebuildFrom(l.model, l.allowed)
	l.sel.Reconcile(l.order, l.model)
	return l
}

func TestHandleSummonCycleDismiss(t *testing.T) {
	l := loadedLoop(6)

	if l.visible {
		t.Fatal("a fresh loop is not visible")
	}

	l.handle(Event{Kind: Summon})
	if !l.visible {
		t.Fatal("Summon did not make the loop visible")
	}
	first := l.sel.ID
	if first == 0 {
		t.Fatal("Summon left nothing selected")
	}

	l.handle(Event{Kind: Cycle, Dir: core.Forward})
	if l.sel.ID == first {
		t.Fatalf("Cycle did not move the selection off %d", first)
	}

	l.handle(Event{Kind: Dismiss})
	if l.visible {
		t.Fatal("Dismiss did not hide the loop")
	}
}

// Cycle before Summon is a no-op: the tap thread can emit one if the gesture recogniser and the
// loop briefly disagree, and it must not move a selection nobody is looking at.
func TestCycleBeforeSummonIsNoop(t *testing.T) {
	l := loadedLoop(6)
	before := l.sel.ID

	l.handle(Event{Kind: Cycle, Dir: core.Forward})

	if l.visible {
		t.Fatal("Cycle made the loop visible")
	}
	if l.sel.ID != before {
		t.Fatalf("Cycle moved the selection while hidden: %d -> %d", before, l.sel.ID)
	}
}

// Quit is the only Kind that tells Run to stop.
func TestHandleQuitStops(t *testing.T) {
	l := loadedLoop(2)
	if !l.handle(Event{Kind: Quit}) {
		t.Fatal("handle(Quit) = false, want true (stop the loop)")
	}
	for _, k := range []Kind{Summon, Cycle, Dismiss, Focused} {
		if l.handle(Event{Kind: k}) {
			t.Fatalf("handle(%v) = true, only Quit stops the loop", k)
		}
	}
}

// V6.8: the summon / cycle / dismiss path allocates nothing once real windows are wired through it.
// core's own kernels are benchmarked in internal/core; this is the composite the loop actually runs
// per keystroke.
func BenchmarkHandleGesture(b *testing.B) {
	l := loadedLoop(20)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.handle(Event{Kind: Summon})
		l.handle(Event{Kind: Cycle, Dir: core.Forward})
		l.handle(Event{Kind: Cycle, Dir: core.Backward})
		l.handle(Event{Kind: Dismiss})
	}
}
