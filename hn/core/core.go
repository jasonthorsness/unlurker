// Package core package implements a retrieval pipeline for the HN API
package core

import (
	"bytes"
	"io"
	"sync"
	"time"
)

const (
	itemPathPrefix = "item/"
	jsonSuffix     = ".json"
)

type Clock interface {
	Now() time.Time
}

type readCloserWithError struct {
	err error
}

func (r *readCloserWithError) Read(_ []byte) (int, error) {
	return 0, r.err
}

func (r *readCloserWithError) Close() error {
	return nil
}

func WrapErrorInReadCloser(err error) (io.ReadCloser, error) {
	return &readCloserWithError{err}, nil
}

type readCloserWithPooledBuffer struct {
	pool  *sync.Pool
	inner *bytes.Buffer
}

func NewReadCloserWithPooledBuffer(pool *sync.Pool, inner *bytes.Buffer) io.ReadCloser {
	return &readCloserWithPooledBuffer{pool, inner}
}

func (r *readCloserWithPooledBuffer) WriteTo(w io.Writer) (int64, error) {
	return r.inner.WriteTo(w)
}

func (r *readCloserWithPooledBuffer) Read(p []byte) (int, error) {
	if r.inner == nil {
		return 0, io.EOF
	}

	return r.inner.Read(p)
}

func (r *readCloserWithPooledBuffer) Close() error {
	if r.inner != nil {
		inner := r.inner
		r.inner = nil
		r.pool.Put(inner)
	}

	return nil
}

func trySend[T any](ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	default:
		return false
	}
}
