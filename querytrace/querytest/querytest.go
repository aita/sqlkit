// Package querytest turns a querytrace.Trace into a test spy: it records the
// SQL one operation actually issued, and this package asserts on it after the
// fact. Where a mock library makes you declare the queries up front and fails
// if they do not run, querytest works the other way around — run the code under
// a Trace, then ask what it did: a query budget, no writes, no N+1, a SELECT on
// a given table that ran exactly once, an INSERT before the COMMIT.
//
// The entry point is Expect, a fluent assertion bound to a testing.TB:
//
//	trace := querytrace.New("GetUserPage")
//	ctx := querytrace.WithTrace(context.Background(), trace)
//	service.RenderUserPage(ctx, id)
//
//	querytest.Expect(t, trace).
//		MaxQueries(5).
//		NoWrites().
//		NoNPlusOne().
//		Query(querytest.Select(), querytest.Table("users")).Times(1)
//
// Match values (Select, Table, Fingerprint, SqlContains, Caller, HasWhere, …)
// select the statements an assertion applies to; pass several and they must all
// hold. All, Any, and Not compose them. Count and Events expose the same matching
// without a testing.TB, for inspecting a Trace directly.
package querytest

import (
	"fmt"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
)

// Assertion is a fluent set of checks against a single Trace. Each method
// reports a failure through the testing.TB and returns the Assertion so checks
// chain. Build one with Expect.
type Assertion struct {
	t     testing.TB
	trace *querytrace.Trace
	rep   querytrace.Report
	stmts []querytrace.Event
}

// Expect snapshots trace and returns an Assertion over it. Call it once the
// code under test has run; the snapshot is stable, so later checks see the same
// statements and warnings even if the Trace keeps recording.
func Expect(t testing.TB, trace *querytrace.Trace) *Assertion {
	t.Helper()
	rep := trace.Report()
	return &Assertion{
		t:     t,
		trace: trace,
		rep:   rep,
		stmts: statementEvents(rep.Events),
	}
}

// fail reports an assertion failure, appending the trace report for context.
func (a *Assertion) fail(format string, args ...any) {
	a.t.Helper()
	a.t.Errorf("querytest: %s\n%s", fmt.Sprintf(format, args...), a.rep.String())
}

func (a *Assertion) reads() int  { return a.rep.QueryCount - a.rep.WriteCount }
func (a *Assertion) writes() int { return a.rep.WriteCount }

// Queries asserts the trace recorded exactly n read and write statements.
func (a *Assertion) Queries(n int) *Assertion {
	a.t.Helper()
	if got := a.rep.QueryCount; got != n {
		a.fail("expected exactly %d queries, got %d", n, got)
	}
	return a
}

// MaxQueries asserts the trace recorded at most n statements, a query budget.
func (a *Assertion) MaxQueries(n int) *Assertion {
	a.t.Helper()
	if got := a.rep.QueryCount; got > n {
		a.fail("%d queries exceed the budget of %d", got, n)
	}
	return a
}

// MinQueries asserts the trace recorded at least n statements.
func (a *Assertion) MinQueries(n int) *Assertion {
	a.t.Helper()
	if got := a.rep.QueryCount; got < n {
		a.fail("expected at least %d queries, got %d", n, got)
	}
	return a
}

// NoQueries asserts the trace touched the database not at all, the check for a
// path that should be served without a query (a cache hit).
func (a *Assertion) NoQueries() *Assertion {
	a.t.Helper()
	if got := a.rep.QueryCount; got > 0 {
		a.fail("expected no queries, got %d", got)
	}
	return a
}

// Reads asserts the trace recorded exactly n read statements.
func (a *Assertion) Reads(n int) *Assertion {
	a.t.Helper()
	if got := a.reads(); got != n {
		a.fail("expected exactly %d reads, got %d", n, got)
	}
	return a
}

// Writes asserts the trace recorded exactly n write statements.
func (a *Assertion) Writes(n int) *Assertion {
	a.t.Helper()
	if got := a.writes(); got != n {
		a.fail("expected exactly %d writes, got %d", n, got)
	}
	return a
}

// NoWrites asserts the trace recorded no write statement, the check for a
// read-only code path.
func (a *Assertion) NoWrites() *Assertion {
	a.t.Helper()
	if got := a.writes(); got > 0 {
		a.fail("expected no writes, got %d", got)
	}
	return a
}

// UniqueQueries asserts the trace recorded exactly n distinct query shapes
// (fingerprints).
func (a *Assertion) UniqueQueries(n int) *Assertion {
	a.t.Helper()
	if got := a.rep.UniqueFingerprintCount; got != n {
		a.fail("expected %d unique query shapes, got %d", n, got)
	}
	return a
}

// Warns asserts the trace produced a warning with code.
func (a *Assertion) Warns(code querytrace.WarningCode) *Assertion {
	a.t.Helper()
	if !a.hasWarning(code) {
		a.fail("expected a %s warning", code)
	}
	return a
}

// NoWarn asserts the trace produced no warning with code.
func (a *Assertion) NoWarn(code querytrace.WarningCode) *Assertion {
	a.t.Helper()
	if a.hasWarning(code) {
		a.fail("unexpected %s warning", code)
	}
	return a
}

// NoWarnings asserts the trace produced no warning at all.
func (a *Assertion) NoWarnings() *Assertion {
	a.t.Helper()
	if n := len(a.rep.Warnings); n > 0 {
		a.fail("expected no warnings, got %d", n)
	}
	return a
}

// NoNPlusOne asserts the trace produced neither a possible-N+1 nor a
// query-in-loop warning.
func (a *Assertion) NoNPlusOne() *Assertion {
	a.t.Helper()
	if a.hasWarning(querytrace.WarnPossibleNPlusOne) || a.hasWarning(querytrace.WarnQueryInLoop) {
		a.fail("unexpected N+1 / query-in-loop warning")
	}
	return a
}

// Transactions asserts exactly n transactions began within the trace.
func (a *Assertion) Transactions(n int) *Assertion {
	a.t.Helper()
	if got := a.txCount(querytrace.TxBegin); got != n {
		a.fail("expected %d transactions, got %d", n, got)
	}
	return a
}

// NoTransaction asserts no transaction began within the trace.
func (a *Assertion) NoTransaction() *Assertion {
	a.t.Helper()
	if got := a.txCount(querytrace.TxBegin); got > 0 {
		a.fail("expected no transaction, got %d", got)
	}
	return a
}

// Committed asserts at least one transaction committed.
func (a *Assertion) Committed() *Assertion {
	a.t.Helper()
	if a.txCount(querytrace.TxCommit) == 0 {
		a.fail("expected a committed transaction, got none")
	}
	return a
}

// RolledBack asserts at least one transaction rolled back.
func (a *Assertion) RolledBack() *Assertion {
	a.t.Helper()
	if a.txCount(querytrace.TxRollback) == 0 {
		a.fail("expected a rolled-back transaction, got none")
	}
	return a
}

// AllRowsClosed asserts no read left its row cursor open (no rows_not_closed
// warning).
func (a *Assertion) AllRowsClosed() *Assertion {
	a.t.Helper()
	if a.hasWarning(querytrace.WarnRowsNotClosed) {
		a.fail("expected all row cursors closed, found an unclosed cursor")
	}
	return a
}

// Query narrows the assertion to the statements matching every Match in ms and
// returns a QueryAssertion to check how many ran. With no matchers it selects
// every statement.
func (a *Assertion) Query(ms ...Match) *QueryAssertion {
	return &QueryAssertion{a: a, ms: ms}
}

// InOrder asserts that statements matching ms appear in the trace in the given
// relative order: an event matching ms[0] before one matching ms[1], and so on.
// The matched events need not be adjacent, and matchers may be transaction
// matchers (Commit, Rollback, Begin), so Insert() before Commit() is
// expressible. Compose multi-condition steps with All.
func (a *Assertion) InOrder(ms ...Match) *Assertion {
	a.t.Helper()
	if len(ms) == 0 {
		return a
	}
	i := 0
	for _, ev := range a.rep.Events {
		if ms[i].fn(ev) {
			if i++; i == len(ms) {
				return a
			}
		}
	}
	a.fail("expected statements in order: %s (matched %d of %d)", describe(ms), i, len(ms))
	return a
}

func (a *Assertion) hasWarning(code querytrace.WarningCode) bool {
	for _, w := range a.rep.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func (a *Assertion) txCount(action querytrace.TxAction) int {
	n := 0
	for _, ev := range a.rep.Events {
		if ev.Kind == querytrace.EventTx && ev.Tx != nil && ev.Tx.Action == action {
			n++
		}
	}
	return n
}

// Count returns how many statements in trace match every Match in ms. It is the
// spy query underneath Query(...).Times, usable without a testing.TB.
func Count(trace *querytrace.Trace, ms ...Match) int {
	n := 0
	for _, ev := range statementEvents(trace.Events()) {
		if matchAll(ev, ms) {
			n++
		}
	}
	return n
}

// Events returns the statements in trace matching every Match in ms, in record
// order.
func Events(trace *querytrace.Trace, ms ...Match) []querytrace.Event {
	var out []querytrace.Event
	for _, ev := range statementEvents(trace.Events()) {
		if matchAll(ev, ms) {
			out = append(out, ev)
		}
	}
	return out
}

// statementEvents returns the query and exec events from evs, dropping
// transaction and other timeline points.
func statementEvents(evs []querytrace.Event) []querytrace.Event {
	out := make([]querytrace.Event, 0, len(evs))
	for _, ev := range evs {
		if ev.Kind == querytrace.EventQuery || ev.Kind == querytrace.EventExec {
			out = append(out, ev)
		}
	}
	return out
}
