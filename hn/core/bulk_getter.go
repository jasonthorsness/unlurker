package core

import (
	"context"
	"io"
)

type BulkGetter[TKey any, TValue any] interface {
	// Get asynchronously retrieves bulk values using keys.
	// 1. Get returns immediately often but not necessarily before do() is called for any key.
	// 2. Keys that cannot be processed because the underlying system is full are returned.
	// 3. The do callback will be called exactly once for each key that is queued and not returned.
	// 4. If duplicates are passed the do function is called for each (ex: [1,1,1] -> 3 calls).
	// 5. If the do function panics, an error will be sent with a non-blocking send on errCh.
	// 6. Generally the do function should be written to not block as the underlying system might have a fixed capacity.
	Get(ctx context.Context, errCh chan<- error, keys []TKey, do func(key TKey, value TValue)) []TKey
}

func NewBulkItemGetter(workerPool *WorkerPool, getter Getter[string, io.ReadCloser]) BulkGetter[int, io.ReadCloser] {
	return NewBulkWorkerPoolGetter(workerPool, NewItemGetter(getter), WrapErrorInReadCloser)
}

type BulkMapCacheGetter[TKey comparable, TValue any] struct {
	inner       BulkGetter[TKey, TValue]
	cache       *MapCache[TKey, TValue]
	shouldCache func(TKey, TValue) bool
}

func NewBulkMapCacheGetter[TKey comparable, TValue any](
	inner BulkGetter[TKey, TValue],
	cache *MapCache[TKey, TValue],
	shouldCache func(TKey, TValue) bool,
) *BulkMapCacheGetter[TKey, TValue] {
	return &BulkMapCacheGetter[TKey, TValue]{inner, cache, shouldCache}
}

func (g *BulkMapCacheGetter[TKey, TValue]) Get(
	ctx context.Context,
	errCh chan<- error,
	keys []TKey,
	do func(key TKey, value TValue),
) []TKey {
	found, remaining := g.cache.Get(keys)
	for _, e := range found {
		do(e.Key, e.Value)
	}

	if len(remaining) == 0 {
		return remaining
	}

	return g.inner.Get(ctx, errCh, remaining, func(key TKey, value TValue) {
		if g.shouldCache(key, value) {
			g.cache.Put(key, value)
		}

		do(key, value)
	})
}

type BulkTransformGetter[TKey any, TValueInner any, TValueOuter any] struct {
	inner     BulkGetter[TKey, TValueInner]
	transform func(TKey, TValueInner) TValueOuter
}

func NewBulkTransformGetter[TKey any, TValueInner any, TValueOuter any](
	inner BulkGetter[TKey, TValueInner],
	transform func(TKey, TValueInner) TValueOuter,
) BulkGetter[TKey, TValueOuter] {
	return &BulkTransformGetter[TKey, TValueInner, TValueOuter]{inner, transform}
}

func (g *BulkTransformGetter[TKey, TValueInner, TValueOuter]) Get(
	ctx context.Context,
	errCh chan<- error,
	keys []TKey,
	do func(TKey, TValueOuter),
) []TKey {
	return g.inner.Get(ctx, errCh, keys, func(key TKey, value TValueInner) { do(key, g.transform(key, value)) })
}
