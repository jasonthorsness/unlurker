// Package Main implements the hn CLI tool to retrieve resources from the HN API
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jasonthorsness/unlurker/hn"
	"github.com/jasonthorsness/unlurker/hn/core"
	_ "github.com/mattn/go-sqlite3"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

type globalItemsContextKey struct{}

type globalItems struct {
	client     *hn.Client
	writer     *bufio.Writer
	outputFile *os.File
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigCh

		cancel()

		sigCh <- sig
	}()

	defaultCachePath, err := os.UserCacheDir()
	if err != nil {
		defaultCachePath = os.TempDir()
	}

	defaultCachePath = filepath.Join(defaultCachePath, "hn.db")

	rootCmd := buildCommand(nil, nil, defaultCachePath)

	err = executeWithCleanup(ctx, rootCmd)
	if err != nil {
		log.Fatal(err)
	}

	const signalExitCodeOffset = 128

	select {
	case sig := <-sigCh:
		s, ok := sig.(syscall.Signal)
		if ok {
			os.Exit(signalExitCodeOffset + int(s))
		}
	default:
	}
}

func executeWithCleanup(ctx context.Context, cmd *cobra.Command) (err error) {
	g := &globalItems{nil, nil, nil}
	ctx = context.WithValue(ctx, globalItemsContextKey{}, g)

	defer func() {
		const numOperationsToCheck = 5
		errs := make([]error, 0, numOperationsToCheck)

		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, syscall.EPIPE) {
			errs = append(errs, err)
		}

		if g.client != nil {
			errs = append(errs, g.client.Close())
		}

		if g.writer != nil {
			err = g.writer.Flush()
			if err != nil && !errors.Is(err, syscall.EPIPE) {
				errs = append(errs, err)
			}
		}

		if g.outputFile != nil {
			errs = append(errs, g.outputFile.Sync(), g.outputFile.Close())
		}

		err = errors.Join(errs...)
	}()

	err = cmd.ExecuteContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	return nil
}

func getGlobalItems(ctx context.Context) (*hn.Client, *bufio.Writer, *os.File) {
	cw := ctx.Value(globalItemsContextKey{}).(*globalItems) //nolint:forcetypeassert // typed context value
	return cw.client, cw.writer, cw.outputFile
}

var errInvalidArgs = errors.New("invalid args")

func buildCommand(getter core.Getter[string, io.ReadCloser], clock core.Clock, defaultCachePath string) *cobra.Command {
	var (
		maxConnections int
		noCache        bool
		cachePath      string
		outputPath     string
	)

	rootCmd := &cobra.Command{
		Use:           "hn [command]",
		Short:         "Hacker News CLI tool",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return setupGlobalsFunc(cmd, args, noCache, cachePath, maxConnections, outputPath, getter, clock)
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Long: "hn retrieves data from the HN API (https://github.com/HackerNews/API)",
		Example: "  hn new --limit 3\n" +
			"  hn user jasonthorsness --submitted --limit 5\n" +
			"  hn scan --limit 10000 --continue-at - -o out.json",
	}

	const defaultMaxConnections = 100

	rootCmd.PersistentFlags().IntVar(
		&maxConnections,
		"max-connections",
		defaultMaxConnections,
		"maximum TCP connections to open")
	rootCmd.PersistentFlags().BoolVar(&noCache, "no-cache", false, "disable caching")
	rootCmd.PersistentFlags().StringVar(&cachePath, "cache-path", defaultCachePath, "cache file path")
	rootCmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "output filename")

	rootCmd.AddCommand(listCmd("new"))
	rootCmd.AddCommand(listCmd("top"))
	rootCmd.AddCommand(listCmd("best"))
	rootCmd.AddCommand(userCmd())
	rootCmd.AddCommand(scanCmd())

	return rootCmd
}

func setupGlobalsFunc(
	cmd *cobra.Command,
	args []string,
	noCache bool,
	cachePath string,
	maxConnections int,
	outputPath string,
	getter core.Getter[string, io.ReadCloser],
	clock core.Clock,
) error {
	ctx := cmd.Context()
	g := ctx.Value(globalItemsContextKey{}).(*globalItems) //nolint:forcetypeassert // typed context value

	if cmd.Flags().Changed("no-cache") && cmd.Flags().Changed("cache-path") {
		return fmt.Errorf("%w: cannot provide both --no-cache and --cache-path", errInvalidArgs)
	}

	if noCache {
		cachePath = ""
	}

	var err error

	g.client, err = hn.NewClient(
		ctx,
		hn.WithMaxConnections(maxConnections),
		hn.WithFileCachePath(cachePath),
		hn.WithGetter(getter),
		hn.WithClock(clock),
	)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	if outputPath != "" && outputPath != "-" {
		outputFlags, err := getOutputFlags(cmd, args)
		if err != nil {
			return err
		}

		const outputFilePermissions = 0o644

		//nolint:gosec // G304 intended
		g.outputFile, err = os.OpenFile(
			outputPath,
			outputFlags,
			outputFilePermissions)
		if err != nil {
			return fmt.Errorf("error opening output file: %w", err)
		}

		g.writer = bufio.NewWriter(g.outputFile)
	} else {
		g.writer = bufio.NewWriter(os.Stdout)
	}

	return nil
}

func getOutputFlags(cmd *cobra.Command, args []string) (int, error) {
	subCmd, _, err := cmd.Find(args)
	if err != nil {
		return 0, fmt.Errorf("failed to find subcommand: %w", err)
	}

	outputFlags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC

	if subCmd.Use == "scan" && subCmd.Flags().Changed("continue-at") {
		c, err := subCmd.Flags().GetString("continue-at")
		if err != nil {
			return 0, fmt.Errorf("failed to get continue-at flag: %w", err)
		}

		limit, err := subCmd.Flags().GetInt("limit")
		if err != nil {
			return 0, fmt.Errorf("failed to get limit flag: %w", err)
		}

		if c == "" || c == "-" || limit != 0 {
			// "--continue-at -" or with a limit requires reading an existing file
			outputFlags = os.O_RDWR | os.O_APPEND | os.O_CREATE
		} else {
			// otherwise we can create, but we don't truncate if it exists
			outputFlags = os.O_WRONLY | os.O_APPEND | os.O_CREATE
		}
	}

	return outputFlags, nil
}

func listCmd(list string) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   list,
		Short: fmt.Sprintf("Retrieve items from the %s list", list),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, writer, _ := getGlobalItems(ctx)

			var getIDs func(context.Context) ([]int, error)
			switch list {
			case "new":
				getIDs = client.GetNew
			case "top":
				getIDs = client.GetTop
			case "best":
				getIDs = client.GetBest
			default:
				return fmt.Errorf("%w: unrecognized list", errInvalidArgs)
			}

			return runList(ctx, client, writer, limit, getIDs)
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "limit number of items")

	return cmd
}

func userCmd() *cobra.Command {
	var limit int
	var submitted bool

	cmd := &cobra.Command{
		Use:   "user [username]",
		Short: "Retrieve a user's profile or their submitted items",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, writer, _ := getGlobalItems(ctx)
			user, err := client.GetUser(ctx, args[0])
			if err != nil {
				return fmt.Errorf("failed to retrieve user: %w", err)
			}
			if !submitted {
				if limit != 0 {
					return fmt.Errorf("%w: can only provide user --limit with --submitted ", errInvalidArgs)
				}

				err = json.NewEncoder(writer).Encode(user)
				if err != nil {
					return fmt.Errorf("failed to write to output: %w", err)
				}
			} else {
				err = runList(ctx, client, writer, limit, func(_ context.Context) ([]int, error) {
					return user.Submitted, nil
				})
				if err != nil {
					return fmt.Errorf("failed to retrieve user items: %w", err)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&submitted, "submitted", "s", false, "retrieve the user's submitted items")

	cmd.Flags().Lookup("submitted").NoOptDefVal = "true"

	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "limit number of items retrieved")

	return cmd
}

const continueAtStart = -1

func scanCmd() *cobra.Command {
	var (
		limit      int
		continueAt string
		ascending  bool
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Retrieve a range of items from the HN API",
		Long: "For a resumable scan (recommended), use a -o <output file> and specify --continue-at -.\n" +
			"For best performance, you might want to increase --max-connections to 400 or more.\n" +
			"If you are scanning a huge range, consider --no-cache or your cache will become very large.",
		Example: "  hn scan --max-connections 400 --no-cache --limit 100000 -c- -o out.json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, writer, outputFile := getGlobalItems(ctx)
			from := continueAtStart
			remaining := limit
			if remaining == 0 {
				remaining = math.MaxInt
			}

			var err error
			if continueAt != "" {
				from, remaining, err = resolveContinueAt(outputFile, limit, ascending, continueAt)
				if err != nil {
					return err
				}
			}

			if remaining == 0 {
				return nil
			}

			maxItem, err := client.GetMaxItem(ctx)
			if err != nil {
				return fmt.Errorf("failed to get max item: %w", err)
			}

			if from == continueAtStart {
				if ascending {
					from = 1
				} else {
					from = maxItem
				}
			}

			var to int
			if ascending {
				remaining = min(remaining, (maxItem+1)-from)
				to = from + remaining
			} else {
				to = max(1, from-remaining)
			}

			if from == to {
				return nil
			}

			return runScan(ctx, client, writer, from, to, ascending)
		},
	}

	cmd.Flags().BoolVar(&ascending, "asc", false, "Sort results in ascending order")
	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "Limit the number of results (0 for no limit)")
	cmd.Flags().StringVarP(&continueAt, "continue-at", "c", "", "Continue from a previous scan and/or item number")

	return cmd
}

func runList(
	ctx context.Context,
	client *hn.Client,
	writer *bufio.Writer,
	limit int,
	getIDs func(context.Context) ([]int, error),
) error {
	ids, err := getIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get item ids: %w", err)
	}

	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}

	return client.Advanced().NewRawItemStream(ctx).SearchOrdered(
		ids,
		func(_ int, item io.ReadCloser) (bool, []int, error) {
			defer func() { _ = item.Close() }()

			if _, err := io.Copy(writer, item); err != nil {
				return false, nil, fmt.Errorf("failed to write item: %w", err)
			}

			if _, err := writer.Write([]byte{'\n'}); err != nil {
				return false, nil, fmt.Errorf("failed to write newline: %w", err)
			}

			return true, nil, nil
		})
}

func runScan(ctx context.Context, client *hn.Client, writer *bufio.Writer, from int, to int, ascending bool) error {
	rawItemStream := client.Advanced().NewRawItemStream(ctx)
	remaining := max(from-to, to-from)

	bar := progressbar.NewOptions(remaining,
		progressbar.OptionSetDescription("Scanning"),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionThrottle(1*time.Second),
		progressbar.OptionSetWriter(os.Stderr),
	)

	defer func() {
		if remaining == 0 {
			_ = bar.Close()
		} else {
			_ = bar.Exit()
		}

		_, _ = os.Stderr.Write([]byte{'\n'})
	}()

	var ids []int
	from, ids = initializeScanIDs(rawItemStream.MaxInFlight(), from, ascending)

	next := make([]int, 1)

	return rawItemStream.SearchOrdered(ids, func(_ int, item io.ReadCloser) (bool, []int, error) {
		defer func() { _ = item.Close() }()

		_, err := io.Copy(writer, item)
		if err != nil {
			return false, nil, fmt.Errorf("failed to write item: %w", err)
		}

		_, err = writer.Write([]byte{'\n'})
		if err != nil {
			return false, nil, fmt.Errorf("failed to write newline: %w", err)
		}

		remaining--
		_ = bar.Add(1)

		if remaining == 0 {
			return false, nil, nil
		}

		if (ascending && from <= to) || (from >= to) {
			next[0] = from

			if ascending {
				from++
			} else {
				from--
			}

			return true, next, nil
		}

		return true, nil, nil
	})
}

func initializeScanIDs(maxInFlight int, from int, ascending bool) (int, []int) {
	// MaxInFlight queued, MaxInFlight in flight, MaxInFlight waiting for in-order processing
	const scanWindowMultipliers = 3
	scanWindowLength := maxInFlight * scanWindowMultipliers

	ids := make([]int, 0, scanWindowLength)

	n := 0
	for n < scanWindowLength {
		ids = append(ids, from)

		n++

		if ascending {
			from++
		} else {
			from--
		}
	}

	return from, ids
}
