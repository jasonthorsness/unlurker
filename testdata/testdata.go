// Package testdata wraps some sample data from the Hacker News API.
// The data can be refreshed by running "refresh.sh".
// The package implements hn/core/Getter[string, io.ReadCloser]
// This is used in lieu of live API testing for end-to-end tests.
//
//nolint:gochecknoglobals,gochecknoinits
package testdata

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
)

var maxItemJSON []byte

//go:embed newstories.json
var newStoriesJSON []byte

var topStoriesJSON []byte

var bestStoriesJSON []byte

var userJSON []byte

//go:embed items.json.gz
var itemsJSONCompressed []byte

var items map[int][]byte

var ItemsRaw []byte

var MaxItem int

var MinItem int

var MaxTime time.Time

var MinTime time.Time

var ItemCount int

var UserID string

var UserSubmitted []int

var New []int

var Top []int

var Best []int

func init() {
	err := initItems()
	if err != nil {
		log.Fatalf("failed to initialize items test data: %v", err)
	}

	err = initLists()
	if err != nil {
		log.Fatalf("failed to initialize lists test data: %v", err)
	}
}

func initItems() error {
	gzReader, err := gzip.NewReader(bytes.NewReader(itemsJSONCompressed))
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}

	defer func() { _ = gzReader.Close() }()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		return fmt.Errorf("failed to decompress test data: %w", err)
	}

	ItemsRaw = decompressed

	items = make(map[int][]byte)

	MinTime = time.Time{}
	MinItem = math.MaxInt

	byUser := make(map[string][]int)

	maxBy := ""

	scanner := bufio.NewScanner(bytes.NewReader(decompressed))
	for scanner.Scan() {
		line := make([]byte, 0, len(scanner.Bytes()))
		line = append(line, scanner.Bytes()...)

		var temp struct {
			By   string `json:"by"`
			ID   int    `json:"id"`
			Time int64  `json:"time"`
		}

		if err = json.Unmarshal(scanner.Bytes(), &temp); err != nil {
			return fmt.Errorf("failed to extract id: %w", err)
		}

		byUser[temp.By] = append(byUser[temp.By], temp.ID)
		if len(byUser[temp.By]) > len(byUser[maxBy]) {
			maxBy = temp.By
		}

		MinItem = min(MinItem, temp.ID)
		MaxItem = max(MaxItem, temp.ID)

		t := time.Unix(temp.Time, 0)

		if t.Before(MinTime) {
			MinTime = t
		}

		if t.After(MaxTime) {
			MaxTime = t
		}

		items[temp.ID] = line
	}

	err = scanner.Err()
	if err != nil {
		return fmt.Errorf("failed to scan test data: %w", err)
	}

	ItemCount = len(items)
	UserID = maxBy

	ids, err := json.Marshal(byUser[UserID])
	if err != nil {
		return fmt.Errorf("failed to marshal user data: %w", err)
	}

	UserSubmitted = byUser[UserID]

	userJSON = []byte(
		`{"about":"This is a test","created":1173923446,"id":"` +
			UserID +
			`","karma":4307,"submitted":` +
			string(ids) +
			`}`)

	maxItemJSON = []byte(strconv.Itoa(MaxItem))

	return nil
}

func initLists() error {
	err := json.Unmarshal(newStoriesJSON, &New)
	if err != nil {
		return fmt.Errorf("failed to parse newstories.json: %w", err)
	}

	// set Top to every-other item in New
	Top = make([]int, 0, len(New)/2)
	Best = make([]int, 0, len(New)/2)

	for i := range New {
		if i%2 == 0 {
			Top = append(Top, New[i])
		} else {
			Best = append(Best, New[i])
		}
	}

	topStoriesJSON, err = json.Marshal(Top)
	if err != nil {
		return fmt.Errorf("failed to marshal top stories: %w", err)
	}

	bestStoriesJSON, err = json.Marshal(Best)
	if err != nil {
		return fmt.Errorf("failed to marshal best stories: %w", err)
	}

	return nil
}

var ErrNotFound = errors.New("not found")

var Getter = &getter{}

type getter struct{}

func (g *getter) Get(_ context.Context, key string) (io.ReadCloser, error) {
	switch key {
	case "newstories.json":
		return io.NopCloser(bytes.NewReader(newStoriesJSON)), nil
	case "topstories.json":
		return io.NopCloser(bytes.NewReader(topStoriesJSON)), nil
	case "beststories.json":
		return io.NopCloser(bytes.NewReader(bestStoriesJSON)), nil
	case "maxitem.json":
		return io.NopCloser(bytes.NewReader(maxItemJSON)), nil
	default:
		if strings.HasPrefix(key, "user/") {
			return io.NopCloser(bytes.NewReader(userJSON)), nil
		}

		if !strings.HasPrefix(key, "item/") {
			return nil, ErrNotFound
		}

		id, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(key, "item/"), ".json"))
		if err != nil {
			return nil, ErrNotFound
		}

		v, ok := items[id]
		if !ok {
			return io.NopCloser(bytes.NewReader([]byte("null"))), nil
		}

		return io.NopCloser(bytes.NewReader(v)), nil
	}
}

// This clock starts ticking when the package is initialized from the max time present in the test data.
// This allows realistic testing of "active" functionality.
var Clock = &clock{time.Now()}

type clock struct {
	start time.Time
}

func (c *clock) Now() time.Time {
	return MaxTime.Add(time.Since(c.start))
}

func (c *clock) Sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
