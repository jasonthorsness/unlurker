// Package unl contains support functions for finding active items common to the unlurker CLI and the web backend
// but too specialized for the hn package.
package unl

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonthorsness/unlurker/hn"
)

func GetActive(
	ctx context.Context,
	client *hn.Client,
	activeAfter time.Time,
	agedAfter time.Time,
	minBy int,
	limit int,
) ([]*hn.Item, map[int]hn.ItemSet, error) {
	maxID, err := client.GetMaxItem(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get max item: %w", err)
	}

	all, err := client.GetActive(ctx, maxID, activeAfter)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get active items: %w", err)
	}

	allByRoot, err := all.GroupByRoot()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get group by root: %w", err)
	}

	activeRoots := getActiveRoots(allByRoot, agedAfter, activeAfter, minBy)

	items := activeRoots.Slice()
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	allByParent, _, err := all.GroupByParent()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get group by root: %w", err)
	}

	return items, allByParent, nil
}

func getActiveRoots(
	allByRoot map[*hn.Item]hn.ItemSet,
	agedAfter time.Time,
	activeAfter time.Time,
	minBy int,
) hn.ItemSet {
	activeRoots := make(hn.ItemSet, len(allByRoot))

	for root, tree := range allByRoot {
		if root.Dead || root.Deleted || !time.Unix(root.Time, 0).After(agedAfter) {
			continue
		}

		active := tree.Filter(func(item *hn.Item) bool {
			return !item.Dead && !item.Deleted && time.Unix(item.Time, 0).After(activeAfter)
		})

		if len(active.GroupByBy()) >= minBy {
			activeRoots[root.ID] = root
		}
	}

	return activeRoots
}

func PrettyFormatTitle(item *hn.Item, withURL bool) string {
	var text string

	switch {
	case item.Dead:
		text = "[dead]"
	case item.Deleted:
		text = "[deleted]"
	case item.Title != "":
		if withURL && item.URL != "" {
			text = item.Title + " (" + PrettyFormatURL(item.URL) + ")"
		} else {
			text = item.Title
		}
	default:
		text = item.Text
	}

	text = PrettyStripTags(text)

	return text
}

func PrettyFormatURL(v string) string {
	u, err := url.Parse(v)
	if err != nil {
		return ""
	}

	host := strings.TrimPrefix(u.Hostname(), "www.")
	if host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		return host + "/" + parts[0]
	}

	return host
}

var (
	linkRegex  = regexp.MustCompile(`(?i)<a\s+href="([^"]*)[^>]*">.*?</a>`)
	stripRegex = regexp.MustCompile(`(?i)(?:\s*</?(?:p|b|i|pre|code)>\s*|\s+)`)
)

func PrettyStripTags(v string) string {
	v = linkRegex.ReplaceAllString(v, "$1")
	v = stripRegex.ReplaceAllString(v, " ")
	v = strings.TrimSpace(v)

	return v
}

// PrettyFormatDuration formats a positive duration for columnar display.
// Output will align in columns if left-padded.
func PrettyFormatDuration(d time.Duration) string {
	totalMinutes := int(d.Minutes())

	const minutesPerHour = 60

	if totalMinutes < minutesPerHour {
		return fmt.Sprintf("%dm", totalMinutes)
	}

	hours := totalMinutes / minutesPerHour
	minutes := totalMinutes % minutesPerHour

	return fmt.Sprintf("%dh %2dm", hours, minutes)
}
