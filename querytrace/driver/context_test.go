package driver_test

import (
	"context"
	"errors"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/querytrace/querytest"
)

// TestRowsIterationCanceled checks that the rows wrapper records a context
// cancellation that ends iteration before the cursor is drained: read one row,
// cancel, then close. The cursor never reached EOF, so Close captures the
// context error as RowsInfo.IterErr and the trace raises query_canceled.
func TestRowsIterationCanceled(t *testing.T) {
	db := newDB(t, testConfig(), 1000) // many rows, so a single read is far from EOF
	trace := querytrace.New("GET /users")
	base := querytrace.WithTrace(context.Background(), trace)
	ctx, cancel := context.WithCancel(base)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("expected at least one row: %v", rows.Err())
	}
	cancel()
	if err := rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	events := trace.Events()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Rows == nil {
		t.Fatal("rows lifecycle not recorded")
	}
	if !errors.Is(ev.Rows.IterErr, context.Canceled) {
		t.Fatalf("IterErr = %v, want context.Canceled", ev.Rows.IterErr)
	}
	querytest.Expect(t, trace).Warns(querytrace.WarnQueryCanceled)
}

// TestRowsDrainedCleanlyHasNoIterErr guards against a false positive: a cursor
// drained to completion records no iteration error even though no cancellation
// occurred.
func TestRowsDrainedCleanlyHasNoIterErr(t *testing.T) {
	db := newDB(t, testConfig(), 3)
	trace := querytrace.New("GET /users")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ev := trace.Events()[0]
	if ev.Rows.IterErr != nil {
		t.Errorf("IterErr = %v, want nil for a cleanly drained cursor", ev.Rows.IterErr)
	}
	if hasWarning(trace, querytrace.WarnQueryCanceled) {
		t.Error("a cleanly drained cursor should not raise query_canceled")
	}
}
