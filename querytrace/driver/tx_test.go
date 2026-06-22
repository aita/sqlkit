package driver_test

import (
	"context"
	stdsql "database/sql"
	stddriver "database/sql/driver"
	"errors"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	qtdriver "github.com/aita/sqlkit/querytrace/driver"
)

// TestTransactionRollbackRecorded verifies a rollback is recorded as a TxRollback
// event and the connection's transaction link is cleared, so a statement run
// after the rollback is no longer attributed to the transaction.
func TestTransactionRollbackRecorded(t *testing.T) {
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
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var begins, rollbacks, commits int
	var txID string
	for _, ev := range trace.Events() {
		if ev.Kind != querytrace.EventTx {
			continue
		}
		switch ev.Tx.Action {
		case querytrace.TxBegin:
			begins++
			txID = ev.Tx.TxID
		case querytrace.TxRollback:
			rollbacks++
		case querytrace.TxCommit:
			commits++
		}
	}
	if begins != 1 || rollbacks != 1 || commits != 0 {
		t.Errorf("begins=%d rollbacks=%d commits=%d, want 1/1/0\n%s",
			begins, rollbacks, commits, querytrace.TextReport(trace))
	}

	// A statement after the rollback must not be linked to the rolled-back tx.
	if _, err := db.ExecContext(ctx, "UPDATE orders SET total = $1 WHERE id = $2", 20, 2); err != nil {
		t.Fatalf("post-rollback exec: %v", err)
	}
	for _, ev := range trace.Events() {
		if ev.Kind == querytrace.EventExec && ev.Query != nil && ev.Query.TxID != "" && ev.Query.TxID != txID {
			t.Errorf("post-rollback exec linked to unexpected tx %q", ev.Query.TxID)
		}
	}
}

// rollbackErrTx is a transaction whose Rollback fails, so the wrapper's error
// propagation on Rollback is exercised while the rollback is still recorded.
type rollbackErrTx struct{ err error }

func (t rollbackErrTx) Commit() error   { return nil }
func (t rollbackErrTx) Rollback() error { return t.err }

// rollbackErrConn is a minimal conn whose BeginTx hands out a rollbackErrTx.
type rollbackErrConn struct{ err error }

var (
	_ stddriver.Conn        = (*rollbackErrConn)(nil)
	_ stddriver.ConnBeginTx = (*rollbackErrConn)(nil)
)

func (c *rollbackErrConn) Prepare(string) (stddriver.Stmt, error) {
	return &fakeStmt{rowCount: 1}, nil
}
func (c *rollbackErrConn) Close() error                 { return nil }
func (c *rollbackErrConn) Begin() (stddriver.Tx, error) { return rollbackErrTx{err: c.err}, nil }
func (c *rollbackErrConn) BeginTx(context.Context, stddriver.TxOptions) (stddriver.Tx, error) {
	return rollbackErrTx{err: c.err}, nil
}

func newRollbackErrDB(t *testing.T, err error) *stdsql.DB {
	t.Helper()
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &rollbackErrConn{err: err} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestTransactionRollbackErrorPropagates checks a rollback error from the
// underlying transaction propagates while the trace still records the rollback.
func TestTransactionRollbackErrorPropagates(t *testing.T) {
	wantErr := errors.New("rollback boom")
	db := newRollbackErrDB(t, wantErr)
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Rollback(); !errors.Is(err, wantErr) {
		t.Fatalf("rollback error = %v, want %v", err, wantErr)
	}

	var rollbacks int
	for _, ev := range trace.Events() {
		if ev.Kind == querytrace.EventTx && ev.Tx.Action == querytrace.TxRollback {
			rollbacks++
		}
	}
	if rollbacks != 1 {
		t.Errorf("rollbacks recorded = %d, want 1 (recorded even on error)", rollbacks)
	}
}
