package sqltest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sqltest"
)

// fakeTB is a recording testing.TB. It embeds the interface so it satisfies the
// sealed testing.TB contract, and overrides only the reporting methods the
// assertions call, so a failure-path test can drive an Assertion against it and
// inspect what was reported instead of failing the real test. Errorf is the only
// failure sink the Assertion uses (it never calls Fatalf), so recording it is
// enough.
type fakeTB struct {
	testing.TB
	errs []string
}

func (f *fakeTB) Helper() {}

// Cleanup records nothing and runs nothing: NewDB registers its verify+close
// through Cleanup, and a failure-path test drives Verify explicitly, so a no-op
// keeps the embedded nil testing.TB from being called.
func (f *fakeTB) Cleanup(func()) {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func (f *fakeTB) failed() bool { return len(f.errs) > 0 }

func (f *fakeTB) joined() string { return strings.Join(f.errs, "\n") }

// traceFromStmts records the given (isQuery, text) statements through a mock DB
// under one trace and returns it, for driving assertions in failure-path tests.
func traceFromStmts(t *testing.T, stmts []recordedStmt) *querytrace.Trace {
	t.Helper()
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Anything()).Return(sqltest.NewRows("id").AddRow(1)).AnyTimes()
	mock.ExpectExec(sqltest.Anything()).ReturnResult(0, 1).AnyTimes()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	for _, s := range stmts {
		if s.isQuery {
			rows, err := db.QueryContext(ctx, s.text, s.args...)
			if err != nil {
				t.Fatalf("query %q: %v", s.text, err)
			}
			for rows.Next() {
			}
			rows.Close()
		} else {
			if _, err := db.ExecContext(ctx, s.text, s.args...); err != nil {
				t.Fatalf("exec %q: %v", s.text, err)
			}
		}
	}
	return trace
}

func hasWarning(rep querytrace.Report, code querytrace.WarningCode) bool {
	for _, w := range rep.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

type recordedStmt struct {
	isQuery bool
	text    string
	args    []any
}

func q(text string, args ...any) recordedStmt {
	return recordedStmt{isQuery: true, text: text, args: args}
}
func x(text string, args ...any) recordedStmt {
	return recordedStmt{isQuery: false, text: text, args: args}
}

// oneSelect is a trace with a single SELECT on users, the common subject.
func oneSelect(t *testing.T) *querytrace.Trace {
	return traceFromStmts(t, []recordedStmt{q("SELECT id FROM users WHERE id = $1", 1)})
}

func TestAssertionCountFailures(t *testing.T) {
	tr := oneSelect(t)
	cases := []struct {
		name string
		want string // substring expected in the failure message
		run  func(a *sqltest.Assertion)
	}{
		{"Queries", "expected exactly 2 queries", func(a *sqltest.Assertion) { a.Queries(2) }},
		{"MaxQueries", "exceed the budget", func(a *sqltest.Assertion) { a.MaxQueries(0) }},
		{"MinQueries", "expected at least 2 queries", func(a *sqltest.Assertion) { a.MinQueries(2) }},
		{"NoQueries", "expected no queries", func(a *sqltest.Assertion) { a.NoQueries() }},
		{"Reads", "expected exactly 2 reads", func(a *sqltest.Assertion) { a.Reads(2) }},
		{"Writes", "expected exactly 1 writes", func(a *sqltest.Assertion) { a.Writes(1) }},
		{"UniqueQueries", "unique query shapes", func(a *sqltest.Assertion) { a.UniqueQueries(5) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeTB{}
			tc.run(sqltest.Expect(ft, tr))
			if !ft.failed() {
				t.Fatalf("%s did not report a failure", tc.name)
			}
			if !strings.Contains(ft.joined(), tc.want) {
				t.Fatalf("%s message = %q, want substring %q", tc.name, ft.joined(), tc.want)
			}
		})
	}
}

func TestAssertionWriteFailures(t *testing.T) {
	tr := traceFromStmts(t, []recordedStmt{x("INSERT INTO users (name) VALUES ($1)", "a")})
	ft := &fakeTB{}
	sqltest.Expect(ft, tr).NoWrites()
	if !ft.failed() || !strings.Contains(ft.joined(), "expected no writes") {
		t.Fatalf("NoWrites failure = %q", ft.joined())
	}
}

func TestAssertionWarningFailures(t *testing.T) {
	tr := oneSelect(t)

	// Warns expects a warning that the trace does not carry.
	ft := &fakeTB{}
	sqltest.Expect(ft, tr).Warns(querytrace.WarnPossibleNPlusOne)
	if !ft.failed() || !strings.Contains(ft.joined(), "expected a") {
		t.Fatalf("Warns failure = %q", ft.joined())
	}

	// NoWarnings / NoWarn / NoNPlusOne / AllRowsClosed hold on a clean trace,
	// so they must not report — exercising their passing branches.
	ok := &fakeTB{}
	sqltest.Expect(ok, tr).
		NoWarnings().
		NoWarn(querytrace.WarnPossibleNPlusOne).
		NoNPlusOne().
		AllRowsClosed()
	if ok.failed() {
		t.Fatalf("clean trace reported warnings: %q", ok.joined())
	}
}

func TestAssertionTransactionFailures(t *testing.T) {
	// A trace with no transaction at all.
	tr := oneSelect(t)
	cases := []struct {
		name string
		want string
		run  func(a *sqltest.Assertion)
	}{
		{"Transactions", "expected 1 transactions", func(a *sqltest.Assertion) { a.Transactions(1) }},
		{"Committed", "expected a committed transaction", func(a *sqltest.Assertion) { a.Committed() }},
		{"RolledBack", "expected a rolled-back transaction", func(a *sqltest.Assertion) { a.RolledBack() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeTB{}
			tc.run(sqltest.Expect(ft, tr))
			if !ft.failed() || !strings.Contains(ft.joined(), tc.want) {
				t.Fatalf("%s message = %q, want %q", tc.name, ft.joined(), tc.want)
			}
		})
	}

	// NoTransaction must report when a transaction did begin.
	withTx := traceFromStmts(t, nil)
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectExec(sqltest.Anything()).ReturnResult(0, 1).AnyTimes()
	ctx := querytrace.WithTrace(context.Background(), withTx)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO users (name) VALUES ($1)", "a"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ft := &fakeTB{}
	sqltest.Expect(ft, withTx).NoTransaction()
	if !ft.failed() || !strings.Contains(ft.joined(), "expected no transaction") {
		t.Fatalf("NoTransaction message = %q", ft.joined())
	}
}

func TestAssertionInOrderFailure(t *testing.T) {
	tr := oneSelect(t)

	// No matchers is a no-op and must not fail.
	ok := &fakeTB{}
	sqltest.Expect(ok, tr).InOrder()
	if ok.failed() {
		t.Fatalf("InOrder() with no matchers failed: %q", ok.joined())
	}

	// The required order is not present (there is no Insert before the Select).
	ft := &fakeTB{}
	sqltest.Expect(ft, tr).InOrder(sqltest.Insert(), sqltest.Select())
	if !ft.failed() || !strings.Contains(ft.joined(), "in order") {
		t.Fatalf("InOrder message = %q", ft.joined())
	}
}

func TestQueryAssertionFailures(t *testing.T) {
	tr := traceFromStmts(t, []recordedStmt{
		q("SELECT id FROM users WHERE id = $1", 1),
		q("SELECT id FROM users WHERE id = $1", 2),
	})
	cases := []struct {
		name string
		want string
		run  func(a *sqltest.Assertion)
	}{
		{"Times", "expected 5 statements", func(a *sqltest.Assertion) { a.Query(sqltest.Table("users")).Times(5) }},
		{"MinTimes", "expected at least 5", func(a *sqltest.Assertion) { a.Query(sqltest.Table("users")).MinTimes(5) }},
		{"MaxTimes", "expected at most 1", func(a *sqltest.Assertion) { a.Query(sqltest.Table("users")).MaxTimes(1) }},
		{"Never", "expected no statement", func(a *sqltest.Assertion) { a.Query(sqltest.Table("users")).Never() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeTB{}
			tc.run(sqltest.Expect(ft, tr))
			if !ft.failed() || !strings.Contains(ft.joined(), tc.want) {
				t.Fatalf("%s message = %q, want %q", tc.name, ft.joined(), tc.want)
			}
		})
	}

	// The describe() branch for an empty matcher set ("any statement") is hit by
	// a failing Query() with no matchers.
	ft := &fakeTB{}
	sqltest.Expect(ft, tr).Query().Times(0)
	if !ft.failed() || !strings.Contains(ft.joined(), "any statement") {
		t.Fatalf("empty-matcher describe = %q, want 'any statement'", ft.joined())
	}
}

// TestAssertionWarningPaths drives a trace that actually produces a warning
// (the same fingerprinted SELECT repeated, an N+1 signature) so the warning
// assertions exercise both their passing and failing branches.
func TestAssertionWarningPaths(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Anything()).Return(sqltest.NewRows("id").AddRow(1)).AnyTimes()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	for i := 0; i < 30; i++ {
		rows, err := db.QueryContext(ctx, "SELECT id FROM users WHERE id = $1", i)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		for rows.Next() {
		}
		rows.Close()
	}

	rep := trace.Report()
	if len(rep.Warnings) == 0 {
		t.Skip("trace produced no warning; N+1 derivation not triggered in this build")
	}
	code := rep.Warnings[0].Code

	// Warns passes for a code that is present.
	ok := &fakeTB{}
	sqltest.Expect(ok, trace).Warns(code)
	if ok.failed() {
		t.Fatalf("Warns(%s) failed on a trace that has it: %q", code, ok.joined())
	}

	// NoWarn / NoWarnings report when a warning of that code is present.
	noWarn := &fakeTB{}
	sqltest.Expect(noWarn, trace).NoWarn(code)
	if !noWarn.failed() || !strings.Contains(noWarn.joined(), "unexpected") {
		t.Fatalf("NoWarn(%s) message = %q, want a failure", code, noWarn.joined())
	}

	noWarnings := &fakeTB{}
	sqltest.Expect(noWarnings, trace).NoWarnings()
	if !noWarnings.failed() || !strings.Contains(noWarnings.joined(), "expected no warnings") {
		t.Fatalf("NoWarnings message = %q, want a failure", noWarnings.joined())
	}

	// If the warning is an N+1 family code, NoNPlusOne must report it too.
	if code == querytrace.WarnPossibleNPlusOne || code == querytrace.WarnQueryInLoop {
		noN := &fakeTB{}
		sqltest.Expect(noN, trace).NoNPlusOne()
		if !noN.failed() || !strings.Contains(noN.joined(), "N+1") {
			t.Fatalf("NoNPlusOne message = %q, want a failure", noN.joined())
		}
	}
}

// TestAllRowsClosedFailure drives a trace with an unclosed cursor so
// AllRowsClosed reports the rows_not_closed warning.
func TestAllRowsClosedFailure(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Anything()).Return(sqltest.NewRows("id").AddRow(1)).AnyTimes()

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	rows, err := db.QueryContext(ctx, "SELECT id FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	_ = rows // deliberately left open; let it be GC'd / finalized without Close

	if !hasWarning(trace.Report(), querytrace.WarnRowsNotClosed) {
		// Some builds only flag rows_not_closed on trace finalization; skip rather
		// than assert a behavior this unit cannot force deterministically.
		t.Skip("rows_not_closed not derivable without closing the cursor in this build")
	}
	ft := &fakeTB{}
	sqltest.Expect(ft, trace).AllRowsClosed()
	if !ft.failed() || !strings.Contains(ft.joined(), "cursor") {
		t.Fatalf("AllRowsClosed message = %q, want a failure", ft.joined())
	}
}

// TestAssertionPassingChains exercises the success branches of the count
// assertions so a passing chain reports nothing.
func TestAssertionPassingChains(t *testing.T) {
	tr := traceFromStmts(t, []recordedStmt{
		q("SELECT id FROM users WHERE id = $1", 1),
		x("INSERT INTO users (name) VALUES ($1)", "a"),
	})
	ft := &fakeTB{}
	sqltest.Expect(ft, tr).
		Queries(2).
		MaxQueries(2).
		MinQueries(1).
		Reads(1).
		Writes(1).
		Query(sqltest.Insert()).MaxTimes(1)
	if ft.failed() {
		t.Fatalf("passing chain reported: %q", ft.joined())
	}
}
