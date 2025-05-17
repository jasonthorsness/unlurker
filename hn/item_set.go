package hn

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

var ErrItemNotFound = errors.New("item not found")

func (item *Item) FindRoot(ancestors ItemSet) (*Item, error) {
	current := item

	for current.Parent != nil {
		next, ok := ancestors[*current.Parent]
		if !ok {
			return nil, fmt.Errorf("parent %d not found: %w", *current.Parent, ErrItemNotFound)
		}

		current = next
	}

	return current, nil
}

func (item *Item) FindChildren(items ItemSet) (ItemSet, error) {
	results := make(ItemSet, len(item.Kids))

	for _, id := range item.Kids {
		kid, ok := items[id]
		if !ok {
			return nil, fmt.Errorf("kid %d not found: %w", id, ErrItemNotFound)
		}

		results[id] = kid
	}

	return results, nil
}

func (items ItemSet) IDs() []int {
	ids := make([]int, 0, len(items))

	for id := range items {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool {
		return ids[i] > ids[j]
	})

	return ids
}

func (items ItemSet) OrderByTimeDesc() []*Item {
	result := make([]*Item, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}

	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Time == b.Time {
			return a.ID > b.ID
		}

		return a.Time > b.Time
	})

	return result
}

func (items ItemSet) Filter(filterFunc func(item *Item) bool) ItemSet {
	result := make(ItemSet, len(items))

	for _, item := range items {
		if filterFunc(item) {
			result[item.ID] = item
		}
	}

	return result
}

func (items ItemSet) Union(other ItemSet) ItemSet {
	result := make(ItemSet, len(items)+len(other))
	for _, item := range items {
		result[item.ID] = item
	}

	for _, item := range other {
		result[item.ID] = item
	}

	return result
}

func (items ItemSet) GroupByRoot() (map[*Item]ItemSet, error) {
	results := make(map[*Item]ItemSet)

	for _, item := range items {
		root, err := item.FindRoot(items)
		if err != nil {
			return nil, err
		}

		tree := results[root]
		if tree == nil {
			tree = make(ItemSet, 1)
			results[root] = tree
		}

		results[root][item.ID] = item
	}

	return results, nil
}

func (items ItemSet) GroupByParent() (map[int]ItemSet, ItemSet, error) {
	results := make(map[int]ItemSet)
	noParent := make(ItemSet)

	for _, item := range items {
		if item.Parent == nil {
			noParent[item.ID] = item
			continue
		}

		tree := results[*item.Parent]
		if tree == nil {
			tree = make(ItemSet, 1)
			results[*item.Parent] = tree
		}

		results[*item.Parent][item.ID] = item
	}

	return results, noParent, nil
}

func (items ItemSet) GroupByBy() map[string]ItemSet {
	results := make(map[string]ItemSet)

	for _, item := range items {
		tree := results[item.By]
		if tree == nil {
			tree = make(ItemSet, 1)
			results[item.By] = tree
		}

		results[item.By][item.ID] = item
	}

	return results
}

// Methods below this point are exposed via Client

func (items ItemSet) getParents(ctx context.Context, c *Client) (ItemSet, error) {
	ids := make([]int, 0, len(items))

	for _, item := range items {
		if item.Parent != nil {
			ids = append(ids, *item.Parent)
		}
	}

	parents, err := c.GetItems(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve parent Items: %w", err)
	}

	for _, item := range parents {
		if item.Type == NullBody {
			return nil, fmt.Errorf("parent %v has null body: %w", item.ID, errNullBody)
		}
	}

	return parents, nil
}

func (items ItemSet) getAncestors(ctx context.Context, c *Client) (ItemSet, error) {
	result := make(ItemSet, len(items))
	ids := items.IDs()
	queuedAsParent := make(map[int]struct{})

	for _, id := range ids {
		queuedAsParent[id] = struct{}{}
	}

	moreIDs := make([]int, 1)

	err := c.SearchUnordered(ctx, items.IDs(), func(id int, item *Item) (bool, []int, error) {
		result[id] = item

		if item.Parent == nil {
			return true, nil, nil
		}

		_, ok := queuedAsParent[*item.Parent]
		if !ok {
			queuedAsParent[*item.Parent] = struct{}{}
			moreIDs[0] = *item.Parent

			return true, moreIDs, nil
		}

		return true, nil, nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (items ItemSet) getKids(ctx context.Context, c *Client) (ItemSet, error) {
	ids := make([]int, 0, len(items))

	for _, item := range items {
		if len(item.Kids) > 0 {
			ids = append(ids, item.Kids...)
		}
	}

	kids, err := c.GetItems(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve kids Items: %w", err)
	}

	return kids, nil
}

func (items ItemSet) getDescendants(ctx context.Context, c *Client) (ItemSet, error) {
	descendants := make(ItemSet, len(items))

	err := c.SearchUnordered(ctx, items.IDs(), func(id int, item *Item) (bool, []int, error) {
		descendants[id] = item
		return true, item.Kids, nil
	})
	if err != nil {
		return nil, err
	}

	return descendants, nil
}
