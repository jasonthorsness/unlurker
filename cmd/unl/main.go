// Package main implements the unl CLI tool for retrieving "active" discussions from the HN API.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jasonthorsness/unlurker/hn"
	"github.com/jasonthorsness/unlurker/hn/core"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var errInvalidArgs = errors.New("invalid args")

func main() {
	const defaultWidthOnTerminalSizeFailure = 80

	var err error
	maxWidth := 0
	defaultNoColor := false

	if term.IsTerminal(int(os.Stdout.Fd())) {
		maxWidth, _, err = term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			maxWidth = defaultWidthOnTerminalSizeFailure
		}
	} else {
		defaultNoColor = true
	}

	defaultCachePath, err := os.UserCacheDir()
	if err != nil {
		defaultCachePath = os.TempDir()
	}

	defaultCachePath = filepath.Join(defaultCachePath, "hn.db")

	cmd := buildCommand(nil, nil, maxWidth, defaultNoColor, defaultCachePath)

	err = cmd.Execute()
	if err != nil {
		log.Fatal(fmt.Errorf("failed to execute: %w", err))
	}
}

func buildCommand(
	getter core.Getter[string, io.ReadCloser],
	clock core.Clock,
	maxWidth int,
	defaultNoColor bool,
	defaultCachePath string,
) *cobra.Command {
	var (
		noCache   bool
		noColor   bool
		cachePath string
		maxAge    time.Duration
		window    time.Duration
		minBy     int
		limit     int
	)

	cmd := &cobra.Command{
		Use:   "unl",
		Short: "unl finds active discussions on news.ycombinator.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if len(args) != 0 {
				return fmt.Errorf("%w: unexpected positional arguments: %v", errInvalidArgs, args)
			}

			if cmd.Flags().Changed("no-cache") && cmd.Flags().Changed("cache-path") {
				return fmt.Errorf("%w: cannot provide both --no-cache and --cache-path", errInvalidArgs)
			}

			if noCache {
				cachePath = ""
			}

			client, err := hn.NewClient(ctx, hn.WithFileCachePath(cachePath), hn.WithGetter(getter), hn.WithClock(clock))
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			defer func(client *hn.Client) {
				err = client.Close()
				if err != nil {
					log.Fatalf("failed to close client: %v", err)
				}
			}(client)

			var now time.Time
			if clock != nil {
				now = clock.Now()
			} else {
				now = time.Now()
			}

			activeAfter := now.Add(-window)
			agedAfter := now.Add(-maxAge)

			items, allByParent, err := getActive(ctx, client, activeAfter, agedAfter, minBy, limit)
			if err != nil {
				return err
			}

			err = writeActiveToStdout(items, allByParent, now, activeAfter, noColor, maxWidth)
			if err != nil {
				return err
			}

			return nil
		},
		Long:    "unl finds active discussions on news.ycombinator.com",
		Example: "  unl --max-age 8h --window 30m --min-by 3 --limit 3",
	}

	const (
		defaultMaxAge = 24 * time.Hour
		defaultWindow = 1 * time.Hour
		defaultMinBy  = 3
	)

	cmd.Flags().DurationVar(&maxAge, "max-age", defaultMaxAge, "maximum age for items")
	cmd.Flags().DurationVar(&window, "window", defaultWindow, "time window for activity")
	cmd.Flags().IntVar(&minBy, "min-by", defaultMinBy, "minimum count of unique contributors to activity")
	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "limit the number of results")
	cmd.Flags().StringVar(&cachePath, "cache-path", defaultCachePath, "cache file path")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "disable cache")
	cmd.Flags().BoolVar(&noColor, "no-color", defaultNoColor, "disable color")

	return cmd
}

func getActive(
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

func writeActiveToStdout(
	items []*hn.Item,
	allByParent map[int]hn.ItemSet,
	now time.Time,
	activeAfter time.Time,
	noColor bool,
	maxWidth int,
) error {
	pw := prettyWriter{now, activeAfter, nil, !noColor, maxWidth}

	for _, item := range items {
		pw.writeTree(item, allByParent)
	}

	_, err := pw.WriteTo(os.Stdout)
	if err != nil {
		return fmt.Errorf("failed to write to writer: %w", err)
	}

	return nil
}
