package core

import (
	"bytes"
	"context"
	"io"
	"sync"
)

const putChannelBatchDepth = 10

func NewBulkItemFileCacheGetter(
	ctx context.Context,
	inner BulkGetter[int, io.ReadCloser],
	cache *ItemFileCache,
	putBatchSize int,
	putChannelFull func(),
	putError func(error),
) *BulkItemFileCacheGetter {
	result := &BulkItemFileCacheGetter{
		inner:          inner,
		ch:             make(chan *bytes.Buffer, putBatchSize*putChannelBatchDepth),
		pool:           &sync.Pool{New: func() any { return &bytes.Buffer{} }},
		wg:             &sync.WaitGroup{},
		cache:          cache,
		putBatchSize:   putBatchSize,
		putChannelFull: putChannelFull,
	}

	result.wg.Add(1)
	go result.put(ctx, putError)

	return result
}

// BulkItemFileCacheGetter applies an ItemFileCache to an inner bulk getter.
// It implements the same BulkGetter[int, io.ReadCloser] interface as the inner bulk getter it wraps.
// Puts to the cache are done asynchronously so they can be batched.
type BulkItemFileCacheGetter struct {
	inner          BulkGetter[int, io.ReadCloser]
	ch             chan *bytes.Buffer
	pool           *sync.Pool
	wg             *sync.WaitGroup
	cache          *ItemFileCache
	putChannelFull func()
	putBatchSize   int
}

func (g *BulkItemFileCacheGetter) Close() error {
	if g.ch != nil {
		close(g.ch)
		g.wg.Wait()
		g.ch = nil
	}

	return nil
}

// Get reads the inner reads into two buffers, one it sends to the cache, and one it passes onward.
func (g *BulkItemFileCacheGetter) Get(
	ctx context.Context,
	keys []int,
	do func(int, io.ReadCloser),
) []int {
	remaining := g.cache.Get(ctx, keys, do)
	if len(remaining) == 0 {
		return remaining
	}

	return g.inner.Get(ctx, remaining, func(key int, reader io.ReadCloser) {
		defer func() { _ = reader.Close() }()

		a := g.pool.Get().(*bytes.Buffer) //nolint:forcetypeassert // typed pool
		a.Reset()

		_, err := a.ReadFrom(reader)
		if err != nil {
			do(key, &readCloserWithError{err})
			return
		}

		b := g.pool.Get().(*bytes.Buffer) //nolint:forcetypeassert // typed pool
		b.Reset()
		b.Write(a.Bytes())

		if !trySend[*bytes.Buffer](g.ch, a) {
			g.pool.Put(a)

			g.putChannelFull()
		}

		do(key, &readCloserWithPooledBuffer{g.pool, b})
	})
}

func (g *BulkItemFileCacheGetter) put(ctx context.Context, putError func(error)) {
	defer g.wg.Done()

	for {
		v, ok := greedyRead(g.ch, g.putBatchSize)
		if !ok {
			break
		}

		func() {
			defer func() {
				for _, vv := range v {
					g.pool.Put(vv)
				}
			}()

			b := make([][]byte, len(v))
			for i, vv := range v {
				b[i] = vv.Bytes()
			}

			err := g.cache.Put(ctx, b)
			if err != nil {
				putError(err)
			}
		}()
	}
}

func greedyRead[T any](from <-chan T, maxRead int) ([]T, bool) {
	var id T
	var ok bool

	id, ok = <-from
	if !ok {
		return nil, false
	}

	var result []T
	result = append(result, id)

	more := true
	for more && len(result) < maxRead {
		select {
		case id, ok = <-from:
			if !ok {
				more = false
				break
			}

			result = append(result, id)
		default:
			more = false
		}
	}

	return result, true
}
