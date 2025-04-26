// Package unl contains support functions for finding active items common to the unlurker CLI and the web backend
// but too specialized for the hn package.
package unl

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
		text = item.Title
		if withURL && item.URL != "" {
			text = text + " (" + PrettyFormatURL(item.URL) + ")"
		}
	default:
		text = item.Text
	}

	text = PrettyCleanText(text)

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
	linkRegex = regexp.MustCompile(`(?i)<a\s+href="([^"]*)[^>]*">.*?</a>`)

	//nolint:gochecknoglobals // excluded type
	tagStripper = strings.NewReplacer(
		"<p>", " ", "</p>", " ",
		"<b>", " ", "</b>", " ",
		"<i>", " ", "</i>", " ",
		"<pre>", " ", "</pre>", " ",
		"<code>", " ", "</code>", " ",
	)
)

func PrettyCleanText(v string) string {
	v = html.UnescapeString(v)
	v = strings.Map(func(r rune) rune {
		switch {
		case r < ' ':
			return ' '
		case r < utf8.RuneSelf || (unicode.IsPrint(r) && !unicode.IsSpace(r)):
			return r
		default:
			return ' '
		}
	}, v)

	v = tagStripper.Replace(v)
	v = linkRegex.ReplaceAllString(v, " $1 ")
	v = collapseSpaces(v)

	return v
}

func collapseSpaces(v string) string {
	sb := strings.Builder{}
	sb.Grow(len(v))

	inWord := false

	for _, r := range v {
		switch {
		case r == ' ':
			if inWord {
				sb.WriteByte(' ')
			}

			inWord = false
		case r < utf8.RuneSelf:
			sb.WriteByte(byte(r))

			inWord = true
		default:
			sb.WriteRune(r)

			inWord = true
		}
	}

	v = sb.String()

	v = strings.TrimSpace(v)

	return v
}

var smallDurations []string //nolint:gochecknoglobals // string cache

//nolint:gochecknoinits // string cache
func init() {
	const numSmallDurations = 3 * 60
	smallDurations = make([]string, numSmallDurations)

	for i := range numSmallDurations {
		smallDurations[i] = prettyFormatMinutes(i)
	}
}

// PrettyFormatDuration formats a positive duration for columnar display.
// Output will align in columns if left-padded.
func PrettyFormatDuration(d time.Duration) string {
	totalMinutes := int(d.Minutes())
	if totalMinutes < len(smallDurations) {
		return smallDurations[totalMinutes]
	}

	return prettyFormatMinutes(totalMinutes)
}

func prettyFormatMinutes(totalMinutes int) string {
	const minutesPerHour = 60

	if totalMinutes < minutesPerHour {
		return strconv.Itoa(totalMinutes) + "m"
	}

	hours := totalMinutes / minutesPerHour
	minutes := totalMinutes % minutesPerHour

	ms := strconv.Itoa(minutes)
	padding := "h "

	if len(ms) == 1 {
		padding = "h  "
	}

	return strconv.Itoa(hours) + padding + ms + "m"
}
