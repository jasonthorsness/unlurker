// Package unl contains support functions for finding active items common to the unlurker CLI and the web backend
// but too specialized for the hn package.
package unl

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jasonthorsness/unlurker/hn"
	"golang.org/x/sync/singleflight"
)

func GetActive(
	ctx context.Context,
	client *hn.Client,
	adjustedTimes map[int]int64,
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

	activeRoots := getActiveRoots(allByRoot, adjustedTimes, agedAfter, activeAfter, minBy)

	items := activeRoots.OrderByTimeDesc()

	items = sortItems(items, adjustedTimes)

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	allByParent, _, err := all.GroupByParent()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get group by root: %w", err)
	}

	return items, allByParent, nil
}

func sortItems(items []*hn.Item, adjustedTimes map[int]int64) []*hn.Item {
	type itemWithTime struct {
		item *hn.Item
		time int64
	}

	itemsWithTimes := make([]itemWithTime, len(items))

	for i, item := range items {
		effectiveTime := item.Time

		adjusted, ok := adjustedTimes[item.ID]
		if ok {
			effectiveTime = adjusted
		}

		itemsWithTimes[i] = itemWithTime{item, effectiveTime}
	}

	sort.Slice(itemsWithTimes, func(i, j int) bool {
		a, b := itemsWithTimes[i], itemsWithTimes[j]
		if a.time == b.time {
			return a.item.ID > b.item.ID
		}

		return a.time > b.time
	})

	sortedItems := make([]*hn.Item, len(items))
	for i, iwt := range itemsWithTimes {
		sortedItems[i] = iwt.item
	}

	return sortedItems
}

func getActiveRoots(
	allByRoot map[*hn.Item]hn.ItemSet,
	adjustedTimes map[int]int64,
	agedAfter time.Time,
	activeAfter time.Time,
	minBy int,
) hn.ItemSet {
	activeRoots := make(hn.ItemSet, len(allByRoot))

	for root, tree := range allByRoot {
		effectiveRootTime := root.Time

		adjusted, ok := adjustedTimes[root.ID]
		if ok {
			effectiveRootTime = adjusted
		}

		if root.Dead || root.Deleted || !time.Unix(effectiveRootTime, 0).After(agedAfter) {
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

type ItemWithDepth struct {
	*hn.Item
	NormalizedTime int64
	Depth          int
}

type treeTraverser struct {
	items []*ItemWithDepth
}

func FlattenTree(item *hn.Item, allByParent map[int]hn.ItemSet) []*ItemWithDepth {
	tt := &treeTraverser{items: make([]*ItemWithDepth, 0, 1+len(allByParent[item.ID]))}

	tt.traverseTreeRecurse(item, allByParent, 0)

	return tt.items
}

func (tt *treeTraverser) traverseTreeRecurse(item *hn.Item, allByParent map[int]hn.ItemSet, depth int) int64 {
	self := &ItemWithDepth{item, item.Time, depth}

	tt.items = append(tt.items, self)

	children := allByParent[item.ID]
	cc := children.OrderByTimeDesc()

	for _, child := range cc {
		self.NormalizedTime = min(self.NormalizedTime, tt.traverseTreeRecurse(child, allByParent, depth+1))
	}

	return self.NormalizedTime
}

type ActiveMapEntry uint8

const (
	ActiveMapInactive = iota
	ActiveMapSelf     = 1
	ActiveMapChild    = 2
)

func BuildActiveMap(flat []*ItemWithDepth, activeAfter time.Time) map[int]ActiveMapEntry {
	activeMap := make(map[int]ActiveMapEntry, len(flat))

	for _, item := range flat {
		active := time.Unix(item.Time, 0).After(activeAfter) && !item.Dead && !item.Deleted
		if !active {
			continue
		}

		activeMap[item.ID] |= ActiveMapSelf
		if item.Parent != nil {
			activeMap[*item.Parent] |= ActiveMapChild
		}
	}

	return activeMap
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
	if d <= 0 {
		return smallDurations[0]
	}

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

// Second-chance article functionality

var (
	fetchCache            atomic.Value       //nolint:gochecknoglobals // cache for front page times
	fetchGroup            singleflight.Group //nolint:gochecknoglobals // deduplication for front page requests
	frontPageAgeExtractor = regexp.MustCompile(
		`<span class="age" title="[^"]+\s+(\d+)"><a href="item\?id=(\d+)">([^<]+) ago</a></span>`)
)

type fetchCacheEntry struct {
	data map[int]int64
	ts   time.Time
}

var (
	errStatusNotOK                = errors.New("status not ok")
	errUnexpectedSingleflightType = errors.New("unexpected type from singleflight")
)

// FetchFrontPageTimes retrieves the current apparent times of articles on HN's front page
// for detecting second-chance articles (articles pulled from the second-chance pool).
func FetchFrontPageTimes(ctx context.Context, now time.Time) (map[int]int64, error) {
	entry, ok := fetchCache.Load().(*fetchCacheEntry)
	if ok {
		if time.Since(entry.ts) < time.Minute {
			return entry.data, nil
		}
	}

	v, err, _ := fetchGroup.Do(
		"frontpage",
		func() (interface{}, error) { return fetchFrontPageTimesInner(ctx, now) })
	if err != nil {
		return nil, fmt.Errorf("singleflight frontpage failed: %w", err)
	}

	times, ok := v.(map[int]int64)
	if !ok {
		return nil, fmt.Errorf("%w: %T", errUnexpectedSingleflightType, v)
	}

	return times, nil
}

func fetchFrontPageTimesInner(ctx context.Context, now time.Time) (interface{}, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://news.ycombinator.com", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	res, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s", errStatusNotOK, res.Status)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}

	matches := frontPageAgeExtractor.FindAllSubmatch(body, -1)
	m := make(map[int]int64, len(matches))

	for _, match := range matches {
		ts, err := strconv.ParseInt(string(match[1]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse time: %w", err)
		}

		t := time.Unix(ts, 0)

		id, err := strconv.Atoi(string(match[2]))
		if err != nil {
			return nil, fmt.Errorf("failed to parse id: %w", err)
		}

		age, gap, err := parseAge(string(match[3]))
		if err != nil {
			return nil, err
		}

		diff := now.Sub(t) - age
		if diff > gap {
			m[id] = now.Add(-age).Unix()
		} else {
			m[id] = ts
		}
	}

	fetchCache.Store(&fetchCacheEntry{
		data: m,
		ts:   time.Now(),
	})

	return m, nil
}

var errUnexpectedAgeFormat = errors.New("unexpected age format")

var relativeAgeRegex = regexp.MustCompile(
	`^\s*(\d+)\s+(hour|hours|minute|minutes|day|days)\s*$`)

func parseAge(s string) (time.Duration, time.Duration, error) {
	m := relativeAgeRegex.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("%w: %q", errUnexpectedAgeFormat, s)
	}

	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse age: %w", err)
	}

	const oneDayDuration = 24 * time.Hour

	switch m[2] {
	case "minute", "minutes":
		return time.Duration(n) * time.Minute, 1 * time.Hour, nil
	case "hour", "hours":
		return time.Duration(n) * time.Hour, 2 * time.Hour, nil
	case "day", "days":
		return time.Duration(n) * oneDayDuration, oneDayDuration, nil
	default:
		return 0, 0, fmt.Errorf("%w: %q", errUnexpectedAgeFormat, m[2])
	}
}
