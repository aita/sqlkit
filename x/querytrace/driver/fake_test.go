package driver_test

import (
	"context"
	stddriver "database/sql/driver"
	"fmt"
	"io"
	"strings"

	"github.com/aita/sqlkit/sql"
)

// fakeConnector is an in-memory database/sql driver used to exercise the wrapper
// without a real database. Queries return rowCount rows of a single int column;
// execs report rowCount rows affected.
type fakeConnector struct {
	rowCount int
}

func (c fakeConnector) Connect(context.Context) (stddriver.Conn, error) {
	return &fakeConn{rowCount: c.rowCount}, nil
}
func (c fakeConnector) Driver() stddriver.Driver { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (stddriver.Conn, error) { return &fakeConn{}, nil }

type fakeConn struct {
	rowCount int
}

var (
	_ stddriver.QueryerContext     = (*fakeConn)(nil)
	_ stddriver.ExecerContext      = (*fakeConn)(nil)
	_ stddriver.ConnPrepareContext = (*fakeConn)(nil)
	_ stddriver.ConnBeginTx        = (*fakeConn)(nil)
)

func (c *fakeConn) QueryContext(_ context.Context, _ string, _ []stddriver.NamedValue) (stddriver.Rows, error) {
	return &fakeRows{remaining: c.rowCount}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, _ string, _ []stddriver.NamedValue) (stddriver.Result, error) {
	return fakeResult{rows: int64(c.rowCount)}, nil
}

func (c *fakeConn) PrepareContext(_ context.Context, _ string) (stddriver.Stmt, error) {
	return &fakeStmt{rowCount: c.rowCount}, nil
}
func (c *fakeConn) Prepare(string) (stddriver.Stmt, error) {
	return &fakeStmt{rowCount: c.rowCount}, nil
}
func (c *fakeConn) Close() error                 { return nil }
func (c *fakeConn) Begin() (stddriver.Tx, error) { return fakeTx{}, nil }
func (c *fakeConn) BeginTx(context.Context, stddriver.TxOptions) (stddriver.Tx, error) {
	return fakeTx{}, nil
}

type fakeStmt struct {
	rowCount int
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec([]stddriver.Value) (stddriver.Result, error) {
	return fakeResult{rows: int64(s.rowCount)}, nil
}
func (s *fakeStmt) Query([]stddriver.Value) (stddriver.Rows, error) {
	return &fakeRows{remaining: s.rowCount}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeResult struct {
	rows int64
}

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rows, nil }

type fakeRows struct {
	remaining int
	n         int64
}

func (r *fakeRows) Columns() []string { return []string{"id"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []stddriver.Value) error {
	if r.remaining <= 0 {
		return io.EOF
	}
	r.remaining--
	r.n++
	dest[0] = r.n
	return nil
}

// fakeParser is a tiny stand-in for a real sqlparse frontend (such as
// x/sqlparse/postgres): it recognizes the four DML shapes the tests use and
// builds the corresponding sql.Query so the analyzer's parse → Describe → ToSQL
// path runs exactly as it would with a real parser, without a cgo dependency.
type fakeParser struct{}

func (fakeParser) Parse(text string) ([]sql.Query, error) {
	s := strings.TrimSpace(text)
	up := strings.ToUpper(s)
	hasWhere := strings.Contains(up, " WHERE ")
	switch {
	case strings.HasPrefix(up, "SELECT"):
		q := sql.Select(sql.Col("*")).From(sql.Tbl(tableAfter(up, s, "FROM")))
		if hasWhere {
			q = q.Where(sql.Eq(sql.Col("id"), sql.Expr(1)))
		}
		return []sql.Query{q}, nil
	case strings.HasPrefix(up, "UPDATE"):
		q := sql.Update(sql.Table{Name: tableAfter(up, s, "UPDATE")}).Set(sql.Set("name", sql.Expr("x")))
		if hasWhere {
			q = q.Where(sql.Eq(sql.Col("id"), sql.Expr(1)))
		}
		return []sql.Query{q}, nil
	case strings.HasPrefix(up, "DELETE"):
		q := sql.DeleteFrom(sql.Table{Name: tableAfter(up, s, "FROM")})
		if hasWhere {
			q = q.Where(sql.Eq(sql.Col("id"), sql.Expr(1)))
		}
		return []sql.Query{q}, nil
	case strings.HasPrefix(up, "INSERT"):
		q := sql.InsertInto(sql.Table{Name: tableAfter(up, s, "INTO")}).Columns("name").Values("x")
		return []sql.Query{q}, nil
	}
	return nil, fmt.Errorf("fakeParser: cannot parse %q", text)
}

// tableAfter returns the token following keyword kw. up is the upper-cased form
// of s, used only to locate kw at the same byte offset (ASCII keywords keep
// length under ToUpper).
func tableAfter(up, s, kw string) string {
	i := strings.Index(up, kw)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(s[i+len(kw):])
	j := strings.IndexAny(rest, " (\t\n")
	if j < 0 {
		return rest
	}
	return rest[:j]
}
