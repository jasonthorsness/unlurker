package core

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "github.com/mattn/go-sqlite3"
)

type testItemCacheEntry struct {
	bytes []byte
	ID    int   `json:"id"`
	Time  int64 `json:"time"`
}

func (e *testItemCacheEntry) GetID() int {
	return e.ID
}

func (e *testItemCacheEntry) GetTime() int64 {
	return e.Time
}

func (e *testItemCacheEntry) Bytes() []byte {
	return e.bytes
}

func newTestItemEntry(t *testing.T, id int, time int64) []byte {
	t.Helper()

	data, err := json.Marshal(struct {
		ID   int   `json:"id"`
		Time int64 `json:"time"`
	}{
		ID:   id,
		Time: time,
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func makeLogAndCheckCallback(t *testing.T, log *[]int) func(id int, r io.ReadCloser) {
	t.Helper()

	return func(id int, r io.ReadCloser) {
		defer func() { _ = r.Close() }()

		var item testItemCacheEntry

		err := json.NewDecoder(r).Decode(&item)
		if err != nil {
			t.Fatalf("json decoding failed: %v", err)
		}

		if item.GetID() != id {
			t.Fatalf("ID and value.ID mismatch: %d != %d", item.GetID(), id)
		}

		*log = append(*log, item.GetID())
	}
}

func TestFileCache_PutAndGet(t *testing.T) {
	t.Parallel()

	clock := &testClock{time.Unix(0, 0)}
	staleIf := "0"
	file := filepath.Join(t.TempDir(), "hn.db")

	fc, err := NewItemFileCache(t.Context(), clock, file, staleIf)
	if err != nil {
		t.Fatalf("NewItemFileCache failed: %v", err)
	}

	err = fc.Put(t.Context(), [][]byte{
		newTestItemEntry(t, 1, 1),
		newTestItemEntry(t, 2, 2),
		newTestItemEntry(t, 3, 3),
	})
	if err != nil {
		t.Fatalf("putToCache failed: %v", err)
	}

	did := make([]int, 0, 1)

	remaining := fc.Get(t.Context(), []int{1, 4}, makeLogAndCheckCallback(t, &did))

	sort.Ints(did)

	diff := cmp.Diff([]int{1}, did)
	if diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	sort.Ints(remaining)

	diff = cmp.Diff([]int{4}, remaining)
	if diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	did = did[:0]

	remaining = fc.Get(t.Context(), []int{1, 2, 2, 3, 1}, makeLogAndCheckCallback(t, &did))

	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining, got %d", len(remaining))
	}

	sort.Ints(did)

	diff = cmp.Diff([]int{1, 1, 2, 2, 3}, did)
	if diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	err = fc.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestFileCache_Stale(t *testing.T) {
	t.Parallel()

	clock := &testClock{time.Unix(0, 0)}
	staleIf := "refreshed < (:now - 150)"
	file := filepath.Join(t.TempDir(), "hn.db")

	fc, err := NewItemFileCache(t.Context(), clock, file, staleIf)
	if err != nil {
		t.Fatalf("NewItemFileCache failed: %v", err)
	}

	clock.Advance(time.Minute) // 60

	err = fc.Put(t.Context(), [][]byte{newTestItemEntry(t, 1, 0)})
	if err != nil {
		t.Fatalf("putToCache failed: %v", err)
	}

	clock.Advance(time.Minute) // 120

	err = fc.Put(t.Context(), [][]byte{newTestItemEntry(t, 2, 0)})
	if err != nil {
		t.Fatalf("putToCache failed: %v", err)
	}

	clock.Advance(time.Minute) // 180

	err = fc.Put(t.Context(), [][]byte{newTestItemEntry(t, 3, 0)})
	if err != nil {
		t.Fatalf("putToCache failed: %v", err)
	}

	did := make([]int, 0, 3)

	_ = fc.Get(t.Context(), []int{1, 2, 3}, makeLogAndCheckCallback(t, &did))

	sort.Ints(did)

	diff := cmp.Diff([]int{1, 2, 3}, did)
	if diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	clock.Advance(time.Minute) // 240

	did = make([]int, 0, 2)

	remaining := fc.Get(t.Context(), []int{1, 2, 3}, makeLogAndCheckCallback(t, &did))

	sort.Ints(did)

	diff = cmp.Diff([]int{2, 3}, did)
	if diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	if len(remaining) != 1 || remaining[0] != 1 {
		t.Fatalf("remaining (%d) != 1", len(remaining))
	}

	err = fc.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestFileCache_DefaultStaleIf(t *testing.T) {
	t.Parallel()

	clock := &testClock{time.Unix(0, 0)}
	file := filepath.Join(t.TempDir(), "hn.db")

	fc, err := NewItemFileCache(t.Context(), clock, file, "")
	if err != nil {
		t.Fatalf("NewItemFileCache failed: %v", err)
	}

	// created and refreshed will both be zero
	err = fc.Put(t.Context(), [][]byte{newTestItemEntry(t, 1, 0)})
	if err != nil {
		t.Fatalf("putToCache failed: %v", err)
	}

	refreshed := clock.Now()

	clock.Advance(30 * time.Second)

	for {
		switch {
		case clock.Now().Unix() < 60*60:
			clock.Advance(time.Second)
		case clock.Now().Unix() < 7*24*60*60:
			clock.Advance(time.Minute)
		default:
			clock.Advance(time.Hour)
		}

		var r []int

		r = fc.Get(t.Context(), []int{1}, func(_ int, r io.ReadCloser) { _ = r.Close() })

		refreshedSince := clock.Now().Sub(refreshed).Seconds()
		staleAt := 60 *
			(math.Log2(math.Max(0.0, float64(clock.Now().Unix())/60.0)+1.0) +
				math.Pow(float64(clock.Now().Unix())/(24.0*60.0*60.0), 3))
		expectedStale := refreshedSince > staleAt

		if clock.Now().Unix() > 60 && staleAt > float64(clock.Now().Unix()) {
			// can never not be stale as the staleAt grows faster than the clock
			break
		}

		if len(r) == 1 != expectedStale {
			t.Fatalf("expected stale %v got %v", expectedStale, len(r) == 1)
		}

		if len(r) == 1 {
			err = fc.Put(t.Context(), [][]byte{newTestItemEntry(t, 1, 0)})
			if err != nil {
				t.Fatalf("putToCache failed: %v", err)
			}

			r = fc.Get(t.Context(), []int{1}, func(_ int, r io.ReadCloser) { _ = r.Close() })

			if len(r) != 0 {
				t.Fatalf("still stale")
			}

			refreshed = clock.Now()
		}
	}

	err = fc.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

type testClock struct {
	T time.Time
}

func (c *testClock) Set(t time.Time) {
	c.T = t
}

func (c *testClock) Advance(d time.Duration) {
	c.T = c.T.Add(d)
}

func (c *testClock) Now() time.Time {
	return c.T
}

func (c *testClock) Sleep(_ context.Context, _ time.Duration) {
}
