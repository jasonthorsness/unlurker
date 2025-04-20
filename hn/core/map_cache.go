package core

import (
	"sync"
	"time"
)

// MapCache is a map with TTL safe for concurrent readers and writers.
// It uses sync.RWLock to guard reading and writing so is optimized for more frequent reads.
// Get and Put are O(1) operations.
type MapCache[TKey comparable, TValue any] struct {
	clock     Clock
	lastPurge time.Time
	m         []map[TKey]mapCacheEntry[TValue]
	mu        sync.RWMutex
	ttl       time.Duration
	mi        int
}

// NewMapCache creates a new cache with the given TTL. Entries are expired immediately at their TTL.
func NewMapCache[TKey comparable, TValue any](clock Clock, ttl time.Duration) *MapCache[TKey, TValue] {
	return &MapCache[TKey, TValue]{
		clock,
		time.Time{},
		[]map[TKey]mapCacheEntry[TValue]{
			make(map[TKey]mapCacheEntry[TValue]),
			make(map[TKey]mapCacheEntry[TValue]),
		},
		sync.RWMutex{},
		ttl,
		0,
	}
}

type mapCacheEntry[TValue any] struct {
	added time.Time
	value TValue
}

type MapCacheFound[TKey comparable, TValue any] struct {
	Key   TKey
	Value TValue
}

// Get returns found and notFound slices for the given keys.
// The relative order of keys is preserved in the response.
func (c *MapCache[TKey, TValue]) Get(keys []TKey) ([]MapCacheFound[TKey, TValue], []TKey) {
	now := c.clock.Now()
	found := make([]MapCacheFound[TKey, TValue], 0, len(keys))
	remaining := make([]TKey, 0, len(keys))

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, k := range keys {
		v, ok := c.get(now, k)
		if ok {
			found = append(found, MapCacheFound[TKey, TValue]{k, v})
		} else {
			remaining = append(remaining, k)
		}
	}

	return found, remaining
}

func (c *MapCache[TKey, TValue]) get(now time.Time, k TKey) (TValue, bool) {
	// new entries are always put into the new map
	// so a given key in the new map will always have an added time >= the same key in the old map
	// so if a key is present in the new map, that is sufficient
	e, ok := c.new()[k]
	if !ok {
		e, ok = c.old()[k]
		if !ok {
			var d TValue
			return d, false
		}
	}

	if now.Sub(e.added) > c.ttl {
		var d TValue
		return d, false
	}

	return e.value, true
}

// Put adds an entry to the map. TTL is assessed relative to the clock time of Put. Purging of expired items from the
// internal maps via an O(1) pointer swap is also triggered on Put.
func (c *MapCache[TKey, TValue]) Put(k TKey, v TValue) {
	now := c.clock.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.new()[k] = mapCacheEntry[TValue]{now, v}

	if now.Sub(c.lastPurge) > c.ttl {
		// rotate the maps
		c.m[c.mi] = make(map[TKey]mapCacheEntry[TValue], len(c.new()))
		c.mi = (c.mi + 1) % len(c.m)
		c.lastPurge = now
	}
}

func (c *MapCache[TKey, TValue]) old() map[TKey]mapCacheEntry[TValue] {
	return c.m[c.mi]
}

func (c *MapCache[TKey, TValue]) new() map[TKey]mapCacheEntry[TValue] {
	return c.m[(c.mi+1)%len(c.m)]
}
