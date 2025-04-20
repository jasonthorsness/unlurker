package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
)

var ErrCannotContinue = errors.New("cannot continue from provided file")

func resolveContinueAt(f *os.File, limit int, ascending bool, continueAt string) (int, int, error) {
	remaining := math.MaxInt

	if limit != 0 {
		var err error

		remaining, err = skipLinesInFile(f, limit)
		if err != nil {
			return 0, 0, err
		}
	}

	if continueAt == "-" {
		if f == nil {
			return 0, 0, fmt.Errorf("%w:--continue-at - requires --output", errInvalidArgs)
		}

		from, err := resolveContinueAtAuto(f, ascending)
		if err != nil {
			return 0, 0, err
		}

		return from, remaining, nil
	}

	from, err := strconv.Atoi(continueAt)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: unsupported value for --continue-at: %s: %w", errInvalidArgs, continueAt, err)
	}

	return from, remaining, nil
}

func resolveContinueAtAuto(f *os.File, ascending bool) (int, error) {
	last, err := lastIDs(f, 2)
	if err != nil {
		return 0, fmt.Errorf("unable to read existing lines in file for --continue-at -: %w", err)
	}

	if len(last) > 1 && last[0] > last[1] != ascending {
		return 0, fmt.Errorf("%w:--asc must match the previous direction for --continue-at", errInvalidArgs)
	}

	switch {
	case len(last) > 0 && ascending:
		return last[0] + 1, nil
	case len(last) > 0 && !ascending:
		return last[0] - 1, nil
	default:
		return continueAtStart, nil
	}
}

func skipLinesInFile(f *os.File, remaining int) (int, error) {
	if f == nil {
		return 0, fmt.Errorf("%w:--continue-at with --limit requires --output", errInvalidArgs)
	}

	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return 0, fmt.Errorf("unable to read existing number of lines in file for --continue-at with --limit: %w", err)
	}

	scanner := bufio.NewScanner(f)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		remaining--
		if remaining < 0 {
			return 0, fmt.Errorf("%w: existing context of output file exceeds --limit for --continue-at", ErrCannotContinue)
		}
	}

	err = scanner.Err()
	if err != nil {
		return 0, fmt.Errorf("failed to scan existing lines in output file: %w", err)
	}

	return remaining, nil
}

func lastIDs(f *os.File, needed int) ([]int, error) {
	lines, err := lastLines(f, needed)
	if err != nil {
		return nil, err
	}

	var item struct {
		ID int `json:"id"`
	}

	ids := make([]int, 0, needed)

	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i]) == 0 {
			continue
		}

		err = json.Unmarshal(lines[i], &item)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal item: %w", err)
		}

		ids = append(ids, item.ID)
	}

	return ids, nil
}

func lastLines(f *os.File, needed int) ([][]byte, error) {
	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat output file: %w", err)
	}

	fileSize := stat.Size()

	if fileSize == 0 {
		return nil, nil
	}

	const minSizeLimit = 4096
	const maxSizeLimit = 1024 * 1024

	minSize := max(1, min(fileSize, minSizeLimit))
	maxSize := min(fileSize, maxSizeLimit)

	sizes := make([]int64, 0, int(math.Log2(maxSizeLimit)-math.Log2(minSizeLimit))+1)

	for size := minSize; size < maxSize; size *= 2 {
		sizes = append(sizes, size)
	}

	sizes = append(sizes, maxSize)

	var lines [][]byte

	for _, size := range sizes {
		buf := make([]byte, size)

		_, err = f.Seek(-int64(len(buf)), io.SeekEnd)
		if err != nil {
			return nil, fmt.Errorf("failed to seek output file: %w", err)
		}

		n, err := f.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("failed to read output file: %w", err)
		}

		lines = bytes.Split(buf[:n], []byte{'\n'})

		if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
			lines = lines[:len(lines)-1]
		}

		if len(lines) >= needed {
			break
		}
	}

	return lines[max(len(lines)-needed, 0):], nil
}
