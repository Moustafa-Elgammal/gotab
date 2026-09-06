package core

// P1.7 — the thumbnail cache policy. Eviction order only; the bitmaps live in the platform layer
// and are released there. Nothing here is cgo, which is what lets the bound be tested on any OS.
//
// The bound buys a predictable peak footprint and a warm cache on summon (D9, D10). It is not a
// resident-size win: macOS already reclaims idle CG raster pages on its own.
//
// Every key this file drops is handed back to the caller, and that hand-off is the only signal the
// platform layer gets that a CGImage may be released. A key dropped but not reported leaks a
// multi-megabyte bitmap the Go GC cannot see, and the symptom appears nowhere near this file.
// TestCacheEveryDropReportedExactlyOnce pins it.

// NewCache returns an empty cache bounded to capacity entries.
//
// Panics on capacity <= 0. An unbounded thumbnail cache is the exact failure this type exists to
// prevent, so quietly substituting a default would hide the mistake until a long session ran out
// of memory.
func NewCache(capacity int) *Cache {
	if capacity <= 0 {
		panic("core: NewCache capacity must be > 0")
	}
	return &Cache{
		capacity: capacity,
		entries:  make([]CacheKey, 0, capacity),
		// Sized for the worst case — Reset dropping a full cache — so no method here allocates.
		evicted: make([]CacheKey, 0, capacity),
	}
}

// Len reports the number of live entries.
func (c *Cache) Len() int { return len(c.entries) }

// Capacity reports the bound this cache was built with.
func (c *Cache) Capacity() int { return c.capacity }

// Contains reports whether k is live. A live key is one the caller has not been told to release.
func (c *Cache) Contains(k CacheKey) bool { return c.index(k) >= 0 }

// Touch records use of k, making it most-recently-used, and returns the keys the caller must now
// release. On a hit the result is empty; on an insert into a full cache it holds the
// least-recently-touched key.
//
// The returned slice is owned by the Cache and reused by the next Touch or Reset. Consume it —
// release the bitmaps, or copy the keys — before calling either again, or the drops silently
// become a leak.
func (c *Cache) Touch(k CacheKey) (evicted []CacheKey) {
	// A zero-value Cache has capacity 0 and would silently degenerate to holding one entry. Fail
	// loudly instead: this type only means anything when it was built by NewCache.
	if c.capacity <= 0 {
		panic("core: Cache used without NewCache")
	}
	c.evicted = c.evicted[:0]

	if i := c.index(k); i >= 0 {
		// Move to front. copy handles i == 0 as a no-op.
		copy(c.entries[1:i+1], c.entries[:i])
		c.entries[0] = k
		return c.evicted
	}

	// A loop, not a single drop. The bound cannot move any more, so one drop per insert is what
	// actually happens — but a loop is what makes that a property of the code rather than of the
	// caller's discipline, and it costs one predicted branch.
	for len(c.entries) > 0 && len(c.entries) >= c.capacity {
		last := len(c.entries) - 1
		c.evicted = append(c.evicted, c.entries[last])
		c.entries = c.entries[:last]
	}

	c.entries = append(c.entries, CacheKey{})
	copy(c.entries[1:], c.entries[:len(c.entries)-1])
	c.entries[0] = k
	return c.evicted
}

// Evict drops k and reports whether it was live. The key is not returned through the evicted slice
// because the caller already holds it; the bool is the cue to release its bitmap, and it is false
// exactly when some earlier Touch already handed k back.
func (c *Cache) Evict(k CacheKey) bool {
	i := c.index(k)
	if i < 0 {
		return false
	}
	copy(c.entries[i:], c.entries[i+1:])
	c.entries = c.entries[:len(c.entries)-1]
	return true
}

// Reset drops every entry and returns them all, most-recently-used first, so a caller tearing the
// cache down releases each bitmap exactly once. Shares Touch's reuse contract: the slice is valid
// only until the next Touch or Reset.
func (c *Cache) Reset() (evicted []CacheKey) {
	c.evicted = append(c.evicted[:0], c.entries...)
	c.entries = c.entries[:0]
	return c.evicted
}

// index returns k's position in entries, or -1. Linear because capacity is tens of entries: the
// scan stays in one or two cache lines, where a map would cost a hash and a pointer chase per
// lookup and an allocation per insert.
func (c *Cache) index(k CacheKey) int {
	for i := range c.entries {
		if c.entries[i] == k {
			return i
		}
	}
	return -1
}
