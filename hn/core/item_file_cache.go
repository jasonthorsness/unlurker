package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DefaultStaleIf marks stale at 60 seconds after creation, then frequently for the first few days after an item is
// created, then quickly tapers after the first week to never again mark stale items more than a few weeks old.
const DefaultStaleIf = "(:now-refreshed)>" +
	"(60.0*(log2(max(0.0,((:now-Time)/60.0))+1.0)+pow(((:now-Time)/(24.0*60.0*60.0)),3)))"

type ItemFileCache struct {
	db      *sql.DB
	clock   Clock
	staleIf string
}

func NewItemFileCache(
	ctx context.Context,
	clock Clock,
	path string,
	staleIf string,
) (_ *ItemFileCache, err error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	defer func() {
		if err != nil {
			err = errors.Join(err, db.Close())
		}
	}()

	if staleIf == "" {
		staleIf = DefaultStaleIf
	}

	c := &ItemFileCache{db, clock, staleIf}

	err = c.execContext(ctx, "PRAGMA journal_mode = WAL")
	if err != nil {
		return nil, err
	}

	err = c.execContext(ctx, "PRAGMA synchronous = NORMAL")
	if err != nil {
		return nil, err
	}

	err = c.execContext(ctx, `
		CREATE TABLE IF NOT EXISTS item(
		  ID INTEGER PRIMARY KEY,
		  refreshed INTEGER NOT NULL,
		  Time INTEGER NOT NULL,
		  value BLOB NOT NULL
    )`)
	if err != nil {
		return nil, err
	}

	err = c.execContext(
		ctx,
		"EXPLAIN SELECT ID, refreshed, Time, value FROM item WHERE "+staleIf,
		sql.Named("now", clock.Now().Unix()))
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *ItemFileCache) Get(ctx context.Context, ids []int, do func(id int, reader io.ReadCloser)) ([]int, error) {
	did := make([]bool, len(ids))
	err := c.get(ctx, ids, did, do)
	remaining := make([]int, 0, len(ids))

	for i, v := range did {
		if !v {
			remaining = append(remaining, ids[i])
		}
	}

	return remaining, err
}

func (c *ItemFileCache) get(ctx context.Context, ids []int, did []bool, do func(id int, reader io.ReadCloser)) error {
	params := make([]interface{}, 0, len(ids))
	indices := make(map[int][]int, len(ids))

	for i, id := range ids {
		indices[id] = append(indices[id], i)
		params = append(params, id)
	}

	if len(params) == 0 {
		return nil
	}

	query := "SELECT ID, value FROM item WHERE ID IN (?" +
		strings.Repeat(",?", len(params)-1) +
		") AND NOT (" + c.staleIf + ")"

	params = append(params, sql.Named("now", c.clock.Now().Unix()))

	rows, err := c.queryContext(ctx, query, params...)
	if err != nil {
		return err
	}

	return getRows(rows, indices, did, do)
}

func getRows(rows *sql.Rows, indices map[int][]int, did []bool, do func(id int, reader io.ReadCloser)) (err error) {
	defer func(rows *sql.Rows) { err = errors.Join(err, rows.Close()) }(rows)

	for rows.Next() {
		var id int
		var data sql.RawBytes

		err = rows.Scan(&id, &data)
		if err != nil {
			return fmt.Errorf("file cache get scan: %w", err)
		}

		ixx, ok := indices[id]
		if !ok {
			return fmt.Errorf("received ID not requested: %w", errUnexpectedResultFromDatabase)
		}

		for _, ix := range ixx {
			did[ix] = true

			do(id, io.NopCloser(bytes.NewReader(data)))
		}
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("file cache get rows err: %w", err)
	}

	return nil
}

var errUnexpectedResultFromDatabase = errors.New("unexpected result from database")

func (c *ItemFileCache) Close() error {
	err := c.db.Close()
	if err != nil {
		return fmt.Errorf("failed to close db: %w", err)
	}

	return nil
}

type ItemCacheEntry interface {
	Bytes() []byte
}

const numPutParams = 4

func (c *ItemFileCache) Put(ctx context.Context, items [][]byte) error {
	if len(items) == 0 {
		return nil
	}

	params := make([]interface{}, 0, len(items)*numPutParams)

	for _, e := range items {
		if bytes.Equal(e, []byte("null")) {
			// null body ignored
			continue
		}

		var result struct {
			ID   int   `json:"id"`
			Time int64 `json:"time"`
		}

		err := json.Unmarshal(e, &result)
		if err != nil {
			return fmt.Errorf("failed to unmarshal item: %w", err)
		}

		params = append(params, result.ID, c.clock.Now().Unix(), result.Time, e)
	}

	if len(params) == 0 {
		return nil
	}

	query := c.putQuery(params)

	err := c.execContext(ctx, query, params...)
	if err != nil {
		return err
	}

	return nil
}

func (c *ItemFileCache) putQuery(params []interface{}) string {
	var sb strings.Builder

	sb.WriteString("INSERT OR REPLACE INTO item (ID,refreshed,Time,value) VALUES ")
	sb.WriteString("(?,?,?,?)")

	for range (len(params) / numPutParams) - 1 {
		sb.WriteString(",(?,?,?,?)")
	}

	query := sb.String()

	return query
}

func (c *ItemFileCache) execContext(ctx context.Context, query string, args ...any) error {
	_, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("exec failed: %s %w", query, err)
	}

	return nil
}

func (c *ItemFileCache) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %s %w", query, err)
	}

	return rows, nil
}
