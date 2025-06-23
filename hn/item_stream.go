package hn

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type ItemStreamValue[TItem any] struct {
	Item TItem
	Err  error
	ID   int
}

func wrapError[TItem any](err error) ItemStreamValue[TItem] {
	var d TItem
	return ItemStreamValue[TItem]{Item: d, Err: err, ID: 0}
}

type ItemStream[TItem any] struct {
	IDs         chan<- int
	Items       <-chan ItemStreamValue[TItem]
	maxInFlight int
}

var errRequestChannelFull = errors.New("request channel full")

var errResultChannelFull = errors.New("result channel full (itemStreamMaxInFlight exceeded)")

func newItemStream[TItem any](
	ctx context.Context,
	bulkItemGetter BulkStreamGetter[TItem],
	maxInFlight int,
) *ItemStream[TItem] {
	idCh := make(chan int, maxInFlight)
	resultCh := make(chan ItemStreamValue[TItem], maxInFlight)

	go func() {
		defer close(resultCh)

		errCh := make(chan error, 1)
		var wg sync.WaitGroup

		for {
			ids, ok := greedyRead(idCh, 0)
			if !ok {
				break
			}

			for range ids {
				wg.Add(1)
			}

			r := bulkItemGetter.Get(ctx, ids, func(_ int, value ItemStreamValue[TItem]) {
				defer wg.Done()

				if !trySend(resultCh, value) {
					_ = trySend(errCh, fmt.Errorf("failed to send result: %w", errResultChannelFull))
				}
			})

			for _, id := range r {
				resultCh <- wrapError[TItem](fmt.Errorf("failed to get %d: %w", id, errRequestChannelFull))

				wg.Done()
			}
		}

		wg.Wait()
		close(errCh)

		var errs []error
		for err := range errCh {
			errs = append(errs, err)
		}

		err := errors.Join(errs...)
		if err != nil {
			resultCh <- wrapError[TItem](err)
		}
	}()

	return &ItemStream[TItem]{idCh, resultCh, maxInFlight}
}

func (s *ItemStream[TItem]) MaxInFlight() int {
	return s.maxInFlight
}

func (s *ItemStream[TItem]) Get(ids []int) (map[int]TItem, error) {
	results := make(map[int]TItem, len(ids))

	err := s.SearchUnordered(ids, func(key int, value TItem) (bool, []int, error) {
		results[key] = value
		return true, nil, nil
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (s *ItemStream[TItem]) SearchOrdered(ids []int, acc func(key int, value TItem) (bool, []int, error)) error {
	all := make(map[int]ItemStreamValue[TItem], len(ids))
	maxReadAhead, idCh, resultCh := s.maxInFlight, s.IDs, s.Items

	var outerErr error

	for outstanding := 0; len(ids) > 0; {
		end := min(len(ids), outstanding+(maxReadAhead-outstanding))
		outstanding += trySendSlice(idCh, ids[outstanding:end])

		items, ok := greedyRead(resultCh, 0)
		if !ok {
			break
		}

		ok, consumed, newIDs, err := searchOrderedBatch(all, ids, items, acc)
		if err != nil {
			outerErr = fmt.Errorf("failed to search: %w", err)
			break
		}

		if !ok {
			break
		}

		ids = ids[consumed:]
		outstanding -= consumed

		ids = append(ids, newIDs...)
	}

	close(idCh)

	return searchDrain(outerErr, resultCh)
}

func (s *ItemStream[TItem]) SearchUnordered(ids []int, acc func(key int, value TItem) (bool, []int, error)) error {
	maxReadAhead, idCh, resultCh := s.maxInFlight, s.IDs, s.Items

	var outerErr error

	for outstanding := 0; len(ids) > 0 || outstanding > 0; {
		sent := trySendSlice(idCh, ids[:min(len(ids), maxReadAhead-outstanding)])
		outstanding += sent
		ids = ids[sent:]

		items, ok := greedyRead(resultCh, 0)
		if !ok {
			break
		}

		var newIDs []int
		var err error

		for i := 0; ok && err == nil && i < len(items); i++ {
			item := items[i]

			if item.Err != nil {
				err = item.Err
				break
			}

			ok, newIDs, err = acc(item.ID, item.Item)
			ids = append(ids, newIDs...)
		}

		if err != nil {
			outerErr = fmt.Errorf("failed to accumulate: %w", err)
			break
		}

		if !ok {
			break
		}

		outstanding -= len(items)
	}

	close(idCh)

	return searchDrain(outerErr, resultCh)
}

func searchDrain[TItem any](err error, resultCh <-chan ItemStreamValue[TItem]) error {
	var errs []error

	for itemOrError := range resultCh {
		if itemOrError.Err != nil {
			errs = append(errs, itemOrError.Err)
		}
	}

	err = errors.Join(err, errors.Join(errs...))
	if err != nil {
		return fmt.Errorf("search error: %w", err)
	}

	return nil
}

func (s *ItemStream[TItem]) Advanced() (int, chan<- int, <-chan ItemStreamValue[TItem]) {
	return s.maxInFlight, s.IDs, s.Items
}

func searchOrderedBatch[TItem any](
	all map[int]ItemStreamValue[TItem],
	ids []int,
	items []ItemStreamValue[TItem],
	acc func(key int, value TItem) (bool, []int, error),
) (bool, int, []int, error) {
	for _, item := range items {
		if item.Err != nil {
			return false, 0, nil, fmt.Errorf("failed to accumulate item: %w", item.Err)
		}

		all[item.ID] = item
	}

	consumed := 0
	keepGoing := true
	var allNewIDs []int

	for ; keepGoing && consumed < len(ids); consumed++ {
		id := ids[consumed]

		item, ok := all[id]
		if !ok {
			break
		}

		delete(all, id)

		ok, newIDs, err := acc(item.ID, item.Item)
		if err != nil {
			return false, consumed, nil, fmt.Errorf("failed to accumulate item: %w", err)
		}

		keepGoing = ok

		allNewIDs = append(allNewIDs, newIDs...)
	}

	return keepGoing, consumed, allNewIDs, nil
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
	for more && (maxRead == 0 || len(result) < maxRead) {
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

func trySendSlice[T any](to chan<- T, v []T) int {
	more := true
	n := 0

	for len(v) > 0 && more {
		select {
		case to <- v[0]:
			v = v[1:]

			n++

			if len(v) == 0 {
				more = false
			}
		default:
			more = false
		}
	}

	return n
}

func trySend[T any](ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	default:
		return false
	}
}
