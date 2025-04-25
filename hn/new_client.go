package hn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"github.com/jasonthorsness/unlurker/hn/core"
)

const BaseURL = "https://hacker-news.firebaseio.com/v0/"

// NewClient creates a new client.
// The default client with no options (client := hn.NewClient()) is suitable for most tasks.
// Options include WithMaxConnections, WithCacheFor, WithFileCachePath, WithLogger
// For more advanced configurations, use NewCustomClient (see implementation of buildClient).
func NewClient(ctx context.Context, options ...Option) (*Client, error) {
	co := getDefaultClientOptions()
	for _, option := range options {
		option.apply(&co)
	}

	return co.buildClient(ctx)
}

type Option struct {
	apply func(*clientOptions)
}

func WithMaxConnections(value int) Option {
	return Option{func(co *clientOptions) {
		co.maxConnections = value
	}}
}

func WithCacheFor(value time.Duration) Option {
	return Option{func(co *clientOptions) {
		co.cacheFor = value
	}}
}

func WithFileCachePath(value string) Option {
	return Option{func(co *clientOptions) {
		co.fileCachePath = value
	}}
}

func WithGetter(getter core.Getter[string, io.ReadCloser]) Option {
	return Option{func(co *clientOptions) {
		co.getter = getter
	}}
}

func WithClock(clock core.Clock) Option {
	return Option{func(co *clientOptions) {
		co.clock = clock
	}}
}

func NewCustomClient(
	resourceGetter ResourceGetter,
	bulkItemGetter BulkStreamGetter[*Item],
	bulkRawItemGetter BulkStreamGetter[io.ReadCloser],
	itemStreamMaxInFlight int,
	closers []io.Closer,
) *Client {
	return &Client{
		resourceGetter,
		bulkItemGetter,
		bulkRawItemGetter,
		closers,
		itemStreamMaxInFlight,
	}
}

type clientOptions struct {
	fileCacheErrorHandler func(error)
	getter                core.Getter[string, io.ReadCloser]
	clock                 core.Clock
	fileCachePath         string
	maxConnections        int
	cacheFor              time.Duration
}

const (
	DefaultMaxConnections = 100
	DefaultCacheFor       = 1 * time.Minute
)

var ErrFileCachePutChannelFull = errors.New("file cache put channel full")

func getDefaultClientOptions() clientOptions {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}

	return clientOptions{
		maxConnections:        DefaultMaxConnections,
		cacheFor:              DefaultCacheFor,
		fileCachePath:         path.Join(cacheDir, "hn.db"),
		fileCacheErrorHandler: nil,
		getter:                nil,
		clock:                 nil,
	}
}

func (co clientOptions) buildClient(ctx context.Context) (*Client, error) {
	const idleConnectionCacheForMultiplier = 5

	dco := co

	if dco.clock == nil {
		dco.clock = &defaultClock{}
	}

	if dco.fileCacheErrorHandler == nil {
		dco.fileCacheErrorHandler = func(error) {}
	}

	if dco.getter == nil {
		transport := &http.Transport{
			MaxIdleConns:        co.maxConnections,
			MaxIdleConnsPerHost: co.maxConnections,
			MaxConnsPerHost:     co.maxConnections,
			IdleConnTimeout:     co.cacheFor * idleConnectionCacheForMultiplier,
		}

		httpClient := &http.Client{
			Transport: transport,
		}

		dco.getter = core.NewBaseGetter(httpClient, BaseURL)
	}

	return dco.buildClientInternal(ctx)
}

func (co clientOptions) buildClientInternal(ctx context.Context) (_ *Client, err error) {
	const (
		workerPoolWorkChannelCapacityPerWorker = 4
		itemStreamMaxInFlightPerWorker         = 2
	)

	var closers []io.Closer

	defer func() {
		if err != nil {
			errs := []error{err}

			for _, closer := range closers {
				errs = append(errs, closer.Close())
			}

			err = errors.Join(errs...)
		}
	}()

	numWorkers := co.maxConnections
	workerPoolChannelCapacity := numWorkers * workerPoolWorkChannelCapacityPerWorker
	itemStreamMaxInFlight := numWorkers * itemStreamMaxInFlightPerWorker
	fileCachePutBatchSize := 100

	rg := core.NewResourceGetter(co.getter, core.NewMapCache[string, any](co.clock, 1*time.Minute))

	wp := core.NewWorkerPool(numWorkers, workerPoolChannelCapacity)
	closers = append(closers, wp)

	inner := core.NewBulkItemGetter(wp, co.getter)

	if co.fileCachePath != "" {
		cache, err := core.NewItemFileCache(ctx, co.clock, co.fileCachePath, "")
		if err != nil {
			return nil, fmt.Errorf("failed to create item file cache: %w", err)
		}

		errorHandler := co.fileCacheErrorHandler
		putChannelFull := func() error { errorHandler(ErrFileCachePutChannelFull); return nil }
		putError := func(err error) { errorHandler(err) }
		fcg := core.NewBulkItemFileCacheGetter(ctx, inner, cache, fileCachePutBatchSize, putChannelFull, putError)
		inner = fcg
		closers = append([]io.Closer{fcg, cache}, closers...)
	}

	outer := core.NewBulkTransformGetter(inner, unmarshalItemStreamValue)

	var mapCache *core.MapCache[int, ItemStreamValue[*Item]]
	var shouldCache func(int, ItemStreamValue[*Item]) bool

	if co.cacheFor != 0 {
		mapCache = core.NewMapCache[int, ItemStreamValue[*Item]](co.clock, co.cacheFor)
		shouldCache = func(_ int, item ItemStreamValue[*Item]) bool {
			return item.Err == nil && item.Item.Type != NullBody
		}
	}

	outer = core.NewBulkSingleFlightGetter(outer, mapCache, shouldCache)

	pool := &sync.Pool{New: func() any { return &bytes.Buffer{} }}
	raw := core.NewBulkTransformGetter(inner, func(id int, reader io.ReadCloser) ItemStreamValue[io.ReadCloser] {
		defer func() { _ = reader.Close() }()

		buffer := pool.Get().(*bytes.Buffer) //nolint:forcetypeassert // typed pool

		buffer.Reset()

		_, err := buffer.ReadFrom(reader)
		if err != nil {
			return wrapError[io.ReadCloser](err)
		}

		return ItemStreamValue[io.ReadCloser]{ID: id, Item: core.NewReadCloserWithPooledBuffer(pool, buffer), Err: nil}
	})

	c := NewCustomClient(rg, outer, raw, itemStreamMaxInFlight, closers)

	return c, nil
}

func unmarshalItemStreamValue(id int, reader io.ReadCloser) ItemStreamValue[*Item] {
	item, err := unmarshalItem(id, reader)
	if err != nil {
		return ItemStreamValue[*Item]{ID: id, Item: nil, Err: err}
	}

	return ItemStreamValue[*Item]{ID: id, Item: item, Err: nil}
}

func unmarshalItem(id int, reader io.ReadCloser) (_ *Item, err error) {
	defer func(reader io.ReadCloser) {
		closeErr := reader.Close()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}(reader)

	var resultOrNil *Item

	decoder := json.NewDecoder(reader)

	err = decoder.Decode(&resultOrNil)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize item: %w", err)
	}

	if resultOrNil == nil {
		var result Item
		result.ID = id
		result.Type = NullBody

		return &result, nil
	}

	if resultOrNil.ID != id {
		return nil, fmt.Errorf("resource id does not match body id: %d: %w", id, errContract)
	}

	return resultOrNil, nil
}

var errContract = errors.New("contract error")

var errNullBody = errors.New("HN API returned 'null' for the body (typical for very new Items)")

type defaultClock struct{}

func (c *defaultClock) Now() time.Time {
	return time.Now()
}

const (
	itemPathPrefix = "item/"
	userPathPrefix = "user/"
	jsonSuffix     = ".json"
)

type ResourceType interface {
	int | []int | *User
}

func getResource[T ResourceType](ctx context.Context, resourceGetter ResourceGetter, path string) (T, error) {
	var result T

	err := resourceGetter.Get(ctx, path, &result)
	if err != nil {
		return result, fmt.Errorf("failed to get path %s: %w", path, err)
	}

	return result, nil
}
