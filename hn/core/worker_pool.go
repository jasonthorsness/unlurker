package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// NewWorkerPool starts a new worker pool with the specified number of workers and work channel capacity.
// Arguments must both be positive numbers.
func NewWorkerPool(numWorkers int, workChannelCapacity int) *WorkerPool {
	w := &WorkerPool{make(chan workWrapper, workChannelCapacity), sync.WaitGroup{}}

	for range numWorkers {
		w.wg.Add(1)
		go w.workerLoop()
	}

	return w
}

// WorkerPool is a fixed-size pool of workers for arbitrary work. Incoming work is enqueued in a FIFO channel which
// the individual workers pull from.
type WorkerPool struct {
	workCh chan workWrapper
	wg     sync.WaitGroup
}

var ErrDoPanic = errors.New("do panic")

// DoWork queues work to the pool for asynchronous execution.
// 1. DoWork returns immediately often but not necessarily before do() is called for each work item.
// 2. If the work queue is full, the remaining work that was not queued is returned.
// 3. The do callback will be called exactly once for each work item that is queued and not returned.
// 5. If the do function panics, ErrDoPanic will be sent with a non-blocking send on errCh.
func DoWork[TWork any](
	ctx context.Context,
	w *WorkerPool,
	errCh chan<- error,
	works []TWork,
	do func(context.Context, TWork),
) []TWork {
	for i, work := range works {
		if trySend(w.workCh, workWrapper{ctx, errCh, wrapDo(do), work}) {
			continue
		}

		// workCh is full: return the remaining work
		return works[i:]
	}

	return nil
}

// Close stops the pool from accepting work and blocks until do returns for all pending work.
// It always returns nil but has error signature to conform to io.Closer.
func (w *WorkerPool) Close() error {
	close(w.workCh)
	w.wg.Wait()

	return nil
}

func wrapDo[TWork any](do func(context.Context, TWork)) func(context.Context, any) {
	return func(ctx context.Context, work any) {
		do(ctx, work.(TWork)) //nolint:forcetypeassert // constrained by generic
	}
}

type workWrapper struct {
	ctx     context.Context
	errorCh chan<- error
	do      func(context.Context, any)
	work    any
}

func (w *WorkerPool) workerLoop() {
	defer w.wg.Done()

	for {
		r, ok := <-w.workCh
		if !ok {
			break
		}

		w.workerLoopInner(r.ctx, r.errorCh, r.work, r.do)
	}
}

func (w *WorkerPool) workerLoopInner(ctx context.Context, errCh chan<- error, work any, do func(context.Context, any)) {
	defer func() {
		r := recover()
		if r != nil {
			_ = trySend(errCh, fmt.Errorf("%v: %w: %v", work, ErrDoPanic, r))
		}
	}()

	do(ctx, work)
}
