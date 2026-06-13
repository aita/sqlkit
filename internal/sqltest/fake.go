// Package sqltest provides a scriptable database/sql driver for testing
// sqlkit without a live database.
package sqltest

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
)

// Call records one statement sent to the fake database.
type Call struct {
	SQL  string
	Args []any
}

// Result is a canned query result.
type Result struct {
	Columns []string
	Rows    [][]driver.Value
}

// Fake is a scriptable in-memory database driver. Set QueryFunc/ExecFunc to
// control responses; all statements are recorded.
type Fake struct {
	mu       sync.Mutex
	queries  []Call
	execs    []Call
	all      []Call
	begins   int
	commits  int
	rowReads int

	// QueryFunc serves QueryContext calls. Nil means every query returns an
	// empty result set.
	QueryFunc func(sql string, args []any) (Result, error)
	// ExecFunc serves ExecContext calls. Nil means success.
	ExecFunc func(sql string, args []any) error
	// LastInsertID is reported by the Result of every ExecContext call, so the
	// RETURNING-less write-back path (MySQL-style LastInsertId) is testable.
	LastInsertID int64
}

// DB opens a database/sql handle backed by this fake.
func (f *Fake) DB() *stdsql.DB { return stdsql.OpenDB(connector{f}) }

// Queries returns the recorded query statements.
func (f *Fake) Queries() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.queries...)
}

// Execs returns the recorded exec statements.
func (f *Fake) Execs() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.execs...)
}

// Statements returns all recorded statements in order of execution.
func (f *Fake) Statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, call := range f.all {
		out = append(out, call.SQL)
	}
	return out
}

// RowReads returns the number of rows handed to the caller across all
// queries, which observes how far result sets were actually read.
func (f *Fake) RowReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rowReads
}

// Begins returns the number of transactions begun.
func (f *Fake) Begins() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.begins
}

// Commits returns the number of committed transactions.
func (f *Fake) Commits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commits
}

func (f *Fake) recordQuery(call Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, call)
	f.all = append(f.all, call)
}

func (f *Fake) recordExec(call Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, call)
	f.all = append(f.all, call)
}

type connector struct{ f *Fake }

func (c connector) Connect(context.Context) (driver.Conn, error) { return &conn{f: c.f}, nil }
func (c connector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, fmt.Errorf("sqltest: use Fake.DB()")
}

type conn struct{ f *Fake }

func (c *conn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("sqltest: prepared statements are not supported")
}

func (c *conn) Close() error { return nil }

func (c *conn) Begin() (driver.Tx, error) { return c.beginTx() }

func (c *conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.beginTx()
}

func (c *conn) beginTx() (driver.Tx, error) {
	c.f.mu.Lock()
	c.f.begins++
	c.f.mu.Unlock()
	return tx{f: c.f}, nil
}

func (c *conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	values := namedToAny(args)
	c.f.recordQuery(Call{SQL: query, Args: values})
	if c.f.QueryFunc == nil {
		return &rows{f: c.f}, nil
	}
	result, err := c.f.QueryFunc(query, values)
	if err != nil {
		return nil, err
	}
	return &rows{f: c.f, columns: result.Columns, data: result.Rows}, nil
}

func (c *conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	values := namedToAny(args)
	c.f.recordExec(Call{SQL: query, Args: values})
	if c.f.ExecFunc != nil {
		if err := c.f.ExecFunc(query, values); err != nil {
			return nil, err
		}
	}
	return execResult{lastInsertID: c.f.LastInsertID, rowsAffected: 1}, nil
}

// execResult is a driver.Result that reports a configurable LastInsertId.
type execResult struct {
	lastInsertID int64
	rowsAffected int64
}

func (r execResult) LastInsertId() (int64, error) { return r.lastInsertID, nil }
func (r execResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

func namedToAny(args []driver.NamedValue) []any {
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return values
}

type tx struct{ f *Fake }

func (t tx) Commit() error {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	t.f.commits++
	return nil
}

func (t tx) Rollback() error { return nil }

type rows struct {
	f       *Fake
	columns []string
	data    [][]driver.Value
	pos     int
}

func (r *rows) Columns() []string { return r.columns }
func (r *rows) Close() error      { return nil }

func (r *rows) Next(dest []driver.Value) error {
	if r.pos >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.pos])
	r.pos++
	if r.f != nil {
		r.f.mu.Lock()
		r.f.rowReads++
		r.f.mu.Unlock()
	}
	return nil
}
