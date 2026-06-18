package driver_test

import (
	"context"
	stdsql "database/sql"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	qtdriver "github.com/aita/sqlkit/querytrace/driver"
	"github.com/aita/sqlkit/querytrace/querytest"
)

func testConfig() querytrace.Config {
	cfg := querytrace.NewConfig(
		querytrace.WithParser(fakeParser{}),
		querytrace.WithVerboseCapture(),
		querytrace.WithNPlusOne(querytrace.NPlusOneConfig{Enabled: true, MinCount: 3}),
	)
	return cfg
}

func newDB(t *testing.T, cfg querytrace.Config, rowCount int) *stdsql.DB {
	t.Helper()
	db := stdsql.OpenDB(qtdriver.WrapConnector(fakeConnector{rowCount: rowCount}, cfg))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func drainRows(t *testing.T, rows *stdsql.Rows) int {
	t.Helper()
	n := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return n
}

func hasWarning(trace *querytrace.Trace, code querytrace.WarningCode) bool {
	for _, w := range trace.Warnings() {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestQueryRecordsEventAndRows(t *testing.T) {
	db := newDB(t, testConfig(), 3)
	trace := querytrace.New("GET /users")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := drainRows(t, rows); got != 3 {
		t.Fatalf("read %d rows, want 3", got)
	}

	events := trace.Events()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Kind != querytrace.EventQuery {
		t.Errorf("kind = %v, want query", ev.Kind)
	}
	if ev.Query == nil {
		t.Fatal("event has no QueryInfo")
	}
	if ev.Query.Operation != querytrace.StmtSelect {
		t.Errorf("operation = %v, want select", ev.Query.Operation)
	}
	if len(ev.Query.Tables) != 1 || ev.Query.Tables[0] != "users" {
		t.Errorf("tables = %v, want [users]", ev.Query.Tables)
	}
	if !ev.Query.HasWhere {
		t.Error("HasWhere = false, want true")
	}
	if ev.Caller == nil {
		t.Error("caller not captured under CallerAlways")
	}
	if ev.Rows == nil {
		t.Fatal("rows lifecycle not recorded")
	}
	if ev.Rows.NextCount != 3 {
		t.Errorf("NextCount = %d, want 3", ev.Rows.NextCount)
	}
	if !ev.Rows.Closed {
		t.Error("rows not marked closed")
	}
}

func TestNPlusOneLoopDetected(t *testing.T) {
	db := newDB(t, testConfig(), 1)
	trace := querytrace.New("GET /posts/:id")
	ctx := querytrace.WithTrace(context.Background(), trace)

	for i := 0; i < 3; i++ {
		rows, err := db.QueryContext(ctx, "SELECT * FROM comments WHERE post_id = $1", i)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		drainRows(t, rows)
	}

	// All three queries share a fingerprint and a single call site: a loop.
	querytest.Expect(t, trace).Warns(querytrace.WarnQueryInLoop)
}

func TestForbidQueries(t *testing.T) {
	db := newDB(t, testConfig(), 1)
	trace := querytrace.New("CacheHit")
	ctx := querytrace.ForbidQueries(querytrace.WithTrace(context.Background(), trace))

	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	drainRows(t, rows)

	querytest.Expect(t, trace).Warns(querytrace.WarnQueryForbidden)
}

func TestReadOnlyWriteWarns(t *testing.T) {
	db := newDB(t, testConfig(), 1)
	trace := querytrace.New("PreviewInvoice")
	ctx := querytrace.ReadOnly(querytrace.WithTrace(context.Background(), trace))

	if _, err := db.ExecContext(ctx, "UPDATE users SET name = $1 WHERE id = $2", "x", 1); err != nil {
		t.Fatalf("exec: %v", err)
	}
	querytest.Expect(t, trace).Warns(querytrace.WarnWriteInReadOnly)
}

func TestUpdateWithoutWhere(t *testing.T) {
	db := newDB(t, testConfig(), 1)

	t.Run("missing where warns", func(t *testing.T) {
		trace := querytrace.New("op")
		ctx := querytrace.WithTrace(context.Background(), trace)
		if _, err := db.ExecContext(ctx, "UPDATE users SET name = $1", "x"); err != nil {
			t.Fatalf("exec: %v", err)
		}
		querytest.Expect(t, trace).Warns(querytrace.WarnUpdateWithoutWhere)
	})

	t.Run("with where is clean", func(t *testing.T) {
		trace := querytrace.New("op")
		ctx := querytrace.WithTrace(context.Background(), trace)
		if _, err := db.ExecContext(ctx, "UPDATE users SET name = $1 WHERE id = $2", "x", 1); err != nil {
			t.Fatalf("exec: %v", err)
		}
		if hasWarning(trace, querytrace.WarnUpdateWithoutWhere) {
			t.Error("update with WHERE should not warn")
		}
	})
}

func TestRowsNotClosed(t *testing.T) {
	db := newDB(t, testConfig(), 5)
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Read one row and abandon the cursor without closing it.
	if !rows.Next() {
		t.Fatal("expected a row")
	}
	if !hasWarning(trace, querytrace.WarnRowsNotClosed) {
		t.Errorf("expected rows_not_closed warning\n%s", querytrace.TextReport(trace))
	}
	_ = rows.Close() // cleanup
}

func TestTransactionRecorded(t *testing.T) {
	db := newDB(t, testConfig(), 1)
	trace := querytrace.New("CreateOrder")
	ctx := querytrace.WithTrace(context.Background(), trace)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orders SET total = $1 WHERE id = $2", 10, 1); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var begins, commits, execs int
	for _, ev := range trace.Events() {
		switch ev.Kind {
		case querytrace.EventTx:
			switch ev.Tx.Action {
			case querytrace.TxBegin:
				begins++
			case querytrace.TxCommit:
				commits++
			}
		case querytrace.EventExec:
			execs++
		}
	}
	if begins != 1 || commits != 1 || execs != 1 {
		t.Errorf("begins=%d commits=%d execs=%d, want 1/1/1\n%s", begins, commits, execs, querytrace.TextReport(trace))
	}
}

func TestTransactionLinksStatements(t *testing.T) {
	db := newDB(t, testConfig(), 1)
	trace := querytrace.New("CreateOrder")
	ctx := querytrace.WithTrace(context.Background(), trace)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orders SET total = $1 WHERE id = $2", 10, 1); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A statement after the transaction commits must not be linked to it.
	if _, err := db.ExecContext(ctx, "UPDATE orders SET total = $1 WHERE id = $2", 20, 2); err != nil {
		t.Fatalf("post-tx exec: %v", err)
	}

	var txID string
	for _, ev := range trace.Events() {
		if ev.Kind == querytrace.EventTx && ev.Tx.Action == querytrace.TxBegin {
			txID = ev.Tx.TxID
		}
	}
	if txID == "" {
		t.Fatal("no begin event recorded")
	}

	var inTx, outTx int
	for _, ev := range trace.Events() {
		if ev.Kind != querytrace.EventExec || ev.Query == nil {
			continue
		}
		switch ev.Query.TxID {
		case txID:
			inTx++
		case "":
			outTx++
		default:
			t.Errorf("exec linked to unexpected tx %q", ev.Query.TxID)
		}
	}
	if inTx != 1 {
		t.Errorf("statements linked to the transaction = %d, want 1", inTx)
	}
	if outTx != 1 {
		t.Errorf("statements outside the transaction = %d, want 1", outTx)
	}
}

func TestSamplingPassthrough(t *testing.T) {
	db := newDB(t, testConfig(), 2)
	trace := querytrace.New("op", querytrace.WithSampled(false))
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := drainRows(t, rows); got != 2 {
		t.Fatalf("read %d rows, want 2", got)
	}

	// A transaction under a sampled-out trace is a pass-through too.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET name = $1 WHERE id = $2", "x", 1); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n := len(trace.Events()); n != 0 {
		t.Errorf("sampled-out trace recorded %d events, want 0\n%s", n, querytrace.TextReport(trace))
	}
	if len(trace.Warnings()) != 0 {
		t.Error("sampled-out trace should produce no warnings")
	}
}

func TestPreparedStatement(t *testing.T) {
	db := newDB(t, testConfig(), 2)
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	stmt, err := db.PrepareContext(ctx, "SELECT * FROM users WHERE id = $1")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	rows, err := stmt.QueryContext(ctx, 1)
	if err != nil {
		t.Fatalf("stmt query: %v", err)
	}
	if got := drainRows(t, rows); got != 2 {
		t.Fatalf("read %d rows, want 2", got)
	}

	events := trace.Events()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	if ev := events[0]; ev.Query == nil || ev.Query.Operation != querytrace.StmtSelect {
		t.Errorf("prepared statement query not analyzed: %+v", ev.Query)
	}
}

func TestNoTracePassthrough(t *testing.T) {
	db := newDB(t, testConfig(), 2)
	// No Trace in the context: the wrapper must be a transparent pass-through.
	rows, err := db.QueryContext(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := drainRows(t, rows); got != 2 {
		t.Fatalf("read %d rows, want 2", got)
	}
}

func TestQuerytestAssertions(t *testing.T) {
	db := newDB(t, testConfig(), 1)
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	drainRows(t, rows)

	querytest.Expect(t, trace).
		MaxQueries(5).
		NoWrites().
		NoNPlusOne().
		Query(querytest.Select(), querytest.Table("users")).Times(1)
}
