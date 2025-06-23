package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type BulkSingleFlightGetter[TKey comparable, TValue any] struct {
	inner       BulkGetter[TKey, TValue]
	cache       *MapCache[TKey, TValue]
	shouldCache func(TKey, TValue) bool
	pending     map[TKey][]func(TKey, TValue)
	mu          sync.Mutex
}

func NewBulkSingleFlightGetter[TKey comparable, TValue any](
	inner BulkGetter[TKey, TValue],
	cache *MapCache[TKey, TValue],
	shouldCache func(TKey, TValue) bool,
) *BulkSingleFlightGetter[TKey, TValue] {
	return &BulkSingleFlightGetter[TKey, TValue]{
		inner:       inner,
		cache:       cache,
		shouldCache: shouldCache,
		pending:     make(map[TKey][]func(TKey, TValue)),
		mu:          sync.Mutex{},
	}
}

func (g *BulkSingleFlightGetter[TKey, TValue]) Get(
	ctx context.Context,
	keys []TKey,
	do func(key TKey, value TValue),
) []TKey {
	remaining := keys

	if g.cache != nil {
		var found []MapCacheFound[TKey, TValue]

		found, remaining = g.cache.Get(keys)
		for _, e := range found {
			do(e.Key, e.Value)
		}
	}

	if len(remaining) == 0 {
		return remaining
	}

	remaining = g.addPending(remaining, do)

	if len(remaining) == 0 {
		return remaining
	}

	return g.inner.Get(ctx, remaining, func(key TKey, value TValue) {
		if g.cache != nil && g.shouldCache(key, value) {
			g.cache.Put(key, value)
		}

		dos := g.removePending(key)

		var err error
		for _, do := range dos {
			err = errors.Join(g.safeRunDo(do, key, value))
		}

		if err != nil {
			panic(err)
		}
	})
}

var ErrDoPanic = errors.New("do panic")

func (g *BulkSingleFlightGetter[TKey, TValue]) safeRunDo(
	do func(key TKey, value TValue),
	key TKey,
	value TValue,
) (err error) {
	defer func() {
		r := recover()
		if r != nil {
			err = fmt.Errorf("%v: %w: %v", key, ErrDoPanic, r)
		}
	}()

	do(key, value)

	return nil
}

const expectedPendingConcurrency = 4

func (g *BulkSingleFlightGetter[TKey, TValue]) addPending(keys []TKey, do func(key TKey, value TValue)) []TKey {
	// pre-allocate outside the lock
	doss := make([][]func(key TKey, value TValue), len(keys))

	for i := range keys {
		dos := make([]func(key TKey, value TValue), 0, expectedPendingConcurrency)
		dos = append(dos, do)
		doss[i] = dos
	}

	remaining := make([]TKey, 0, len(keys))

	g.mu.Lock()
	defer g.mu.Unlock()

	for i, key := range keys {
		dos, ok := g.pending[key]
		if ok {
			g.pending[key] = append(dos, do)
		} else {
			g.pending[key] = doss[i]

			remaining = append(remaining, key)
		}
	}

	return remaining
}

func (g *BulkSingleFlightGetter[TKey, TValue]) removePending(key TKey) []func(key TKey, value TValue) {
	g.mu.Lock()
	defer g.mu.Unlock()

	cbs := g.pending[key]
	delete(g.pending, key)

	return cbs
}
