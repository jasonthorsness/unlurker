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
	"github.com/jasonthorsness/unlurker/unl"
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
			return runCommand(cmd, args, getter, clock, noCache, cachePath, maxWidth, window, maxAge, minBy, limit, noColor)
		},
		Long:    "unl finds active discussions on news.ycombinator.com",
		Example: "  unl --max-age 8h --window 30m --min-by 3 --limit 3",
	}

	const (
		defaultMaxAge = 8 * time.Hour
		defaultWindow = 30 * time.Minute
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

func runCommand(
	cmd *cobra.Command,
	args []string,
	getter core.Getter[string, io.ReadCloser],
	clock core.Clock,
	noCache bool,
	cachePath string,
	maxWidth int,
	window time.Duration,
	maxAge time.Duration,
	minBy int,
	limit int,
	noColor bool,
) error {
	ctx := cmd.Context()

	if err := validateArgs(cmd, args, noCache); err != nil {
		return err
	}

	if noCache {
		cachePath = ""
	}

	client, err := createClient(ctx, cachePath, getter, clock)
	if err != nil {
		return err
	}
	defer closeClient(client)

	now := getCurrentTime(clock)
	activeAfter := now.Add(-window)
	agedAfter := now.Add(-maxAge)

	frontPageTimes, err := unl.FetchFrontPageTimes(ctx, now)
	if err != nil {
		_, err = fmt.Fprintf(os.Stderr, "\nWarning: Failed to adjust times for second-chance articles: %v\n", err)
		if err != nil {
			return fmt.Errorf("failed to write warning: %w", err)
		}
	}

	items, allByParent, err := unl.GetActive(ctx, client, frontPageTimes, activeAfter, agedAfter, minBy, limit)
	if err != nil {
		return err
	}

	err = writeActiveToStdout(items, allByParent, frontPageTimes, now, activeAfter, noColor, maxWidth)
	if err != nil {
		return err
	}

	return nil
}

func validateArgs(cmd *cobra.Command, args []string, _ bool) error {
	if len(args) != 0 {
		return fmt.Errorf("%w: unexpected positional arguments: %v", errInvalidArgs, args)
	}

	if cmd.Flags().Changed("no-cache") && cmd.Flags().Changed("cache-path") {
		return fmt.Errorf("%w: cannot provide both --no-cache and --cache-path", errInvalidArgs)
	}

	return nil
}

func createClient(
	ctx context.Context, cachePath string, getter core.Getter[string, io.ReadCloser], clock core.Clock,
) (*hn.Client, error) {
	client, err := hn.NewClient(ctx, hn.WithFileCachePath(cachePath), hn.WithGetter(getter), hn.WithClock(clock))
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return client, nil
}

func closeClient(client *hn.Client) {
	err := client.Close()
	if err != nil {
		log.Fatalf("failed to close client: %v", err)
	}
}

func getCurrentTime(clock core.Clock) time.Time {
	if clock != nil {
		return clock.Now()
	}

	return time.Now()
}

func writeActiveToStdout(
	items []*hn.Item,
	allByParent map[int]hn.ItemSet,
	adjustedTimes map[int]int64,
	now time.Time,
	activeAfter time.Time,
	noColor bool,
	maxWidth int,
) error {
	pw := prettyWriter{
		now:           now,
		activeAfter:   activeAfter,
		adjustedTimes: adjustedTimes,
		lines:         nil,
		maxWidth:      maxWidth,
		showColor:     !noColor,
	}

	for _, item := range items {
		pw.writeTree(item, allByParent)
	}

	_, err := pw.WriteTo(os.Stdout)
	if err != nil {
		return fmt.Errorf("failed to write to writer: %w", err)
	}

	return nil
}
