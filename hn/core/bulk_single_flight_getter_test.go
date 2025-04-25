package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type BulkGetterFunc[TKey comparable, TValue any] func(
	ctx context.Context,
	errCh chan<- error,
	keys []TKey,
	do func(TKey, TValue),
) []TKey

func (f BulkGetterFunc[TKey, TValue]) Get(
	ctx context.Context,
	errCh chan<- error,
	keys []TKey,
	do func(TKey, TValue),
) []TKey {
	return f(ctx, errCh, keys, do)
}

func TestSingleFlightDedupWithPanicMiddle(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	var (
		innerCalls [][]int
		started    = make(chan struct{})
		proceed    = make(chan struct{})
	)

	inner := BulkGetterFunc[int, int](func(
		_ context.Context,
		_ chan<- error,
		keys []int,
		do func(int, int),
	) []int {
		innerCalls = append(innerCalls, append([]int(nil), keys...))

		started <- struct{}{}

		<-proceed

		for _, k := range keys {
			do(k, k*10)
		}

		return nil
	})

	g := NewBulkSingleFlightGetter(inner, nil, nil)
	errCh := make(chan error, 3)

	var (
		do1Count int32
		do3Count int32
		wg       sync.WaitGroup
	)
	wg.Add(2)

	cb1 := func(_ int, _ int) {
		atomic.AddInt32(&do1Count, 1)
		wg.Done()
	}
	panicCb := func(_ int, _ int) {
		panic("boom")
	}
	cb3 := func(_ int, _ int) {
		atomic.AddInt32(&do3Count, 1)
		wg.Done()
	}

	go g.Get(ctx, errCh, []int{42}, cb1)
	<-started

	g.Get(ctx, errCh, []int{42}, panicCb)
	g.Get(ctx, errCh, []int{42}, cb3)

	proceed <- struct{}{}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for callbacks")
	}

	if len(innerCalls) != 1 {
		t.Fatalf("expected inner.Get once, got %d calls", len(innerCalls))
	}

	if got := atomic.LoadInt32(&do1Count); got != 1 {
		t.Errorf("expected do1Count=1, got %d", got)
	}

	if got := atomic.LoadInt32(&do3Count); got != 1 {
		t.Errorf("expected do3Count=1, got %d", got)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrDoPanic) {
			t.Errorf("expected ErrDoPanic, got %v", err)
		}
	default:
		t.Fatal("expected one error on errCh but got none")
	}

	select {
	case err := <-errCh:
		t.Errorf("unexpected extra error: %v", err)
	default:
	}
}
