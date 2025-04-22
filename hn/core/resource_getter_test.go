package core

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

type fakeGetter struct {
	data map[string]string
}

func (f *fakeGetter) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s, ok := f.data[key]
	if !ok {
		panic("not expected")
	}

	return io.NopCloser(strings.NewReader(s)), nil
}

func TestResourceGetter_Get_Sanity(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &testClock{time.Unix(0, 0)}
	cache := NewMapCache[string, any](clock, time.Minute)
	getter := &fakeGetter{data: map[string]string{
		"maxitem.json":    "123",
		"newstories.json": "[1,2,3]",
	}}
	rg := NewResourceGetter(getter, cache)

	var maxItem int

	err := rg.Get(ctx, "maxitem.json", &maxItem)
	if err != nil {
		t.Fatalf("Get maxitem.json failed: %v", err)
	}

	if maxItem != 123 {
		t.Errorf("expected 123, got %d", maxItem)
	}

	getter.data["maxitem.json"] = "999"

	maxItem = 0

	err = rg.Get(ctx, "maxitem.json", &maxItem)
	if err != nil {
		t.Fatalf("Get(max) from cache failed: %v", err)
	}

	if maxItem != 123 {
		t.Errorf("cache miss: expected 123, got %d", maxItem)
	}

	var stories []int

	err = rg.Get(ctx, "newstories.json", &stories)
	if err != nil {
		t.Fatalf("Get newstories.json failed: %v", err)
	}

	want := []int{1, 2, 3}

	if !cmp.Equal(stories, want) {
		t.Errorf("expected %v, got %v", want, stories)
	}

	getter.data["newstories.json"] = "[4,5,6]"

	clock.Advance(2 * time.Minute)

	err = rg.Get(ctx, "maxitem.json", &maxItem)
	if err != nil {
		t.Fatalf("Get maxitem.json failed: %v", err)
	}

	if maxItem != 999 {
		t.Errorf("expected 123, got %d", maxItem)
	}

	err = rg.Get(ctx, "newstories.json", &stories)
	if err != nil {
		t.Fatalf("Get newstories.json failed: %v", err)
	}

	want = []int{4, 5, 6}

	if !cmp.Equal(stories, want) {
		t.Errorf("expected %v, got %v", want, stories)
	}
}
