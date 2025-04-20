package core

import (
	"sync"
	"testing"
	"time"
)

func TestMapCache_PutGet(t *testing.T) {
	t.Parallel()

	ttl := time.Second
	clock := &testClock{time.Unix(0, 0)}

	cache := NewMapCache[string, int](clock, ttl)

	cache.Put("one", 1)
	cache.Put("two", 2)

	found, remaining := cache.Get([]string{"one", "two"})

	numFound := len(found)
	numRemaining := len(remaining)

	if numFound != 2 {
		t.Errorf("Expected 2 found items, but got %d", numFound)
	}

	if numRemaining != 0 {
		t.Errorf("Expected 0 remaining items, but got %d", numRemaining)
	}

	for _, item := range found {
		switch item.Key {
		case "one":
			if item.Value != 1 {
				t.Errorf(`Expected key "one" to have value 1, but got %d`, item.Value)
			}
		case "two":
			if item.Value != 2 {
				t.Errorf(`Expected key "two" to have value 2, but got %d`, item.Value)
			}
		default:
			t.Errorf("Unexpected key found: %v", item.Key)
		}
	}
}

func TestMapCache_Expiration(t *testing.T) {
	t.Parallel()

	ttl := time.Second
	fc := &testClock{time.Unix(0, 0)}

	cache := NewMapCache[string, int](fc, ttl)

	cache.Put("one", 1)

	found, remaining := cache.Get([]string{"one"})

	numFound := len(found)
	numRemaining := len(remaining)

	if numFound != 1 {
		t.Errorf("Expected 1 found item, but got %d", numFound)
	}

	if numRemaining != 0 {
		t.Errorf("Expected 0 remaining items, but got %d", numRemaining)
	}

	fc.Advance(2 * ttl)

	found, remaining = cache.Get([]string{"one"})

	numFound = len(found)
	numRemaining = len(remaining)

	if numFound != 0 {
		t.Errorf("Expected 0 found items after expiration, but got %d", numFound)
	}

	if numRemaining != 1 {
		t.Errorf("Expected 1 remaining (expired) item, but got %d", numRemaining)
	}
}

func TestMapCache_Purging(t *testing.T) {
	t.Parallel()

	const keyCount = 10

	ttl := (keyCount - 1) * time.Second
	clock := &testClock{time.Unix(0, 0)}
	cache := NewMapCache[int, int](clock, ttl)

	keys := make([]int, keyCount)
	for i := range keyCount {
		keys[i] = i
		cache.Put(i, i)
		clock.Advance(1 * time.Second)
	}

	os, ns := 0, 0

	for i := 0; time.Duration(i)*time.Second < ttl*3; i++ {
		// with a 9 second TTL, and 10 keys, putting 1 per second, there will always be 9 found and 1 expired
		f, nf := cache.Get(keys)
		if len(f) != 9 || len(nf) != 1 {
			t.Fatalf("asd")
		}

		cache.Put(nf[0], nf[0])
		clock.Advance(1 * time.Second)

		os = max(len(cache.old()), os)
		ns = max(len(cache.new()), ns)
	}

	// max of "old" cache is 10, max of "new" cache is 9 (swap triggered on every 10th put after the insert)
	if os != 10 || ns != 9 {
		t.Fatalf("os: %d, ns: %d", os, ns)
	}
}

func TestMapCache_Concurrent(t *testing.T) {
	t.Parallel()

	ttl := time.Second
	fc := &testClock{time.Unix(0, 0)}
	cache := NewMapCache[string, int](fc, ttl)

	var wg sync.WaitGroup
	keys := []string{"one", "two", "three", "four", "five"}

	for index, key := range keys {
		wg.Add(1)

		val, k := index, key

		go func() {
			defer wg.Done()
			cache.Put(k, val)
		}()
	}

	wg.Wait()

	for range 10 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			foundResults, remainingResults := cache.Get(keys)

			if len(foundResults)+len(remainingResults) != len(keys) {
				t.Errorf("Expected total keys %d, but got found %d and remaining %d",
					len(keys), len(foundResults), len(remainingResults))
			}
		}()
	}

	wg.Wait()
}
