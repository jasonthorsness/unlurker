// Package hn exposes the hacker news API https://github.com/HackerNews/API.
// See client.go for available APIs.
package hn

import (
	"context"
	"errors"
	"io"
	"time"
)

// Client is the primary interface to the HN API.
// To create a new client, use hn.NewClient(). For more options, see new_client.go.
//
// Basic usage:
// IDs, err := client.GetNew(ctx)
// Items, err := client.GetItems(IDs)
//
// Remember to Close() the client when done.
type Client struct {
	resourceGetter        ResourceGetter
	bulkItemGetter        BulkStreamGetter[*Item]
	bulkRawItemGetter     BulkStreamGetter[io.ReadCloser]
	closers               []io.Closer
	itemStreamMaxInFlight int
}

func (c *Client) GetTop(ctx context.Context) ([]int, error) {
	return getResource[[]int](ctx, c.resourceGetter, "topstories.json")
}

func (c *Client) GetBest(ctx context.Context) ([]int, error) {
	return getResource[[]int](ctx, c.resourceGetter, "beststories.json")
}

func (c *Client) GetNew(ctx context.Context) ([]int, error) {
	return getResource[[]int](ctx, c.resourceGetter, "newstories.json")
}

func (c *Client) GetAsk(ctx context.Context) ([]int, error) {
	return getResource[[]int](ctx, c.resourceGetter, "askstories.json")
}

func (c *Client) GetShow(ctx context.Context) ([]int, error) {
	return getResource[[]int](ctx, c.resourceGetter, "showstories.json")
}

func (c *Client) GetJobs(ctx context.Context) ([]int, error) {
	return getResource[[]int](ctx, c.resourceGetter, "jobsstories.json")
}

func (c *Client) GetMaxItem(ctx context.Context) (int, error) {
	return getResource[int](ctx, c.resourceGetter, "maxitem.json")
}

type User struct {
	About     string `json:"about"`
	ID        string `json:"id"`
	Submitted []int  `json:"submitted"`
	Created   int64  `json:"created"`
	Karma     int    `json:"karma"`
}

func (c *Client) GetUser(ctx context.Context, username string) (*User, error) {
	return getResource[*User](ctx, c.resourceGetter, userPathPrefix+username+jsonSuffix)
}

type ItemType string

const (
	NullBody   ItemType = ""
	Job        ItemType = "job"
	Story      ItemType = "story"
	Comment    ItemType = "comment"
	Poll       ItemType = "poll"
	PollOption ItemType = "pollopt"
)

type Item struct {
	Parent      *int     `json:"parent"`
	Poll        *int     `json:"poll"`
	By          string   `json:"by"`
	Text        string   `json:"text"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Type        ItemType `json:"type"`
	Kids        []int    `json:"kids"`
	Parts       []int    `json:"parts"`
	Time        int64    `json:"time"`
	Descendants int      `json:"descendants"`
	ID          int      `json:"id"`
	Score       int      `json:"score"`
	Dead        bool     `json:"dead"`
	Deleted     bool     `json:"deleted"`
}

func (c *Client) GetItems(ctx context.Context, ids []int) (ItemSet, error) {
	return newItemStream(ctx, c.bulkItemGetter, c.itemStreamMaxInFlight).Get(ids)
}

// GetActive returns the active items, defined as items created after the provided time, along with their ancestors.
// It scans roughly from the most recent item, avoiding checking ids beyond one it knows was too old.
// This seems overly specialized, but it's the whole reason this package exists, so it gets to remain :P.
func (c *Client) GetActive(ctx context.Context, maxID int, activeAfter time.Time) (ItemSet, error) {
	itemStream := c.Advanced().NewItemStream(ctx)
	ids := make([]int, 0, itemStream.MaxInFlight())

	for i := itemStream.MaxInFlight(); i >= 0; i-- {
		ids = append(ids, maxID-i)
	}

	next := ids[0] - 1
	largestKnownInactiveID := 0
	queuedAsParent := make(map[int]struct{}, len(ids))
	all := make(ItemSet, len(ids))
	moreIDs := make([]int, 0, 2)

	err := itemStream.SearchUnordered(ids, func(id int, item *Item) (bool, []int, error) {
		isActive := time.Unix(item.Time, 0).After(activeAfter) && !item.Dead && !item.Deleted
		if !isActive {
			largestKnownInactiveID = max(id, largestKnownInactiveID)

			_, ok := queuedAsParent[id]
			if !ok {
				return true, nil, nil
			}
		}

		all[id] = item
		moreIDs = moreIDs[:0]

		getActiveTryEnqueueParent(item, queuedAsParent, &moreIDs)
		getActiveTryEnqueueNextID(&next, largestKnownInactiveID, queuedAsParent, &moreIDs)

		return true, moreIDs, nil
	})
	if err != nil {
		return nil, err
	}

	return all, nil
}

func getActiveTryEnqueueParent(item *Item, queuedAsParent map[int]struct{}, moreIDs *[]int) {
	if item.Parent == nil {
		return
	}

	parentID := *item.Parent

	_, ok := queuedAsParent[parentID]
	if !ok {
		queuedAsParent[parentID] = struct{}{}

		*moreIDs = append(*moreIDs, parentID)
	}
}

func getActiveTryEnqueueNextID(next *int, largestKnownInactiveID int, queuedAsParent map[int]struct{}, moreIDs *[]int) {
	for ; *next > largestKnownInactiveID; *next-- {
		_, ok := queuedAsParent[*next]
		if ok {
			continue
		}

		*moreIDs = append(*moreIDs, *next)
		*next--

		break
	}
}

func (c *Client) SearchOrdered(
	ctx context.Context,
	ids []int,
	acc func(id int, item *Item) (bool, []int, error),
) error {
	if len(ids) == 0 {
		return nil
	}

	return newItemStream(ctx, c.bulkItemGetter, c.itemStreamMaxInFlight).SearchOrdered(ids, acc)
}

func (c *Client) SearchUnordered(
	ctx context.Context,
	ids []int,
	acc func(id int, item *Item) (bool, []int, error),
) error {
	if len(ids) == 0 {
		return nil
	}

	return newItemStream(ctx, c.bulkItemGetter, c.itemStreamMaxInFlight).SearchUnordered(ids, acc)
}

type ItemSet map[int]*Item

func (c *Client) GetParents(ctx context.Context, items ItemSet) (ItemSet, error) {
	return items.getParents(ctx, c)
}

func (c *Client) GetAncestors(ctx context.Context, items ItemSet) (ItemSet, error) {
	return items.getAncestors(ctx, c)
}

func (c *Client) GetKids(ctx context.Context, items ItemSet) (ItemSet, error) {
	return items.getKids(ctx, c)
}

func (c *Client) GetDescendants(ctx context.Context, items ItemSet) (ItemSet, error) {
	return items.getDescendants(ctx, c)
}

func (c *Client) Close() error {
	errs := make([]error, 0, len(c.closers))

	for _, closer := range c.closers {
		errs = append(errs, closer.Close())
	}

	c.closers = nil

	return errors.Join(errs...)
}

func (c *Client) Advanced() AdvancedClient {
	return AdvancedClient{client: c}
}

type AdvancedClient struct {
	client *Client
}

type ResourceGetter interface {
	Get(ctx context.Context, path string, result any) error
}

type BulkStreamGetter[TItem any] interface {
	Get(ctx context.Context, errCh chan<- error, ids []int, do func(id int, value ItemStreamValue[TItem])) []int
}

func (c AdvancedClient) BulkItemGetter() BulkStreamGetter[*Item] {
	return c.client.bulkItemGetter
}

func (c AdvancedClient) BulkRawItemGetter() BulkStreamGetter[io.ReadCloser] {
	return c.client.bulkRawItemGetter
}

func (c AdvancedClient) ResourceGetter() ResourceGetter {
	return c.client.resourceGetter
}

func (c AdvancedClient) NewItemStream(ctx context.Context) *ItemStream[*Item] {
	return newItemStream(ctx, c.client.bulkItemGetter, c.client.itemStreamMaxInFlight)
}

func (c AdvancedClient) NewRawItemStream(ctx context.Context) *ItemStream[io.ReadCloser] {
	return newItemStream(ctx, c.client.bulkRawItemGetter, c.client.itemStreamMaxInFlight)
}
