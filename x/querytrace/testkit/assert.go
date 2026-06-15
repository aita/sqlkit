// Package testkit provides test assertions over a querytrace.Trace, turning the
// recorded queries and warnings into checks a test can make: a query budget, no
// database access, no writes, no N+1. Use it with an in-context Trace:
//
//	trace := querytrace.New("GetUserPage")
//	ctx := querytrace.WithTrace(context.Background(), trace)
//	service.RenderUserPage(ctx, id)
//	testkit.AssertMaxQueries(t, trace, 5)
//	testkit.AssertNoNPlusOne(t, trace)
package testkit

import (
	"testing"

	qt "github.com/aita/sqlkit/x/querytrace"
)

// AssertMaxQueries fails t when the trace recorded more than max read/write
// statements.
func AssertMaxQueries(t testing.TB, trace *qt.Trace, max int) {
	t.Helper()
	if n := trace.Report().QueryCount; n > max {
		t.Errorf("querytrace: %d queries exceed the budget of %d\n%s", n, max, qt.TextReport(trace))
	}
}

// AssertNoQueries fails t when the trace recorded any statement, asserting a
// code path did not touch the database.
func AssertNoQueries(t testing.TB, trace *qt.Trace) {
	t.Helper()
	if n := trace.Report().QueryCount; n > 0 {
		t.Errorf("querytrace: expected no queries, got %d\n%s", n, qt.TextReport(trace))
	}
}

// AssertNoWrites fails t when the trace recorded any write statement.
func AssertNoWrites(t testing.TB, trace *qt.Trace) {
	t.Helper()
	if n := trace.Report().WriteCount; n > 0 {
		t.Errorf("querytrace: expected no writes, got %d\n%s", n, qt.TextReport(trace))
	}
}

// AssertNoWarnings fails t when the trace produced any warning.
func AssertNoWarnings(t testing.TB, trace *qt.Trace) {
	t.Helper()
	if ws := trace.Warnings(); len(ws) > 0 {
		t.Errorf("querytrace: expected no warnings, got %d\n%s", len(ws), qt.TextReport(trace))
	}
}

// AssertNoNPlusOne fails t when the trace produced a possible-N+1 or
// query-in-loop warning.
func AssertNoNPlusOne(t testing.TB, trace *qt.Trace) {
	t.Helper()
	for _, w := range trace.Warnings() {
		if w.Code == qt.WarnPossibleNPlusOne || w.Code == qt.WarnQueryInLoop {
			t.Errorf("querytrace: unexpected N+1 warning: %s\n%s", w.Message, qt.TextReport(trace))
			return
		}
	}
}

// AssertWarning fails t unless the trace produced a warning with code.
func AssertWarning(t testing.TB, trace *qt.Trace, code qt.WarningCode) {
	t.Helper()
	for _, w := range trace.Warnings() {
		if w.Code == code {
			return
		}
	}
	t.Errorf("querytrace: expected a %s warning\n%s", code, qt.TextReport(trace))
}
