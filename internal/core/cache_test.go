package core

import (
	"math/rand"
	"testing"
)

func cacheK(id WindowID, w, h uint16) CacheKey {
	return CacheKey{Window: id, Width: w, Height: h}
}

// cacheKN is a distinct key per n, at one tile size.
func cacheKN(n int) CacheKey { return cacheK(WindowID(n), 128, 96) }

// cacheRefTouch is an independent LRU used to pin eviction order: MRU first, drop from the tail.
// Written straight and slowly on purpose — if it and Cache agree, they are unlikely to be wrong the
// same way.
func cacheRefTouch(order []CacheKey, capacity int, k CacheKey) (next []CacheKey, dropped []CacheKey) {
	for i, e := range order {
		if e == k {
			order = append(order[:i], order[i+1:]...)
			break
		}
	}
	order = append([]CacheKey{k}, order...)
	for len(order) > capacity {
		dropped = append(dropped, order[len(order)-1])
		order = order[:len(order)-1]
	}
	return order, dropped
}

func cacheAssertOrder(t *testing.T, c *Cache, want []CacheKey) {
	t.Helper()
	if len(c.entries) != len(want) {
		t.Fatalf("entries = %v, want %v", c.entries, want)
	}
	for i := range want {
		if c.entries[i] != want[i] {
			t.Fatalf("entries = %v, want %v", c.entries, want)
		}
	}
}

func TestCacheNewCacheRejectsNonPositiveCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewCache(%d) did not panic", capacity)
				}
			}()
			NewCache(capacity)
		}()
	}
}

func TestCacheTouchInsertsAndHits(t *testing.T) {
	c := NewCache(4)
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0", c.Len())
	}

	for n := 1; n <= 4; n++ {
		if ev := c.Touch(cacheKN(n)); len(ev) != 0 {
			t.Fatalf("Touch(%d) under capacity evicted %v", n, ev)
		}
	}
	if c.Len() != 4 {
		t.Fatalf("Len = %d, want 4", c.Len())
	}
	for n := 1; n <= 4; n++ {
		if !c.Contains(cacheKN(n)) {
			t.Fatalf("Contains(%d) = false", n)
		}
	}
	if c.Contains(cacheKN(99)) {
		t.Fatal("Contains(99) = true for a key never touched")
	}

	// A hit must not grow the cache and must not evict.
	if ev := c.Touch(cacheKN(2)); len(ev) != 0 {
		t.Fatalf("Touch on a hit evicted %v", ev)
	}
	if c.Len() != 4 {
		t.Fatalf("Len after hit = %d, want 4", c.Len())
	}
}

func TestCacheEvictionOrderIsLeastRecentlyTouched(t *testing.T) {
	c := NewCache(3)
	c.Touch(cacheKN(1))
	c.Touch(cacheKN(2))
	c.Touch(cacheKN(3))
	cacheAssertOrder(t, c, []CacheKey{cacheKN(3), cacheKN(2), cacheKN(1)})

	// Re-touching 1 makes 2 the least recently used, so the next insert must drop 2, not 1.
	c.Touch(cacheKN(1))
	cacheAssertOrder(t, c, []CacheKey{cacheKN(1), cacheKN(3), cacheKN(2)})

	ev := c.Touch(cacheKN(4))
	if len(ev) != 1 || ev[0] != cacheKN(2) {
		t.Fatalf("overflow evicted %v, want [%v]", ev, cacheKN(2))
	}
	if c.Contains(cacheKN(2)) {
		t.Fatal("evicted key is still live")
	}
	cacheAssertOrder(t, c, []CacheKey{cacheKN(4), cacheKN(1), cacheKN(3)})

	// Every further insert drops exactly one, oldest first.
	for _, want := range []CacheKey{cacheKN(3), cacheKN(1), cacheKN(4)} {
		ev := c.Touch(cacheKN(int(want.Window) + 10))
		if len(ev) != 1 || ev[0] != want {
			t.Fatalf("evicted %v, want [%v]", ev, want)
		}
		if c.Len() != 3 {
			t.Fatalf("Len = %d, want 3 (cache must stay at capacity)", c.Len())
		}
	}
}

func TestCacheCapacityOfOne(t *testing.T) {
	c := NewCache(1)
	c.Touch(cacheKN(1))
	ev := c.Touch(cacheKN(2))
	if len(ev) != 1 || ev[0] != cacheKN(1) {
		t.Fatalf("evicted %v, want [%v]", ev, cacheKN(1))
	}
	if c.Len() != 1 || !c.Contains(cacheKN(2)) {
		t.Fatalf("Len = %d, Contains(2) = %v", c.Len(), c.Contains(cacheKN(2)))
	}
}

func TestCacheSizeIsPartOfTheKey(t *testing.T) {
	c := NewCache(4)
	small := cacheK(7, 64, 48)
	large := cacheK(7, 256, 192)

	c.Touch(small)
	if c.Contains(large) {
		t.Fatal("same window at a different tile size reported as cached; serving it would show a stale size")
	}
	if ev := c.Touch(large); len(ev) != 0 {
		t.Fatalf("Touch(large) evicted %v, want a second entry", ev)
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
}

func TestCacheEvictedSliceIsReusedAcrossCalls(t *testing.T) {
	c := NewCache(1)
	c.Touch(cacheKN(1))

	first := c.Touch(cacheKN(2))
	if len(first) != 1 || first[0] != cacheKN(1) {
		t.Fatalf("first = %v", first)
	}

	second := c.Touch(cacheKN(3))
	if len(second) != 1 || second[0] != cacheKN(2) {
		t.Fatalf("second = %v", second)
	}
	// The documented aliasing: the caller must consume the slice before the next Touch. Pinned
	// here so nobody "fixes" Touch into allocating a fresh slice, and so the hazard is visible.
	if &first[0] != &second[0] {
		t.Fatal("Touch returned a fresh slice; the reuse contract in its doc comment is now a lie")
	}
	if first[0] != cacheKN(2) {
		t.Fatalf("first was not overwritten by the second Touch: %v", first)
	}
}

func TestCacheEvict(t *testing.T) {
	c := NewCache(4)
	for n := 1; n <= 4; n++ {
		c.Touch(cacheKN(n))
	}

	if !c.Evict(cacheKN(3)) {
		t.Fatal("Evict of a live key returned false")
	}
	if c.Evict(cacheKN(3)) {
		t.Fatal("Evict of an already-dropped key returned true; the caller would release it twice")
	}
	if c.Evict(cacheKN(99)) {
		t.Fatal("Evict of an unknown key returned true")
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}
	// Removal must close the gap without disturbing recency of the survivors.
	cacheAssertOrder(t, c, []CacheKey{cacheKN(4), cacheKN(2), cacheKN(1)})

	// The freed slot is reusable, and re-touching an evicted key inserts it fresh.
	if ev := c.Touch(cacheKN(3)); len(ev) != 0 {
		t.Fatalf("Touch after Evict evicted %v", ev)
	}
	cacheAssertOrder(t, c, []CacheKey{cacheKN(3), cacheKN(4), cacheKN(2), cacheKN(1)})
}

func TestCacheReset(t *testing.T) {
	c := NewCache(4)
	for n := 1; n <= 3; n++ {
		c.Touch(cacheKN(n))
	}

	ev := c.Reset()
	want := []CacheKey{cacheKN(3), cacheKN(2), cacheKN(1)}
	if len(ev) != len(want) {
		t.Fatalf("Reset = %v, want %v", ev, want)
	}
	for i := range want {
		if ev[i] != want[i] {
			t.Fatalf("Reset = %v, want %v", ev, want)
		}
	}
	if c.Len() != 0 {
		t.Fatalf("Len after Reset = %d, want 0", c.Len())
	}
	for n := 1; n <= 3; n++ {
		if c.Contains(cacheKN(n)) {
			t.Fatalf("Contains(%d) = true after Reset", n)
		}
	}
	if ev := c.Reset(); len(ev) != 0 {
		t.Fatalf("second Reset = %v, want empty", ev)
	}
	// Still usable afterwards.
	if ev := c.Touch(cacheKN(1)); len(ev) != 0 || c.Len() != 1 {
		t.Fatalf("Touch after Reset: evicted %v, Len %d", ev, c.Len())
	}
}

func TestCacheCapacityIsFixedAtConstruction(t *testing.T) {
	// The bound used to be an exported field, which let a caller lower it and strand entries above
	// the new bound with nobody told to release them (D11). It is unexported now, so the only way
	// to reach a bad bound is to skip NewCache entirely — and that panics rather than degenerating
	// to a one-entry cache.
	c := NewCache(3)
	if c.Capacity() != 3 {
		t.Fatalf("Capacity() = %d, want 3", c.Capacity())
	}

	defer func() {
		if recover() == nil {
			t.Fatal("Touch on a zero-value Cache did not panic")
		}
	}()
	var zero Cache
	zero.Touch(cacheKN(1))
}

// TestCacheEveryDropReportedExactlyOnce is the test the memory rule rests on: over a long mixed
// sequence, a key is live in the caller's book-keeping if and only if it is live in the Cache. A
// key dropped without being reported shows up as a phantom live key (the leak); a key reported
// twice shows up as a double release.
func TestCacheEveryDropReportedExactlyOnce(t *testing.T) {
	const (
		capacity = 8
		keySpace = 20
		ops      = 20000
	)

	c := NewCache(capacity)
	live := make(map[CacheKey]bool)
	ref := []CacheKey(nil)
	drops := 0
	rng := rand.New(rand.NewSource(1))

	report := func(evicted []CacheKey) {
		for _, k := range evicted {
			if !live[k] {
				t.Fatalf("op reported %v as evicted, but it was not live (double release)", k)
			}
			delete(live, k)
			drops++
		}
	}

	for op := 0; op < ops; op++ {
		k := cacheKN(rng.Intn(keySpace))
		switch n := rng.Intn(100); {
		case n < 85:
			report(c.Touch(k))
			live[k] = true
			ref, _ = cacheRefTouch(ref, capacity, k)
		case n < 97:
			if c.Evict(k) != live[k] {
				t.Fatalf("op %d: Evict(%v) = %v, live = %v", op, k, !live[k], live[k])
			}
			if live[k] {
				delete(live, k)
				drops++
			}
			for i, e := range ref {
				if e == k {
					ref = append(ref[:i], ref[i+1:]...)
					break
				}
			}
		default:
			report(c.Reset())
			ref = ref[:0]
		}

		if len(live) != c.Len() {
			t.Fatalf("op %d: caller believes %d bitmaps live, cache holds %d", op, len(live), c.Len())
		}
		for k := range live {
			if !c.Contains(k) {
				t.Fatalf("op %d: %v is live for the caller but gone from the cache (leaked bitmap)", op, k)
			}
		}
		cacheAssertOrder(t, c, ref)
	}

	if drops == 0 {
		t.Fatal("the sequence never evicted anything; the test proves nothing")
	}
	t.Logf("%d ops, %d drops reported, %d still live", ops, drops, len(live))
}

func BenchmarkCacheTouchHit(b *testing.B) {
	const capacity = 32
	c := NewCache(capacity)
	for n := 0; n < capacity; n++ {
		c.Touch(cacheKN(n))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ev := c.Touch(cacheKN(i % capacity)); len(ev) != 0 {
			b.Fatalf("hit evicted %v", ev)
		}
	}
}

func BenchmarkCacheTouchMiss(b *testing.B) {
	c := NewCache(32)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Touch(cacheKN(i))
	}
}
