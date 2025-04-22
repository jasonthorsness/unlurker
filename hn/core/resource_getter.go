package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
)

type ResourceGetter struct {
	getter Getter[string, io.ReadCloser]
	cache  *MapCache[string, any]
}

func NewResourceGetter(getter Getter[string, io.ReadCloser], cache *MapCache[string, any]) *ResourceGetter {
	return &ResourceGetter{getter, cache}
}

// ErrTypeNotAllowed is returned when the result type is not supported.
var ErrTypeNotAllowed = errors.New("type not allowed")

func (r *ResourceGetter) Get(ctx context.Context, path string, result any) error {
	ok, err := r.getResourceFromCache(path, result)
	if err != nil {
		return err
	}

	if ok {
		return nil
	}

	reader, err := r.getter.Get(ctx, path)
	if err != nil {
		return fmt.Errorf("getter get failed: %w", err)
	}

	err = getResourceFromReader(reader, result)
	if err != nil {
		return err
	}

	err = r.putResourceToCache(path, result)
	if err != nil {
		return err
	}

	return nil
}

func (r *ResourceGetter) getResourceFromCache(path string, value any) (bool, error) {
	found, _ := r.cache.Get([]string{path})
	if len(found) == 0 {
		return false, nil
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Ptr {
		return true, fmt.Errorf("get value must be a pointer: %w", ErrTypeNotAllowed)
	}

	vVal := reflect.ValueOf(found[0].Value)
	if rv.Elem().Type() != vVal.Type() {
		return true, fmt.Errorf(
			"type mismatch: expected pointer to %v, got pointer to %v: %w",
			vVal.Type(),
			rv.Elem().Type(),
			ErrTypeNotAllowed)
	}

	rv.Elem().Set(vVal)

	return true, nil
}

func (r *ResourceGetter) putResourceToCache(path string, value any) error {
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Ptr {
		return fmt.Errorf("put value must be a pointer: %w", ErrTypeNotAllowed)
	}

	r.cache.Put(path, rv.Elem().Interface())

	return nil
}

func getResourceFromReader(reader io.ReadCloser, result any) (err error) {
	defer func(reader io.ReadCloser) {
		err = errors.Join(err, reader.Close())
	}(reader)

	decoder := json.NewDecoder(reader)

	err = decoder.Decode(&result)
	if err != nil {
		return fmt.Errorf("failed to decode: %w", err)
	}

	return nil
}
