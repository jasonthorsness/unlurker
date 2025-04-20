//nolint:paralleltest,gochecknoglobals // these tests capture stdout so can't run in parallel (and thus can use globals)
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jasonthorsness/unlurker/hn"
	"github.com/jasonthorsness/unlurker/testdata"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/goleak"
)

func TestHelp(t *testing.T) {
	buf, err := exec(t)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(buf), "Usage:") {
		t.Fatal("expected usage")
	}
}

func TestConflictingFlags(t *testing.T) {
	_, err := exec(t, "--no-cache", "--cache-path", filepath.Join(t.TempDir(), "cache.db"))

	if err == nil {
		t.Fatal("expected error when both --no-cache and --cache-path are provided")
	}

	if !errors.Is(err, errInvalidArgs) {
		t.Fatalf("unexpected error: %v", err)
	}
}

var useNoCache bool

var useDefaultCachePath string

var useCachePath string

func TestAllNoCache(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	useNoCache = true
	defer func() { useNoCache = false }()

	useDefaultCachePath = filepath.Join(t.TempDir(), "hn.db")
	defer func() { useDefaultCachePath = "" }()

	err := os.Remove(useDefaultCachePath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	t.Run("--no-cache", func(t *testing.T) {
		TestHelp(t)
		TestNew(t)
		TestTop(t)
		TestBest(t)
		TestUser(t)
		TestUserSubmitted(t)
		TestScan(t)
		TestScanAsc(t)
		TestScanContinue(t)
		TestScanContinueAsc(t)
	})

	_, err = os.Stat(useDefaultCachePath)
	if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestAllCheckCache(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	useCachePath = filepath.Join(t.TempDir(), "cache.db")
	defer func() { useCachePath = "" }()

	t.Run("--cache-path", func(t *testing.T) {
		TestHelp(t)
		TestNew(t)
		TestTop(t)
		TestBest(t)
		TestUser(t)
		TestUserSubmitted(t)
		TestScan(t)
		TestScanAsc(t)
		TestScanContinue(t)
		TestScanContinueAsc(t)
	})

	f, err := os.Stat(useCachePath)
	if err != nil {
		t.Fatal(err)
	}

	if f.Size() < int64(len(testdata.ItemsRaw)) {
		t.Fatalf("cache file size %d is less than expected %d", f.Size(), len(testdata.ItemsRaw))
	}
}

func TestAllCheckDefaultCache(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	useDefaultCachePath = filepath.Join(t.TempDir(), "hn.db")
	defer func() { useDefaultCachePath = "" }()

	err := os.Remove(useDefaultCachePath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	t.Run("default cache", func(t *testing.T) {
		TestHelp(t)
		TestNew(t)
		TestTop(t)
		TestBest(t)
		TestUser(t)
		TestUserSubmitted(t)
		TestScan(t)
		TestScanAsc(t)
		TestScanContinue(t)
		TestScanContinueAsc(t)
	})

	f, err := os.Stat(useDefaultCachePath)
	if err != nil {
		t.Fatal(err)
	}

	if f.Size() < int64(len(testdata.ItemsRaw)) {
		t.Fatalf("cache file size %d is less than expected %d", f.Size(), len(testdata.ItemsRaw))
	}
}

func TestUser(t *testing.T) {
	buf, err := exec(t, "user", testdata.UserID)
	if err != nil {
		t.Fatal(err)
	}

	var user *hn.User

	err = json.Unmarshal(buf, &user)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUserSubmitted(t *testing.T) {
	testListInner(t, testdata.UserSubmitted, "user", testdata.UserID, "--submitted")
	testListInner(t, testdata.UserSubmitted[:1], "user", testdata.UserID, "--submitted", "-l1")
}

func TestNew(t *testing.T) {
	testList(t, "new", testdata.New)
}

func TestTop(t *testing.T) {
	testList(t, "top", testdata.Top)
}

func TestBest(t *testing.T) {
	testList(t, "best", testdata.Best)
}

func testList(t *testing.T, list string, expected []int) {
	t.Helper()
	testListInner(t, expected, list)
	testListInner(t, expected[:10], list, "-l10")
}

func testListInner(t *testing.T, expected []int, args ...string) {
	t.Helper()

	buf, err := exec(t, args...)
	if err != nil {
		t.Fatal(err)
	}

	ids := make([]int, 0, len(expected))

	scanner := bufio.NewScanner(bytes.NewReader(buf))
	for scanner.Scan() {
		var item hn.Item

		err = json.Unmarshal(scanner.Bytes(), &item)
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, item.ID)
	}

	diff := cmp.Diff(ids, expected)
	if diff != "" {
		t.Fatalf("diff: %s", diff)
	}
}

func exec(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()

	defaultCachePath := useDefaultCachePath
	if defaultCachePath == "" {
		defaultCachePath = filepath.Join(t.TempDir(), "hn.db")
	}

	cmd := buildCommand(testdata.Getter, testdata.Clock, defaultCachePath)

	if useNoCache {
		args = append(args, "--no-cache")
	}

	if useCachePath != "" {
		args = append(args, "--cache-path", useCachePath)
	}

	if args == nil {
		args = []string{}
	}

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

	err := errors.Join(executeWithCleanup(t.Context(), cmd), w.Close(), <-done)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func TestScan(t *testing.T) {
	buf, err := exec(t, "scan", "--limit", strconv.Itoa(testdata.ItemCount))
	if err != nil {
		t.Fatal(err)
	}

	verifyFullScan(t, bytes.NewReader(buf), testdata.MaxItem, testdata.MinItem)
}

func TestScanAsc(t *testing.T) {
	buf, err := exec(t, "scan", "--asc", "--continue-at", strconv.Itoa(testdata.MinItem))
	if err != nil {
		t.Fatal(err)
	}

	verifyFullScan(t, bytes.NewReader(buf), testdata.MinItem, testdata.MaxItem)

	if !bytes.Equal(buf, testdata.ItemsRaw) {
		t.Fatalf("scan bytes differed")
	}
}

func TestScanContinue(t *testing.T) {
	o := filepath.Join(t.TempDir(), "test.json")

	// continue-at - from not exist is OK
	_, err := exec(t, "scan", "--limit", "2", "--continue-at", "-", "-o", o)
	if err != nil {
		t.Fatal(err)
	}

	// direction change fails
	_, err = exec(t, "scan", "--asc", "--limit", "2", "--continue-at", "-", "-o", o)
	if err == nil {
		t.Fatal("expected error on direction change")
	}

	// from a specific number is OK
	_, err = exec(t, "scan", "--limit", "3", "--continue-at", strconv.Itoa(testdata.MaxItem-2), "-o", o)
	if err != nil {
		t.Fatal(err)
	}

	// continue-at with remainder
	_, err = exec(t, "scan", "--limit", strconv.Itoa(testdata.ItemCount), "--continue-at", "-", "-o", o)
	if err != nil {
		t.Fatal(err)
	}

	// continue-at repeated
	_, err = exec(t, "scan", "--limit", strconv.Itoa(testdata.ItemCount), "--continue-at", "-", "-o", o)
	if err != nil {
		t.Fatal(err)
	}

	// output file should contain expected results
	f, err := os.Open(o) //nolint:gosec // G304 intended
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	verifyFullScan(t, f, testdata.MaxItem, testdata.MinItem)
}

func TestInvalidUserArgs(t *testing.T) {
	// using --limit without --submitted must error
	_, err := exec(t, "user", testdata.UserID, "--limit", "5")
	if err == nil {
		t.Fatal("expected error when --limit is used without --submitted")
	}
}

func TestUnknownCommand(t *testing.T) {
	// unknown sub‑commands should return an error
	_, err := exec(t, "foobar")
	if err == nil {
		t.Fatal("expected error for unknown command")
	}

	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestScanTruncateOutput(t *testing.T) {
	// scan without --continue-at should truncate the output file
	out := filepath.Join(t.TempDir(), "scan.json")
	// first write two items
	_, err := exec(t, "scan", "--limit", "2", "-o", out)
	if err != nil {
		t.Fatal(err)
	}
	// then write only one; file must be truncated to one line
	_, err = exec(t, "scan", "--limit", "1", "-o", out)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out) //nolint:gosec // G304 intended
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	lines := 0

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines++
	}

	err = scanner.Err()
	if err != nil {
		t.Fatal(err)
	}

	if lines != 1 {
		t.Fatalf("expected 1 line after truncation, got %d", lines)
	}
}

func TestScanContinueAsc(t *testing.T) {
	o := filepath.Join(t.TempDir(), "test.json")

	// continue-at - from not exist is OK
	_, err := exec(t, "scan", "--asc", "-l2", "--continue-at", strconv.Itoa(testdata.MinItem), "-o", o)
	if err != nil {
		t.Fatal(err)
	}

	// continue-at with direction change fails
	_, err = exec(t, "scan", "--limit", strconv.Itoa(testdata.ItemCount), "--continue-at", "-", "-o", o)
	if err == nil {
		t.Fatal("expected error on direction change")
	}

	// continue-at repeated
	_, err = exec(t, "scan", "--asc", "--limit", strconv.Itoa(testdata.ItemCount), "--continue-at", "-", "-o", o)
	if err != nil {
		t.Fatal(err)
	}

	// output file should contain expected results
	f, err := os.Open(o) //nolint:gosec // G304 intended
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	verifyFullScan(t, f, testdata.MinItem, testdata.MaxItem)

	_, err = f.Seek(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	buf, err := io.ReadAll(f)

	if !bytes.Equal(buf, testdata.ItemsRaw) {
		t.Fatalf("scan bytes differed")
	}
}

func verifyFullScan(t *testing.T, r io.Reader, first int, last int) {
	t.Helper()

	lineCount := 0

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		switch lineCount {
		case 0:
			if !strings.Contains(scanner.Text(), strconv.Itoa(first)) {
				t.Fatalf("wrong first item: %s", scanner.Text())
			}
		case testdata.ItemCount:
			if !strings.Contains(scanner.Text(), strconv.Itoa(last)) {
				t.Fatalf("wrong last item: %s", scanner.Text())
			}
		}

		lineCount++
	}

	if lineCount != testdata.ItemCount {
		t.Fatalf("scan returned %d lines, expected %d", lineCount, testdata.ItemCount)
	}
}
