package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type Getter[TKey any, TValue any] interface {
	// Get synchronously retrieves a single value using a key
	Get(ctx context.Context, key TKey) (TValue, error)
}

func NewBaseGetter(httpClient *http.Client, baseURL string) Getter[string, io.ReadCloser] {
	return &baseGetter{httpClient, baseURL}
}

type baseGetter struct {
	httpClient *http.Client
	baseURL    string
}

func (g *baseGetter) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	response, err := g.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	if response.StatusCode != http.StatusOK {
		return nil, &GetterError{path, response.StatusCode}
	}

	return response.Body, nil
}

func NewItemGetter(inner Getter[string, io.ReadCloser]) Getter[int, io.ReadCloser] {
	return &rekeyGetter[int, string, io.ReadCloser]{inner, func(id int) string {
		return itemPathPrefix + strconv.Itoa(id) + jsonSuffix
	}}
}

type rekeyGetter[TOuter any, TInner any, TValue any] struct {
	inner Getter[TInner, TValue]
	rekey func(TOuter) TInner
}

func (g *rekeyGetter[TOuter, TInner, TValue]) Get(ctx context.Context, key TOuter) (TValue, error) {
	return g.inner.Get(ctx, g.rekey(key))
}

type GetterError struct {
	Path string
	Code int
}

func (e *GetterError) Error() string {
	return strconv.Itoa(e.Code) + " " + e.Path
}
