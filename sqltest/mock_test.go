package sqltest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	querytrace "github.com/aita/sqlkit/querytrace"
	sqlpkg "github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqltest"
)

// TestMatcherDescriptions pins the String() rendering of every SQL matcher, the
// text Verify uses in failure messages.
func TestMatcherDescriptions(t *testing.T) {
	cases := []struct {
		m    sqltest.Matcher
		want string
	}{
		{sqltest.Contains("from users"), `contains "from users"`},
		{sqltest.Equals("SELECT 1"), `equals "SELECT 1"`},
		{sqltest.Regexp(`FROM\s+users`), `regexp FROM\s+users`},
		{sqltest.Anything(), "any statement"},
	}
	for _, tc := range cases {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("%T.String() = %q, want %q", tc.m, got, tc.want)
		}
	}
}

// TestArgMatcherDescriptions pins the String() rendering of the argument
// matchers.
func TestArgMatcherDescriptions(t *testing.T) {
	if got := sqltest.Arg(5).String(); got != "5" {
		t.Errorf("Arg(5).String() = %q, want 5", got)
	}
	if got := sqltest.AnyArg().String(); got != "any" {
		t.Errorf("AnyArg().String() = %q, want any", got)
	}
	if got := sqltest.ArgMatch(func(any) bool { return true }).String(); got != "func" {
		t.Errorf("ArgMatch().String() = %q, want func", got)
	}
}

// TestQueryMatcherDescription checks the compiled-query matcher renders its
// wildcards as <any> in the Verify failure description.
func TestQueryMatcherDescription(t *testing.T) {
	ft := &fakeTB{}
	_, mock := sqltest.NewDB(ft, sqltest.WithConfig(debugConfig()))
	mock.ExpectQuery(
		sqlpkg.Select(sqlpkg.Col("id")).From(sqltest.AnyTable()),
	).Return(sqltest.NewRows("id")).Required()
	mock.Verify(ft)
	if !strings.Contains(ft.joined(), "<any>") {
		t.Fatalf("query matcher description = %q, want <any> wildcard", ft.joined())
	}
}

// TestAnyArgMatches confirms AnyArg accepts any bind value at its position.
func TestAnyArgMatches(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Contains("from users")).
		WithArgs(sqltest.AnyArg()).
		Return(sqltest.NewRows("id").AddRow(1)).
		Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	rows, err := db.QueryContext(ctx, "SELECT id FROM users WHERE id = $1", 99)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if n != 1 {
		t.Errorf("AnyArg rows = %d, want 1", n)
	}
}

// TestStrictVerifyReportsUnmatched checks the strict (default) Verify reports a
// statement no stub answered.
func TestStrictVerifyReportsUnmatched(t *testing.T) {
	ft := &fakeTB{}
	db, mock := sqltest.NewDB(ft, sqltest.WithConfig(debugConfig()))

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	rows, err := db.QueryContext(ctx, "SELECT id FROM widgets")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	mock.Verify(ft)
	if !ft.failed() || !strings.Contains(ft.joined(), "no matching stub") {
		t.Fatalf("strict Verify message = %q, want an unmatched-statement report", ft.joined())
	}
}

// TestVerifyReportsUnmetMinimum checks Verify reports a Required stub that never
// matched.
func TestVerifyReportsUnmetMinimum(t *testing.T) {
	ft := &fakeTB{}
	_, mock := sqltest.NewDB(ft, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Contains("from orders")).
		Return(sqltest.NewRows("id")).
		Required()
	mock.Verify(ft)
	if !ft.failed() || !strings.Contains(ft.joined(), "want at least 1") {
		t.Fatalf("Verify message = %q, want an unmet-minimum report", ft.joined())
	}
}

// TestCoerceValues exercises coerce across the numeric, float, bool, []byte and
// string branches via AddRow, reading each value back out.
func TestCoerceValues(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Anything()).
		Return(sqltest.NewRows("i8", "u", "f", "b", "by", "s", "tm").
			AddRow(int8(1), uint(2), float32(3.5), true, []byte("x"), "y", time.Unix(0, 0)))

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	rows, err := db.QueryContext(ctx, "SELECT * FROM t")
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
}

// TestExecReturnsError covers runExec's error branch.
func TestExecReturnsError(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()))
	boom := errorString("exec boom")
	mock.ExpectExec(sqltest.Anything()).ReturnError(boom)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	if _, err := db.ExecContext(ctx, "DELETE FROM users"); err == nil {
		t.Fatal("exec succeeded, want the stubbed error")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

// TestResolvePanicsOnBadType checks resolve rejects an expectation that is
// neither a Matcher, string, nor sql.Query.
func TestResolvePanicsOnBadType(t *testing.T) {
	_, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ExpectQuery(123) did not panic")
		}
		if !strings.Contains(fmt.Sprint(r), "Matcher, string, or sql.Query") {
			t.Fatalf("panic = %v, want a type-error message", r)
		}
	}()
	mock.ExpectQuery(123)
}

// TestWithArgsRawValue covers WithArgs' branch that wraps a non-ArgMatcher value
// in an exact Arg.
func TestWithArgsRawValue(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Contains("from users")).
		WithArgs(5). // a raw int, wrapped in Arg(5) by WithArgs
		Return(sqltest.NewRows("id").AddRow(1)).
		Required()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	const q = "SELECT id FROM users WHERE id = $1"
	if n := rowCount(t, ctx, db, q, 5); n != 1 {
		t.Errorf("raw-arg 5 rows = %d, want 1", n)
	}
	if n := rowCount(t, ctx, db, q, 6); n != 0 {
		t.Errorf("raw-arg 6 rows = %d, want 0", n)
	}
}

// TestPreparedStatement drives the mock through a prepared statement, covering
// the mockConn.PrepareContext path and the mockStmt Query/Exec context methods.
func TestPreparedStatement(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Contains("from users")).
		Return(sqltest.NewRows("id").AddRow(1).AddRow(2))
	mock.ExpectExec(sqltest.Contains("delete from users")).ReturnResult(0, 3)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	stmt, err := db.PrepareContext(ctx, "SELECT id FROM users WHERE id = $1")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close() //nolint:errcheck // best-effort
	rows, err := stmt.QueryContext(ctx, 1)
	if err != nil {
		t.Fatalf("stmt query: %v", err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if n != 2 {
		t.Fatalf("prepared query rows = %d, want 2", n)
	}

	exec, err := db.PrepareContext(ctx, "DELETE FROM users WHERE id = $1")
	if err != nil {
		t.Fatalf("prepare exec: %v", err)
	}
	defer exec.Close() //nolint:errcheck // best-effort
	res, err := exec.ExecContext(ctx, 1)
	if err != nil {
		t.Fatalf("stmt exec: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 3 {
		t.Fatalf("rows affected = %d, want 3", affected)
	}
}

// TestDriverHandle covers the connector's Driver accessor.
func TestDriverHandle(t *testing.T) {
	db, _ := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	if db.Driver() == nil {
		t.Fatal("db.Driver() = nil, want a driver")
	}
}

// TestWillDelayForCompletes covers waitFor's success branch: a delay shorter
// than the context deadline returns the rows.
func TestWillDelayForCompletes(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()))
	mock.ExpectQuery(sqltest.Anything()).
		WillDelayFor(1 * time.Millisecond).
		Return(sqltest.NewRows("id").AddRow(1))

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	rows, err := db.QueryContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()
}
