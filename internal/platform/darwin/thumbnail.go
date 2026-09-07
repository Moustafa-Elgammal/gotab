package darwin

/*
// CoreAnimation (CALayer, and the kCAGravity* symbols P3.1's tile layers reference) is not pulled in
// by -framework AppKit at link time, so it is named here. This is the file that first put a hard
// CALayer dependency in the package; if P3.1 later links QuartzCore from panel.go this line becomes
// redundant rather than wrong.
#cgo LDFLAGS: -framework QuartzCore
#include "thumbnail.h"
*/
import "C"

import (
	"errors"
	"sync"
	"time"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
)

// ThumbRequest names one tile the panel is showing right now: the window in it, the tile's index in
// the last ShowPanel / UpdatePanel call, and the size the thumbnail must be captured at.
//
// Width and Height are PIXELS -- a point size from core.Layout times the display's backing scale.
// core.CacheKey is keyed by pixel size (a thumbnail captured for one tile size is not reusable at
// another), and darwin.Capture's bound is in pixels too.
type ThumbRequest struct {
	Window core.WindowID
	Tile   int
	Width  int
	Height int
}

// maxCaptureFails drops a window after this many failed captures in a row. D26 measured 2-10% of
// captures failing outright -- the window closed between enumeration and capture, or SCK declined --
// and that "retrying twice recovered none". Three attempts is that measured ceiling; past it the id
// is a ~57 ms stall per cycle with nothing to show, so it is abandoned for the life of this
// Prefetcher. D26: a stale entry never self-heals, so there is nothing to gain by trying again.
const maxCaptureFails = 3

// stopDrainTimeout bounds how long Stop waits for the main queue to run the release it posted there.
// Stop frees on the main thread so the free is ordered after every gt_thumbnail_set still queued for
// the same handle. With a live run loop that hop is microseconds. If nothing is draining the main
// queue -- teardown with AppKit already stopped -- Stop releases inline after this wait instead; any
// gt_thumbnail_set still queued then is one no run loop will ever execute.
const stopDrainTimeout = 2 * time.Second

// Prefetcher fills the panel's tiles with thumbnails after the panel is already on screen (D12:
// capture is ~57 ms and cannot run on the summon path). It runs ONE capture goroutine -- not a pool,
// because SCScreenshotManager serialises in the WindowServer (D12/D26), so concurrency buys nothing
// and costs threads -- which drives core.Cache and darwin.Capture and sets each visible tile's
// CALayer contents as the bitmap arrives.
//
// The memory rule (docs/ARCHITECTURE.md): every ImageRef handed to a layer is held here and released
// exactly once, on cache eviction or Stop. The panel never releases it (panel.h). Releases are
// posted to the main thread so a release cannot overtake a still-queued gt_thumbnail_set for the
// same handle.
//
// Lifecycle: NewPrefetcher once; Want after each ShowPanel / UpdatePanel (edit-in-place -- it
// captures what is newly visible and lets the rest age out of the cache); Stop on teardown. Not for
// concurrent callers: Want and Stop expect the one event-loop goroutine (docs/ARCHITECTURE.md).
type Prefetcher struct {
	cache *core.Cache

	mu      sync.Mutex
	pending []ThumbRequest // newest request set; nil once the worker has taken it
	stopped bool

	wake chan struct{} // depth-1 latch: Want pokes it, the worker drains it
	stop chan struct{} // closed by Stop
	done chan struct{} // closed when the worker returns

	// Owned by the worker goroutine until Stop has observed done; no lock.
	live  map[core.CacheKey]ImageRef
	fails map[core.WindowID]int
	dead  map[core.WindowID]bool
}

// NewPrefetcher returns a started Prefetcher whose cache holds at most capacity thumbnails. Panics
// on capacity <= 0: that is core.NewCache's rule, and an unbounded thumbnail cache is the exact
// failure that type exists to prevent.
func NewPrefetcher(capacity int) *Prefetcher {
	p := &Prefetcher{
		cache: core.NewCache(capacity),
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		live:  make(map[core.CacheKey]ImageRef),
		fails: make(map[core.WindowID]int),
		dead:  make(map[core.WindowID]bool),
	}
	go p.run()
	return p
}

// Want tells the Prefetcher which tiles the panel is showing now. Non-blocking: it copies the slice,
// publishes it, and pokes the worker. Called again, it replaces the set wholesale -- the worker
// abandons whatever of the previous set it had not captured and picks up the new one. A call after
// Stop is a no-op.
func (p *Prefetcher) Want(tiles []ThumbRequest) {
	cp := make([]ThumbRequest, len(tiles))
	copy(cp, tiles)

	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.pending = cp
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default: // already poked; the worker will see the newer pending set
	}
}

// Stop cancels in-flight work and releases every live image; LiveImages() returns to 0 once it has.
// Idempotent. After Stop the Prefetcher is inert -- build a new one for the next session.
//
// It blocks until an in-flight Capture returns. darwin.Capture has no cancellation and D12 saw a
// capture hang once in ~10 runs, which is why captureTimeout exists; Stop can therefore take up to
// that timeout in the rare bad case, and ~57 ms in the ordinary one.
func (p *Prefetcher) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	close(p.stop)
	<-p.done // the worker no longer touches live / cache / fails / dead

	var once sync.Once
	releaseAll := func() {
		once.Do(func() {
			for k, img := range p.live {
				img.Release()
				delete(p.live, k)
			}
		})
	}

	drained := make(chan struct{})
	OnMain(func() {
		// Ordered after every gt_thumbnail_set the worker posted before it stopped, so no layer is
		// mid-assignment with a handle this is about to free.
		releaseAll()
		close(drained)
	})

	t := time.NewTimer(stopDrainTimeout)
	defer t.Stop()
	select {
	case <-drained:
	case <-t.C:
		// Nothing has been queued for stopDrainTimeout, so releasing inline is safe now; the
		// sync.Once makes this and a late OnMain mutually exclusive.
		releaseAll()
	}
}

// run is the single capture goroutine.
func (p *Prefetcher) run() {
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		case <-p.wake:
		}
		for {
			p.mu.Lock()
			reqs := p.pending
			p.pending = nil
			p.mu.Unlock()
			if reqs == nil {
				break // back to waiting on wake / stop
			}
			if !p.serve(reqs) {
				return // stop observed, or nothing is capturable
			}
		}
	}
}

// serve walks one request set. Returns false to end the worker (stop, or Screen Recording is not
// granted so every capture would fail); true otherwise, including when a newer Want superseded this
// set part-way through -- run's inner loop then picks the new one up.
func (p *Prefetcher) serve(reqs []ThumbRequest) bool {
	for i := range reqs {
		select {
		case <-p.stop:
			return false
		default:
		}
		p.mu.Lock()
		superseded := p.pending != nil
		p.mu.Unlock()
		if superseded {
			return true
		}
		if !p.fetchOne(reqs[i]) {
			return false
		}
	}
	return true
}

// fetchOne ensures one tile's thumbnail exists and is on its layer. Returns false only when Screen
// Recording is not granted: there is nothing to prefetch until the user fixes that (P4.3), so the
// worker stops rather than spinning the whole set through the same failure.
func (p *Prefetcher) fetchOne(r ThumbRequest) bool {
	if r.Tile < 0 || r.Width <= 0 || r.Height <= 0 || r.Window == 0 {
		return true
	}
	if p.dead[r.Window] {
		return true
	}

	key := core.CacheKey{Window: r.Window, Width: clampU16(r.Width), Height: clampU16(r.Height)}

	if _, ok := p.live[key]; ok {
		p.cache.Touch(key) // a hit: reorders MRU, evicts nothing
		p.show(r.Tile, key)
		return true
	}

	img, err := Capture(r.Window, int(key.Width))
	if err != nil {
		if errors.Is(err, ErrNoRecording) {
			return false
		}
		p.fails[r.Window]++
		if p.fails[r.Window] >= maxCaptureFails {
			p.dead[r.Window] = true
		}
		return true
	}
	p.fails[r.Window] = 0

	// Touch's evicted slice is owned by the Cache and reused by the next Touch; drop() does not
	// call Touch, so consuming it in this loop is safe.
	for _, ev := range p.cache.Touch(key) {
		p.drop(ev)
	}
	p.live[key] = img
	p.show(r.Tile, key)
	return true
}

// show posts image `key` onto tile `index`'s layer. Idempotent on the layer -- CoreAnimation skips a
// contents assignment that does not change the pointer -- so it is issued every pass rather than
// tracked, which keeps it correct even across a panel that rebuilt its tile layers underneath us.
func (p *Prefetcher) show(index int, key core.CacheKey) {
	img, ok := p.live[key]
	if !ok {
		return
	}
	h := img.c
	OnMain(func() {
		C.gt_thumbnail_set(C.int32_t(index), h)
	})
}

// drop releases the image for an evicted key. The release is posted to the main queue, which is
// FIFO, so it runs after every gt_thumbnail_set already queued for that handle -- a key is shown
// before it can be evicted, so its set is always queued first. Without that ordering a set could
// paint a layer with a pointer drop had just freed. CoreAnimation keeps the CGImage alive for as
// long as a layer still shows it, so a tile mid-eviction does not go black; the next Want repaints
// it from a fresh capture.
func (p *Prefetcher) drop(key core.CacheKey) {
	img, ok := p.live[key]
	if !ok {
		return
	}
	delete(p.live, key)
	OnMain(func() {
		img.Release()
	})
}

// clampU16 fits a pixel dimension into core.CacheKey's uint16 field. fetchOne has already rejected
// <= 0; the ceiling is defensive against a nonsense tile size and never hit by a real layout.
func clampU16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 0xFFFF {
		return 0xFFFF
	}
	return uint16(v)
}
