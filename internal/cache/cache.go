// Package cache is the in-memory TTL cache used for aggregation-payload
// responses (stats/timeline/projects/leaderboards/etc.). Keys are strings, values
// are the marshalled JSON bytes ready to write straight to the response — the
// handler layer builds them via cacheKey(owner|name|parts...) so per-owner
// invalidation is a prefix scan.
package cache

import (
	"strings"
	"sync"
	"time"
)

// entry holds a cached blob and its expiry deadline (monotonic via time.Now).
type entry struct {
	blob    []byte
	expires time.Time
}

// TTL is a concurrent-safe in-memory cache. Zero TTL disables caching entirely
// (Set is a no-op, Get always misses) so operators can turn caching off with
// BOOM_STATS_CACHE_TTL=0 without any code branches at the call sites.
type TTL struct {
	ttl  time.Duration
	mu   sync.RWMutex
	data map[string]entry
	stop chan struct{}
}

// New returns a TTL cache with the given entry lifetime. A background sweeper
// evicts expired entries every ttl (or 30s if ttl is very short) so a workload
// that mints many unique keys — e.g. cache-miss floods from stale cursor
// buckets — cannot grow the map without bound. When ttl == 0, no sweeper runs
// and the cache is inert.
func New(ttl time.Duration) *TTL {
	c := &TTL{
		ttl:  ttl,
		data: make(map[string]entry),
		stop: make(chan struct{}),
	}
	if ttl > 0 {
		go c.sweepLoop()
	}
	return c
}

// Get returns the cached blob for key when present and unexpired.
func (c *TTL) Get(key string) ([]byte, bool) {
	if c == nil || c.ttl == 0 {
		return nil, false
	}
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		// Expired but not yet swept — drop it now so the caller re-computes and
		// the map doesn't retain the stale copy until the next sweep tick.
		c.mu.Lock()
		if cur, still := c.data[key]; still && !cur.expires.After(e.expires) {
			delete(c.data, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	return e.blob, true
}

// Set stores blob under key with the cache's configured TTL.
func (c *TTL) Set(key string, blob []byte) {
	if c == nil || c.ttl == 0 {
		return
	}
	c.mu.Lock()
	c.data[key] = entry{blob: blob, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// InvalidatePrefix drops every entry whose key starts with prefix. An empty
// prefix clears the cache. Used to bust an owner's aggregation results after
// curation/space mutations (prefix = "<owner>|") and to drop everything on a
// restore-from-backup (prefix = "").
func (c *TTL) InvalidatePrefix(prefix string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if prefix == "" {
		c.data = make(map[string]entry)
		return
	}
	for k := range c.data {
		if strings.HasPrefix(k, prefix) {
			delete(c.data, k)
		}
	}
}

// Len reports the number of live entries (test/observability hook).
func (c *TTL) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}

// Close stops the background sweeper. Safe to call multiple times.
func (c *TTL) Close() {
	if c == nil {
		return
	}
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
}

// sweepLoop evicts expired entries at a fixed cadence. The tick is the TTL
// clamped to a floor of 30s — a 1s TTL doesn't need a 1s scan across the whole
// map; expired entries are also lazy-evicted by Get.
func (c *TTL) sweepLoop() {
	interval := max(c.ttl, 30*time.Second)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case now := <-t.C:
			c.sweep(now)
		}
	}
}

// sweep drops every entry whose expiry is before now.
func (c *TTL) sweep(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.data {
		if now.After(e.expires) {
			delete(c.data, k)
		}
	}
}
