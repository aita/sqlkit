package sqltest_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	querytrace "github.com/aita/sqlkit/querytrace"
	sqlpkg "github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqltest"
)

// debugConfig is a verbose NewConfig with a small parser wired in, so the
// structural matchers (Table, Fingerprint, HasTopLevelWhere) and raw-SQL matchers work
// without a real sqlparse frontend.
func debugConfig() querytrace.Config {
	return querytrace.NewConfig(querytrace.WithParser(fakeParser{}), querytrace.WithVerboseCapture())
}

func newDB(t *testing.T, opts ...sqltest.Option) (*sql.DB, *sqltest.Mock) {
	t.Helper()
	opts = append([]sqltest.Option{sqltest.WithConfig(debugConfig())}, opts...)
	// NewDB registers a t.Cleanup that verifies the mock and closes the DB.
	return sqltest.NewDB(t, opts...)
}

func TestMockReturnsRows(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery(sqltest.Contains("from users")).
		Return(sqltest.NewRows("id", "name").AddRow(1, "alice").AddRow(2, "bob")).
		Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT id, name FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var names []string
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	rows.Close()

	if got := strings.Join(names, ","); got != "alice,bob" {
		t.Fatalf("names = %q, want alice,bob", got)
	}

	sqltest.Expect(t, trace).
		Queries(1).
		Reads(1).
		NoWrites().
		NoWarnings().
		Query(sqltest.Select(), sqltest.Table("users"), sqltest.HasTopLevelWhere()).Times(1)
}

func TestMockReturnsResult(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectExec(sqltest.Contains("insert into audit_log")).ReturnResult(7, 1)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	res, err := db.ExecContext(ctx, "INSERT INTO audit_log (msg) VALUES ($1)", "hi")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if id, _ := res.LastInsertId(); id != 7 {
		t.Fatalf("last insert id = %d, want 7", id)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}

	sqltest.Expect(t, trace).
		Writes(1).
		Query(sqltest.Insert(), sqltest.Table("audit_log")).Times(1)
}

func TestMockReturnsError(t *testing.T) {
	db, mock := newDB(t)
	want := errors.New("boom")
	mock.ExpectQuery(sqltest.Anything()).ReturnError(want)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	if _, err := db.QueryContext(ctx, "SELECT 1"); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// TestUnstubbedIsLenient documents the fake-friendly default: a statement no
// stub answers gets empty rows, so a test need only program what it asserts on.
func TestUnstubbedIsLenient(t *testing.T) {
	db, _ := newDB(t, sqltest.Lenient())
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM widgets")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if rows.Next() {
		t.Fatal("expected no rows from an unstubbed query")
	}
	rows.Close()

	sqltest.Expect(t, trace).Query(sqltest.Table("widgets")).Times(1)
}

func TestTransactionTimeline(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery(sqltest.Contains("from users")).Return(sqltest.NewRows("id").AddRow(1))
	mock.ExpectExec(sqltest.Anything()).ReturnResult(0, 1)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT id FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO audit_log (msg) VALUES ($1)", "x"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	sqltest.Expect(t, trace).
		Queries(2).
		Transactions(1).
		Committed().
		InOrder(sqltest.Select(), sqltest.Insert(), sqltest.Commit())
}

func TestSpyCountAndEvents(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery(sqltest.Anything()).Return(sqltest.NewRows("id").AddRow(1))

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	for i := 0; i < 3; i++ {
		rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", i)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		rows.Close()
	}
	rows, err := db.QueryContext(ctx, "SELECT * FROM posts")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if n := sqltest.Count(trace, sqltest.Table("users")); n != 3 {
		t.Errorf("users count = %d, want 3", n)
	}
	if n := sqltest.Count(trace, sqltest.Select()); n != 4 {
		t.Errorf("select count = %d, want 4", n)
	}
	if evs := sqltest.Events(trace, sqltest.Table("posts")); len(evs) != 1 {
		t.Errorf("posts events = %d, want 1", len(evs))
	}

	sqltest.Expect(t, trace).
		Query(sqltest.Table("users")).MinTimes(3).
		Query(sqltest.Table("comments")).Never().
		UniqueQueries(2)
}

func TestExpectQueryBySQLQuery(t *testing.T) {
	db, mock := newDB(t)

	q := sqlpkg.Select(sqlpkg.Col("id"), sqlpkg.Col("name")).
		From(sqlpkg.Tbl("users")).
		Where(sqlpkg.Eq(sqlpkg.Col("id"), sqlpkg.Expr(1)))
	mock.ExpectQuery(q).
		Return(sqltest.NewRows("id", "name").AddRow(1, "alice")).
		Required()

	text, args, err := q.ToSQL(sqlpkg.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, text, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}

	sqltest.Expect(t, trace).Query(sqltest.Table("users")).Times(1)
}

func TestMaxTimes(t *testing.T) {
	db, mock := newDB(t, sqltest.Lenient())
	// The stub answers one statement, then later calls fall through to the
	// lenient default (empty rows).
	mock.ExpectQuery(sqltest.Contains("from users")).
		Return(sqltest.NewRows("id").AddRow(1)).
		MaxTimes(1)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	counts := []int{}
	for i := 0; i < 2; i++ {
		rows, err := db.QueryContext(ctx, "SELECT id FROM users WHERE id = $1", i)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		counts = append(counts, n)
	}
	if counts[0] != 1 || counts[1] != 0 {
		t.Fatalf("row counts = %v, want [1 0]", counts)
	}
}

func TestMatchers(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery(sqltest.Regexp(`(?i)FROM\s+users`)).
		Return(sqltest.NewRows("id").AddRow(1)).
		Required()
	mock.ExpectExec(sqltest.Equals("DELETE FROM users WHERE id = $1")).
		ReturnResult(0, 1).
		Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM   users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()
	// Equals ignores the extra whitespace and case.
	if _, err := db.ExecContext(ctx, "delete from users where id = $1", 1); err != nil {
		t.Fatalf("exec: %v", err)
	}

	sqltest.Expect(t, trace).Reads(1).Writes(1)
}

// TestDDLNotCountedAsRead pins that a schema statement (CREATE/ALTER/DROP) counts
// as neither a read nor a write: Reads must count read statements directly, not
// subtract writes from the total, or DDL leaks into the read count.
func TestDDLNotCountedAsRead(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectExec(sqltest.Contains("create table")).
		ReturnResult(0, 0).
		Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	if _, err := db.ExecContext(ctx, "CREATE TABLE users (id int)"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	sqltest.Expect(t, trace).
		Queries(1).
		Reads(0).
		NoWrites()
}

func rowCount(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int {
	t.Helper()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n
}

func TestArgMatchers(t *testing.T) {
	db, mock := newDB(t, sqltest.Lenient())
	pg := sqlpkg.PostgreSQLDialect{}

	// The expectation pins id to 5 with an Arg matcher.
	mock.ExpectQuery(
		sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("users")).
			Where(sqlpkg.Eq(sqlpkg.Col("id"), sqltest.Arg(5))),
	).Return(sqltest.NewRows("id").AddRow(1)).Required()

	// The real statement has the same shape; only its bind value varies.
	realText, _, err := sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("users")).
		Where(sqlpkg.Eq(sqlpkg.Col("id"), sqlpkg.Expr(0))).ToSQL(pg)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	if n := rowCount(t, ctx, db, realText, 5); n != 1 {
		t.Errorf("arg 5 rows = %d, want 1", n)
	}
	if n := rowCount(t, ctx, db, realText, 6); n != 0 {
		t.Errorf("arg 6 rows = %d, want 0 (Arg(5) should not match)", n)
	}
}

func TestArgMatchFunc(t *testing.T) {
	db, mock := newDB(t, sqltest.Lenient())
	pg := sqlpkg.PostgreSQLDialect{}

	positive := sqltest.ArgMatch(func(v any) bool {
		n, ok := v.(int64)
		return ok && n > 0
	})
	mock.ExpectQuery(
		sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("users")).
			Where(sqlpkg.Eq(sqlpkg.Col("id"), positive)),
	).Return(sqltest.NewRows("id").AddRow(1)).Required()

	realText, _, _ := sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("users")).
		Where(sqlpkg.Eq(sqlpkg.Col("id"), sqlpkg.Expr(0))).ToSQL(pg)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	if n := rowCount(t, ctx, db, realText, 3); n != 1 {
		t.Errorf("arg 3 rows = %d, want 1", n)
	}
	if n := rowCount(t, ctx, db, realText, -1); n != 0 {
		t.Errorf("arg -1 rows = %d, want 0", n)
	}
}

func TestStructuralWildcards(t *testing.T) {
	db, mock := newDB(t, sqltest.Lenient())
	pg := sqlpkg.PostgreSQLDialect{}

	// Any table, but the column is fixed to id.
	mock.ExpectQuery(
		sqlpkg.Select(sqlpkg.Col("id")).From(sqltest.AnyTable()),
	).Return(sqltest.NewRows("id").AddRow(1)).Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	usersText, _, _ := sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("users")).ToSQL(pg)
	acctText, _, _ := sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("accounts")).ToSQL(pg)
	nameText, _, _ := sqlpkg.Select(sqlpkg.Col("name")).From(sqlpkg.Tbl("users")).ToSQL(pg)

	if n := rowCount(t, ctx, db, usersText); n != 1 {
		t.Errorf("users rows = %d, want 1", n)
	}
	if n := rowCount(t, ctx, db, acctText); n != 1 {
		t.Errorf("accounts rows = %d, want 1 (AnyTable)", n)
	}
	if n := rowCount(t, ctx, db, nameText); n != 0 {
		t.Errorf("name rows = %d, want 0 (column id is fixed)", n)
	}
}

func TestAnyColumnAndExpr(t *testing.T) {
	db, mock := newDB(t, sqltest.Lenient())
	pg := sqlpkg.PostgreSQLDialect{}

	mock.ExpectQuery(
		sqlpkg.Select(sqltest.AnyColumn()).From(sqlpkg.Tbl("users")).Where(sqltest.AnyExpr()),
	).Return(sqltest.NewRows("x").AddRow(1)).Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	text, _, _ := sqlpkg.Select(sqlpkg.Col("email")).From(sqlpkg.Tbl("users")).
		Where(sqlpkg.Eq(sqlpkg.Col("status"), sqlpkg.Expr("active"))).ToSQL(pg)
	if n := rowCount(t, ctx, db, text, "active"); n != 1 {
		t.Errorf("rows = %d, want 1 (any column, any where)", n)
	}

	other, _, _ := sqlpkg.Select(sqlpkg.Col("email")).From(sqlpkg.Tbl("orders")).
		Where(sqlpkg.Eq(sqlpkg.Col("status"), sqlpkg.Expr("active"))).ToSQL(pg)
	if n := rowCount(t, ctx, db, other, "active"); n != 0 {
		t.Errorf("orders rows = %d, want 0 (table fixed to users)", n)
	}
}

func TestWithArgs(t *testing.T) {
	db, mock := newDB(t, sqltest.Lenient())
	mock.ExpectQuery(sqltest.Contains("from users")).
		WithArgs(sqltest.Arg(5)).
		Return(sqltest.NewRows("id").AddRow(1)).
		Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	const q = "SELECT id FROM users WHERE id = $1"
	if n := rowCount(t, ctx, db, q, 5); n != 1 {
		t.Errorf("arg 5 rows = %d, want 1", n)
	}
	if n := rowCount(t, ctx, db, q, 6); n != 0 {
		t.Errorf("arg 6 rows = %d, want 0 (WithArgs(5) should not match)", n)
	}
}

func TestTimes(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery(sqltest.Contains("from users")).
		Return(sqltest.NewRows("id").AddRow(1)).
		Times(2)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	for i := 0; i < 2; i++ {
		if n := rowCount(t, ctx, db, "SELECT id FROM users WHERE id = $1", i); n != 1 {
			t.Errorf("call %d rows = %d, want 1", i, n)
		}
	}
	// The cleanup Verify requires exactly two matches (Times(2)).
}

func TestAnyTimes(t *testing.T) {
	// An AnyTimes stub that never matches must not fail the cleanup Verify: it
	// imposes no floor, unlike Required.
	_, mock := newDB(t)
	mock.ExpectQuery(sqltest.Anything()).
		Return(sqltest.NewRows("id")).
		AnyTimes()
	_ = mock
}

func TestWillDelayFor(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery(sqltest.Anything()).
		WillDelayFor(50 * time.Millisecond).
		Return(sqltest.NewRows("id").AddRow(1))

	trace := querytrace.New("op")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	ctx = querytrace.WithTrace(ctx, trace)

	if _, err := db.QueryContext(ctx, "SELECT 1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestRowError(t *testing.T) {
	db, mock := newDB(t)
	boom := errors.New("read boom")
	mock.ExpectQuery(sqltest.Anything()).
		Return(sqltest.NewRows("id").AddRow(1).AddRow(2).RowError(1, boom))

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if n != 1 {
		t.Errorf("rows read = %d, want 1 before the error", n)
	}
	if err := rows.Err(); !errors.Is(err, boom) {
		t.Fatalf("rows.Err = %v, want read boom", err)
	}
}

// TestSentinelDoesNotCollide guards the wildcard sentinel: a real identifier
// that looks like the marker must be matched literally, never as a wildcard.
func TestSentinelDoesNotCollide(t *testing.T) {
	db, mock := newDB(t, sqltest.Lenient())
	pg := sqlpkg.PostgreSQLDialect{}

	mock.ExpectQuery(
		sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("qtwild_0")),
	).Return(sqltest.NewRows("id").AddRow(1)).Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	exact, _, _ := sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("qtwild_0")).ToSQL(pg)
	other, _, _ := sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("users")).ToSQL(pg)

	if n := rowCount(t, ctx, db, exact); n != 1 {
		t.Errorf("exact qtwild_0 rows = %d, want 1", n)
	}
	if n := rowCount(t, ctx, db, other); n != 0 {
		t.Errorf("users rows = %d, want 0 (a literal qtwild_0 must not act as a wildcard)", n)
	}
}

// --- fakeParser: a tiny stand-in so structural matchers work in tests ---

type fakeParser struct{}

func (fakeParser) Parse(text string) ([]sqlpkg.Query, error) {
	s := strings.TrimSpace(text)
	up := strings.ToUpper(s)
	hasWhere := strings.Contains(up, " WHERE ")
	switch {
	case strings.HasPrefix(up, "SELECT"):
		q := sqlpkg.Select(sqlpkg.Col("*")).From(sqlpkg.Tbl(tableAfter(up, s, "FROM")))
		if hasWhere {
			q = q.Where(sqlpkg.Eq(sqlpkg.Col("id"), sqlpkg.Expr(1)))
		}
		return []sqlpkg.Query{q}, nil
	case strings.HasPrefix(up, "UPDATE"):
		q := sqlpkg.Update(sqlpkg.Table{Name: sqlpkg.NewIdent(tableAfter(up, s, "UPDATE"))}).Set(sqlpkg.Set("name", sqlpkg.Expr("x")))
		if hasWhere {
			q = q.Where(sqlpkg.Eq(sqlpkg.Col("id"), sqlpkg.Expr(1)))
		}
		return []sqlpkg.Query{q}, nil
	case strings.HasPrefix(up, "DELETE"):
		q := sqlpkg.DeleteFrom(sqlpkg.Table{Name: sqlpkg.NewIdent(tableAfter(up, s, "FROM"))})
		if hasWhere {
			q = q.Where(sqlpkg.Eq(sqlpkg.Col("id"), sqlpkg.Expr(1)))
		}
		return []sqlpkg.Query{q}, nil
	case strings.HasPrefix(up, "INSERT"):
		q := sqlpkg.InsertInto(sqlpkg.Table{Name: sqlpkg.NewIdent(tableAfter(up, s, "INTO"))}).Columns("name").Values("x")
		return []sqlpkg.Query{q}, nil
	}
	return nil, fmt.Errorf("fakeParser: cannot parse %q", text)
}

func tableAfter(up, s, kw string) string {
	i := strings.Index(up, kw)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(s[i+len(kw):])
	j := strings.IndexAny(rest, " (\t\n")
	if j >= 0 {
		rest = rest[:j]
	}
	// Strip surrounding double quotes so quoted identifiers ("users") and bare
	// ones (users) extract the same table name.
	return strings.Trim(rest, `"`)
}
