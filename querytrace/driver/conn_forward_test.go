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

// pingConn implements driver.Pinger, recording calls and returning a
// configurable error.
type pingConn struct {
	pings   int
	pingErr error
}

var (
	_ stddriver.Conn               = (*pingConn)(nil)
	_ stddriver.Pinger             = (*pingConn)(nil)
	_ stddriver.QueryerContext     = (*pingConn)(nil)
	_ stddriver.ConnPrepareContext = (*pingConn)(nil)
)

func (c *pingConn) Ping(context.Context) error { c.pings++; return c.pingErr }
func (c *pingConn) QueryContext(context.Context, string, []stddriver.NamedValue) (stddriver.Rows, error) {
	return &fakeRows{remaining: 1}, nil
}
func (c *pingConn) PrepareContext(context.Context, string) (stddriver.Stmt, error) {
	return &fakeStmt{rowCount: 1}, nil
}
func (c *pingConn) Prepare(string) (stddriver.Stmt, error) { return &fakeStmt{rowCount: 1}, nil }
func (c *pingConn) Close() error                           { return nil }
func (c *pingConn) Begin() (stddriver.Tx, error)           { return fakeTx{}, nil }

// TestConnPingForwardedToUnderlying checks Ping reaches an underlying Pinger and
// its error propagates.
func TestConnPingForwardedToUnderlying(t *testing.T) {
	under := &pingConn{}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return under }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if under.pings == 0 {
		t.Error("Ping was not forwarded to the underlying Pinger")
	}

	wantErr := errors.New("ping failed")
	under.pingErr = wantErr
	if err := db.PingContext(context.Background()); !errors.Is(err, wantErr) {
		t.Errorf("ping error = %v, want %v", err, wantErr)
	}
}

// nonPingerConn implements neither Pinger nor ConnBeginTx: it is a minimal
// driver.Conn plus QueryerContext, so the wrapper's Ping reports healthy without
// an underlying Pinger and BeginTx falls back to the legacy Begin.
type nonPingerConn struct {
	begins int
}

var (
	_ stddriver.Conn           = (*nonPingerConn)(nil)
	_ stddriver.QueryerContext = (*nonPingerConn)(nil)
)

func (c *nonPingerConn) QueryContext(context.Context, string, []stddriver.NamedValue) (stddriver.Rows, error) {
	return &fakeRows{remaining: 1}, nil
}
func (c *nonPingerConn) Prepare(string) (stddriver.Stmt, error) { return &fakeStmt{rowCount: 1}, nil }
func (c *nonPingerConn) Close() error                           { return nil }
func (c *nonPingerConn) Begin() (stddriver.Tx, error)           { c.begins++; return fakeTx{}, nil }

// TestConnPingDefaultWhenUnderlyingNotPinger checks that wrapping a connection
// that is not a Pinger still answers Ping as healthy (database/sql's default),
// without panicking.
func TestConnPingDefaultWhenUnderlyingNotPinger(t *testing.T) {
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &nonPingerConn{} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping over non-Pinger conn = %v, want nil", err)
	}
}

// TestConnBeginTxFallsBackToLegacyBegin drives BeginTx over a connection that
// does not implement ConnBeginTx, so the wrapper must fall back to the legacy
// Begin while still recording the transaction.
func TestConnBeginTxFallsBackToLegacyBegin(t *testing.T) {
	under := &nonPingerConn{}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return under }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	trace := querytrace.New("tx-legacy")
	ctx := querytrace.WithTrace(context.Background(), trace)
	// Default (nil) options keep the legacy Begin reachable: database/sql only
	// rejects non-default isolation/read-only when ConnBeginTx is absent.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if under.begins == 0 {
		t.Error("BeginTx did not fall back to the underlying legacy Begin")
	}

	var begins, commits int
	for _, ev := range trace.Events() {
		if ev.Kind == querytrace.EventTx {
			switch ev.Tx.Action {
			case querytrace.TxBegin:
				begins++
			case querytrace.TxCommit:
				commits++
			}
		}
	}
	if begins != 1 || commits != 1 {
		t.Errorf("begins=%d commits=%d, want 1/1", begins, commits)
	}
}

// beginErrConn fails on the legacy Begin so BeginTx error propagation (no
// ConnBeginTx) is exercised.
type beginErrConn struct{ err error }

var (
	_ stddriver.Conn           = (*beginErrConn)(nil)
	_ stddriver.QueryerContext = (*beginErrConn)(nil)
)

func (c *beginErrConn) QueryContext(context.Context, string, []stddriver.NamedValue) (stddriver.Rows, error) {
	return &fakeRows{remaining: 1}, nil
}
func (c *beginErrConn) Prepare(string) (stddriver.Stmt, error) { return &fakeStmt{rowCount: 1}, nil }
func (c *beginErrConn) Close() error                           { return nil }
func (c *beginErrConn) Begin() (stddriver.Tx, error)           { return nil, c.err }

// TestConnBeginTxLegacyBeginError checks a legacy Begin failure propagates from
// BeginTx.
func TestConnBeginTxLegacyBeginError(t *testing.T) {
	wantErr := errors.New("begin boom")
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &beginErrConn{err: wantErr} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })

	trace := querytrace.New("tx-err")
	ctx := querytrace.WithTrace(context.Background(), trace)
	if _, err := db.BeginTx(ctx, nil); !errors.Is(err, wantErr) {
		t.Fatalf("begin error = %v, want %v", err, wantErr)
	}
}

// plainConn implements neither QueryerContext nor ExecerContext, so the
// wrapper's QueryContext/ExecContext return driver.ErrSkip and database/sql
// falls back to the prepare path. Prepare/PrepareContext still route through the
// wrapper, so the statement is recorded.
type plainConn struct {
	rowCount int
	prepares int
}

var (
	_ stddriver.Conn               = (*plainConn)(nil)
	_ stddriver.ConnPrepareContext = (*plainConn)(nil)
)

func (c *plainConn) PrepareContext(context.Context, string) (stddriver.Stmt, error) {
	c.prepares++
	return &fakeStmt{rowCount: c.rowCount}, nil
}
func (c *plainConn) Prepare(string) (stddriver.Stmt, error) {
	c.prepares++
	return &fakeStmt{rowCount: c.rowCount}, nil
}
func (c *plainConn) Close() error                 { return nil }
func (c *plainConn) Begin() (stddriver.Tx, error) { return fakeTx{}, nil }

// TestConnQueryExecSkipFallsBackToPrepare verifies that when the underlying
// connection lacks the context Queryer/Execer interfaces, the wrapper returns
// ErrSkip (so database/sql prepares a statement) and the prepared path still
// records the statement.
func TestConnQueryExecSkipFallsBackToPrepare(t *testing.T) {
	under := &plainConn{rowCount: 2}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return under }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	trace := querytrace.New("skip")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := drainRows(t, rows); got != 2 {
		t.Fatalf("read %d rows, want 2", got)
	}
	if _, err := db.ExecContext(ctx, "UPDATE users SET name = $1 WHERE id = $2", "x", 1); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if under.prepares == 0 {
		t.Error("ErrSkip did not route through the prepare fallback")
	}

	var queries, execs int
	for _, ev := range trace.Events() {
		switch ev.Kind {
		case querytrace.EventQuery:
			queries++
		case querytrace.EventExec:
			execs++
		}
	}
	if queries != 1 || execs != 1 {
		t.Errorf("queries=%d execs=%d, want 1/1 (prepared path still records)", queries, execs)
	}
}

// legacyPrepareConn implements neither QueryerContext/ExecerContext nor
// ConnPrepareContext: only the legacy Prepare. So database/sql prepares every
// statement, and the wrapper's PrepareContext must fall back to the legacy
// Prepare path.
type legacyPrepareConn struct {
	rowCount int
	prepares int
}

var _ stddriver.Conn = (*legacyPrepareConn)(nil)

func (c *legacyPrepareConn) Prepare(string) (stddriver.Stmt, error) {
	c.prepares++
	return &fakeStmt{rowCount: c.rowCount}, nil
}
func (c *legacyPrepareConn) Close() error                 { return nil }
func (c *legacyPrepareConn) Begin() (stddriver.Tx, error) { return fakeTx{}, nil }

// TestConnPrepareContextFallsBackToLegacyPrepare verifies that when the
// underlying connection lacks ConnPrepareContext, the wrapper's PrepareContext
// falls back to the legacy Prepare while still instrumenting the statement.
func TestConnPrepareContextFallsBackToLegacyPrepare(t *testing.T) {
	under := &legacyPrepareConn{rowCount: 2}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return under }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	trace := querytrace.New("legacy-prepare")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := drainRows(t, rows); got != 2 {
		t.Fatalf("read %d rows, want 2", got)
	}
	if under.prepares == 0 {
		t.Error("PrepareContext did not fall back to the legacy Prepare")
	}

	var queries int
	for _, ev := range trace.Events() {
		if ev.Kind == querytrace.EventQuery {
			queries++
		}
	}
	if queries != 1 {
		t.Errorf("queries recorded = %d, want 1 (legacy prepare still instruments)", queries)
	}
}

// prepareErrConn fails on Prepare/PrepareContext so the wrapper's prepare error
// propagation is exercised.
type prepareErrConn struct{ err error }

var (
	_ stddriver.Conn               = (*prepareErrConn)(nil)
	_ stddriver.ConnPrepareContext = (*prepareErrConn)(nil)
)

func (c *prepareErrConn) Prepare(string) (stddriver.Stmt, error) { return nil, c.err }
func (c *prepareErrConn) PrepareContext(context.Context, string) (stddriver.Stmt, error) {
	return nil, c.err
}
func (c *prepareErrConn) Close() error                 { return nil }
func (c *prepareErrConn) Begin() (stddriver.Tx, error) { return fakeTx{}, nil }

// TestConnPrepareContextErrorPropagates checks a PrepareContext failure from the
// underlying connection propagates (and yields no instrumented statement).
func TestConnPrepareContextErrorPropagates(t *testing.T) {
	wantErr := errors.New("prepare boom")
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &prepareErrConn{err: wantErr} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.PrepareContext(context.Background(), "SELECT * FROM users"); !errors.Is(err, wantErr) {
		t.Fatalf("prepare error = %v, want %v", err, wantErr)
	}
}

// skipQueryConn is a QueryerContext (and ExecerContext) whose context methods
// return driver.ErrSkip, so database/sql falls back to the prepare path. This
// drives the ErrSkip branch *inside* instrumentQuery/instrumentExec (the run()
// closure returns ErrSkip under a sampled trace), distinct from the conn-level
// ErrSkip returned when the interface is absent entirely.
type skipQueryConn struct{ rowCount int }

var (
	_ stddriver.Conn               = (*skipQueryConn)(nil)
	_ stddriver.QueryerContext     = (*skipQueryConn)(nil)
	_ stddriver.ExecerContext      = (*skipQueryConn)(nil)
	_ stddriver.ConnPrepareContext = (*skipQueryConn)(nil)
)

func (c *skipQueryConn) QueryContext(context.Context, string, []stddriver.NamedValue) (stddriver.Rows, error) {
	return nil, stddriver.ErrSkip
}
func (c *skipQueryConn) ExecContext(context.Context, string, []stddriver.NamedValue) (stddriver.Result, error) {
	return nil, stddriver.ErrSkip
}
func (c *skipQueryConn) PrepareContext(context.Context, string) (stddriver.Stmt, error) {
	return &fakeStmt{rowCount: c.rowCount}, nil
}
func (c *skipQueryConn) Prepare(string) (stddriver.Stmt, error) {
	return &fakeStmt{rowCount: c.rowCount}, nil
}
func (c *skipQueryConn) Close() error                 { return nil }
func (c *skipQueryConn) Begin() (stddriver.Tx, error) { return fakeTx{}, nil }

// TestInstrumentSkipFromUnderlyingFallsBackToPrepare verifies that when the
// underlying QueryerContext/ExecerContext returns driver.ErrSkip under a sampled
// trace, the wrapper does not record a spurious event for the skip and
// database/sql falls back to the prepared path (which records exactly one
// event).
func TestInstrumentSkipFromUnderlyingFallsBackToPrepare(t *testing.T) {
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &skipQueryConn{rowCount: 2} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	trace := querytrace.New("skip-inner")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := drainRows(t, rows); got != 2 {
		t.Fatalf("read %d rows, want 2", got)
	}
	if _, err := db.ExecContext(ctx, "UPDATE users SET name = $1 WHERE id = $2", "x", 1); err != nil {
		t.Fatalf("exec: %v", err)
	}

	var queries, execs int
	for _, ev := range trace.Events() {
		switch ev.Kind {
		case querytrace.EventQuery:
			queries++
		case querytrace.EventExec:
			execs++
		}
	}
	// Exactly one of each: the inner ErrSkip must not record a duplicate event
	// alongside the prepared-path recording.
	if queries != 1 || execs != 1 {
		t.Errorf("queries=%d execs=%d, want 1/1 (ErrSkip must not double-record)\n%s",
			queries, execs, querytrace.TextReport(trace))
	}
}

// nvcConn implements driver.NamedValueChecker on the connection so the wrapper's
// connNamedValueChecker variant is selected and its CheckNamedValue forwards.
type nvcConn struct {
	checks int
}

var (
	_ stddriver.Conn              = (*nvcConn)(nil)
	_ stddriver.ExecerContext     = (*nvcConn)(nil)
	_ stddriver.NamedValueChecker = (*nvcConn)(nil)
)

func (c *nvcConn) CheckNamedValue(nv *stddriver.NamedValue) error {
	c.checks++
	// Force the value through so database/sql does not apply its own default
	// conversion to the int below.
	nv.Value = int64(99)
	return nil
}
func (c *nvcConn) ExecContext(context.Context, string, []stddriver.NamedValue) (stddriver.Result, error) {
	return fakeResult{rows: 1}, nil
}
func (c *nvcConn) Prepare(string) (stddriver.Stmt, error) { return &fakeStmt{rowCount: 1}, nil }
func (c *nvcConn) Close() error                           { return nil }
func (c *nvcConn) Begin() (stddriver.Tx, error)           { return fakeTx{}, nil }

// TestConnNamedValueCheckerForwarded verifies the wrapper advertises and
// forwards a connection-level NamedValueChecker: with the conn-level checker
// present and the ExecerContext path taken (no statement), database/sql consults
// the wrapper's CheckNamedValue, which forwards to the underlying.
func TestConnNamedValueCheckerForwarded(t *testing.T) {
	under := &nvcConn{}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return under }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(context.Background(), "UPDATE users SET name = $1", 7); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if under.checks == 0 {
		t.Error("connection-level CheckNamedValue was not forwarded by the wrapper")
	}
}
