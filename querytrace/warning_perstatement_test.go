package querytrace_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aita/sqlkit/analyze"
	querytrace "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sqltest"
)

// TestExecutionErrorsAreNotWarned documents the scope decision: a statement
// that errored, was canceled, or hit its deadline during execution raises no
// querytrace warning — OpenTelemetry tracing records those as span status.
func TestExecutionErrorsAreNotWarned(t *testing.T) {
	for _, err := range []error{
		errors.New("connection reset"),
		context.Canceled,
		context.DeadlineExceeded,
	} {
		trace := querytrace.New("op", querytrace.WithConfig(querytrace.Config{Enabled: true}))
		trace.RecordStatement(context.Background(), querytrace.Event{
			Kind:  querytrace.EventQuery,
			Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect}},
			Err:   err,
		})
		sqltest.Expect(t, trace).NoWarnings()
	}
}

func TestTooManyParametersWarns(t *testing.T) {
	cfg := querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{MaxParams: 100}}
	trace := querytrace.New("op", querytrace.WithConfig(cfg))

	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect, Fingerprint: "SELECT * FROM t WHERE id IN (?)"}, ParamCount: 100},
	})
	sqltest.Expect(t, trace).Warns(querytrace.WarnTooManyParameters)
}

func TestParametersBelowBudgetIsClean(t *testing.T) {
	cfg := querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{MaxParams: 100}}
	trace := querytrace.New("op", querytrace.WithConfig(cfg))

	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect}, ParamCount: 99},
	})
	sqltest.Expect(t, trace).NoWarnings()
}

func TestIterationCanceledWarns(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.Config{Enabled: true}))
	// The query call itself succeeded (Err nil); the cancellation ended row
	// iteration, recorded on the cursor as IterErr.
	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect}},
		Rows:  &querytrace.RowsInfo{NextCount: 3, Closed: true, IterErr: context.Canceled},
	})
	sqltest.Expect(t, trace).Warns(querytrace.WarnQueryCanceled)
	for _, w := range trace.Warnings() {
		if w.Code == querytrace.WarnQueryCanceled {
			if w.Details["phase"] != "iteration" {
				t.Errorf("phase = %v, want iteration", w.Details["phase"])
			}
			if w.Details["rows_read"] != 3 {
				t.Errorf("rows_read = %v, want 3", w.Details["rows_read"])
			}
		}
	}
}

func TestIterationDeadlineExceededWarns(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.Config{Enabled: true}))
	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect}},
		Rows:  &querytrace.RowsInfo{NextCount: 1, Closed: true, IterErr: context.DeadlineExceeded},
	})
	sqltest.Expect(t, trace).Warns(querytrace.WarnQueryDeadlineExceeded)
}

func TestCleanIterationIsQuiet(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.Config{Enabled: true}))
	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect}},
		Rows:  &querytrace.RowsInfo{NextCount: 3, Closed: true}, // IterErr nil
	})
	sqltest.Expect(t, trace).NoWarnings()
}

// TestIterationWarningsAreIdempotent guards the derived-warning contract:
// repeated Warnings calls must not accumulate duplicates.
func TestIterationWarningsAreIdempotent(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.Config{Enabled: true}))
	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect}},
		Rows:  &querytrace.RowsInfo{NextCount: 5, Closed: true, IterErr: context.Canceled},
	})

	first := len(trace.Warnings())
	second := len(trace.Warnings())
	if first != second {
		t.Fatalf("warning count changed across calls: %d then %d", first, second)
	}
	if first != 1 {
		t.Fatalf("want exactly 1 warning, got %d", first)
	}
}
