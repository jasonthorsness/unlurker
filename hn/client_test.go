package hn

import (
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/goleak"
)

//nolint:paralleltest // goroutine checks can't be parallel
func TestNewClientClose_NoGoroutineLeaks(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	client, err := NewClient(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	err = client.Close()
	if err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
}
