package core

import (
	"context"
	"errors"
	"fmt"
)

// ErrGetterPanic is passed through to the do method when the getter panics.
var ErrGetterPanic = errors.New("getter panic")

func NewBulkWorkerPoolGetter[TKey any, TValue any](
	workerPool *WorkerPool,
	getter Getter[TKey, TValue],
	wrapError func(error) TValue,
) *BulkWorkerPoolGetter[TKey, TValue] {
	return &BulkWorkerPoolGetter[TKey, TValue]{
		workerPool: workerPool,
		getter:     getter,
		wrapError:  wrapError,
	}
}

type BulkWorkerPoolGetter[TKey any, TValue any] struct {
	workerPool *WorkerPool
	getter     Getter[TKey, TValue]
	wrapError  func(error) TValue
}

func (g *BulkWorkerPoolGetter[TKey, TValue]) Get(
	ctx context.Context,
	keys []TKey,
	do func(TKey, TValue),
) []TKey {
	return DoWork[TKey](ctx, g.workerPool, keys, func(ctx context.Context, key TKey) {
		result, err := safeRunGetter(ctx, g.getter, key)
		if err != nil {
			result = g.wrapError(err)
		}

		do(key, result)
	})
}

func safeRunGetter[TKey any, TValue any](ctx context.Context, g Getter[TKey, TValue], key TKey) (_ TValue, err error) {
	defer func() {
		r := recover()
		if r != nil {
			err = fmt.Errorf("%v: %w: %v", key, ErrGetterPanic, r)
		}
	}()

	result, err := g.Get(ctx, key)
	if err != nil {
		var d TValue
		return d, fmt.Errorf("%v: getter failed: %w", key, err)
	}

	return result, nil
}
