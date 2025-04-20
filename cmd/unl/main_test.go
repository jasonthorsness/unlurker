//nolint:paralleltest // these tests capture stdout so can't run in parallel
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonthorsness/unlurker/testdata"
)

func TestConflictingFlags(t *testing.T) {
	_, err := exec(t, "--no-cache", "--cache-path", filepath.Join(t.TempDir(), "cache.db"))

	if err == nil {
		t.Fatal("expected error when both --no-cache and --cache-path are provided")
	}

	if !errors.Is(err, errInvalidArgs) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnknownArg(t *testing.T) {
	_, err := exec(t, "--foo")

	if err == nil {
		t.Fatal("expected error for unknown flag")
	}

	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestCachePathCreatesDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hn.db")

	_, err := exec(t, "--cache-path", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("expected cache file at %s, got error: %v", dbPath, err)
	}

	if info.Size() == 0 {
		t.Fatalf("expected non-empty cache file, got size %d", info.Size())
	}
}

func TestMaxAge(t *testing.T) {
	out, err := exec(t)
	if err != nil {
		t.Fatal(err)
	}

	const testMaxAgeHours = 4

	hasOver := false

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		duration, ok := firstDurationInLine(scanner.Text())
		if ok && duration > testMaxAgeHours*time.Hour {
			hasOver = true
		}
	}

	err = scanner.Err()
	if err != nil {
		t.Fatal(err)
	}

	if !hasOver {
		t.Fatalf("expected max age to be at least %d hours", testMaxAgeHours)
	}

	out, err = exec(t, "--max-age", fmt.Sprintf("%dh", testMaxAgeHours))
	if err != nil {
		t.Fatal(err)
	}

	scanner = bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		duration, ok := firstDurationInLine(scanner.Text())
		if ok && duration > testMaxAgeHours*time.Hour {
			t.Fatalf("expected max age to be at most %d hours", testMaxAgeHours)
		}
	}

	err = scanner.Err()
	if err != nil {
		t.Fatal(err)
	}
}

func TestWindow(t *testing.T) {
	out, err := exec(t)
	if err != nil {
		t.Fatal(err)
	}

	const testWindowMinutes = 30

	hasOver := false

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		duration, ok := firstDurationInLine(scanner.Text())
		if !ok {
			continue
		}

		if strings.Contains(scanner.Text(), colorLightBlue) {
			if duration > time.Hour {
				t.Fatalf("expected default to be one hour")
			}

			if duration > testWindowMinutes*time.Minute {
				hasOver = true
			}
		} else if duration < time.Hour {
			t.Fatalf("expected default to be one hour")
		}
	}

	err = scanner.Err()
	if err != nil {
		t.Fatal(err)
	}

	if !hasOver {
		t.Fatalf("expected active between %dm and 1h", testWindowMinutes)
	}

	out, err = exec(t, "--window", fmt.Sprintf("%dm", testWindowMinutes))
	if err != nil {
		t.Fatal(err)
	}

	scanner = bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		duration, ok := firstDurationInLine(scanner.Text())
		if !ok {
			continue
		}

		if strings.Contains(scanner.Text(), colorLightBlue) {
			if duration > testWindowMinutes*time.Minute {
				t.Fatalf("marked active outside window")
			}
		} else {
			if duration < testWindowMinutes*time.Minute {
				t.Fatalf("marked inactive outside window")
			}
		}
	}

	err = scanner.Err()
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoColorDisablesANSI(t *testing.T) {
	out, err := exec(t, "--no-color")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "\x1b[") {
		t.Fatal("expected no ANSI color codes in output")
	}
}

func TestLimitFlag(t *testing.T) {
	out, err := exec(t, "-l", "2")
	if err != nil {
		t.Fatal(err)
	}

	count := 0

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "\033[92m") {
			count++
		}
	}

	err = scanner.Err()
	if err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Fatalf("expected 1 item printed, got %d", count)
	}
}

func TestDefaultFlags(t *testing.T) {
	out, err := exec(t)
	if err != nil {
		t.Fatal(err)
	}

	if len(out) == 0 {
		t.Fatal("expected non-empty output with default flags")
	}
}

func TestDurationFlagParsing(t *testing.T) {
	_, err := exec(t, "--max-age", "notaduration")
	if err == nil {
		t.Fatal("expected error for invalid --max-age value")
	}

	_, err = exec(t, "--window", "123xyz")
	if err == nil {
		t.Fatal("expected error for invalid --window value")
	}
}

func TestCombinationOfAll(t *testing.T) {
	_, err := exec(
		t,
		"--no-cache",
		"--no-color",
		"--max-age", "1h",
		"--window", "15m",
		"--min-by", "2",
		"--limit", "2",
	)
	if err != nil {
		t.Fatal(err)
	}
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	timeRe = regexp.MustCompile(`\b(?:(\d+)h\s*)?(\d+)m\b`)
)

func firstDurationInLine(line string) (time.Duration, bool) {
	clean := ansiRe.ReplaceAllString(line, "")

	m := timeRe.FindStringSubmatch(clean)
	if m == nil {
		return 0, false
	}

	h := 0

	if m[1] != "" {
		h, _ = strconv.Atoi(m[1])
	}

	mins, _ := strconv.Atoi(m[2])

	return time.Duration(h)*time.Hour + time.Duration(mins)*time.Minute, true
}

func exec(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()

	defaultCachePath := filepath.Join(t.TempDir(), "hn.db")

	cmd := buildCommand(testdata.Getter, testdata.Clock, 120, false, defaultCachePath)
	cmd.SetArgs(args)

	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	defer func() { os.Stdout = stdout }()

	var buf bytes.Buffer
	done := make(chan error, 1)

	go func() {
		_, err := io.Copy(&buf, r)
		done <- err
	}()

	err := errors.Join(cmd.ExecuteContext(t.Context()), w.Close(), <-done)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
