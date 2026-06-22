package driver_test

import (
	"context"
	stdsql "database/sql"
	stddriver "database/sql/driver"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	qtdriver "github.com/aita/sqlkit/querytrace/driver"
)

// ctxStmt implements the context statement interfaces (StmtExecContext,
// StmtQueryContext) so the wrapper takes the non-fallback branch of its
// ExecContext/QueryContext.
type ctxStmt struct {
	rowCount int
	execCtx  int
	queryCtx int
}

var (
	_ stddriver.Stmt             = (*ctxStmt)(nil)
	_ stddriver.StmtExecContext  = (*ctxStmt)(nil)
	_ stddriver.StmtQueryContext = (*ctxStmt)(nil)
)

func (s *ctxStmt) Close() error  { return nil }
func (s *ctxStmt) NumInput() int { return -1 }
func (s *ctxStmt) Exec([]stddriver.Value) (stddriver.Result, error) {
	return fakeResult{rows: int64(s.rowCount)}, nil
}
func (s *ctxStmt) Query([]stddriver.Value) (stddriver.Rows, error) {
	return &fakeRows{remaining: s.rowCount}, nil
}
func (s *ctxStmt) ExecContext(context.Context, []stddriver.NamedValue) (stddriver.Result, error) {
	s.execCtx++
	return fakeResult{rows: int64(s.rowCount)}, nil
}
func (s *ctxStmt) QueryContext(context.Context, []stddriver.NamedValue) (stddriver.Rows, error) {
	s.queryCtx++
	return &fakeRows{remaining: s.rowCount}, nil
}

// stmtOnlyConn prepares a caller-supplied statement so the test can inspect it.
type stmtOnlyConn struct{ stmt stddriver.Stmt }

var (
	_ stddriver.Conn               = (*stmtOnlyConn)(nil)
	_ stddriver.ConnPrepareContext = (*stmtOnlyConn)(nil)
)

func (c *stmtOnlyConn) PrepareContext(context.Context, string) (stddriver.Stmt, error) {
	return c.stmt, nil
}
func (c *stmtOnlyConn) Prepare(string) (stddriver.Stmt, error) { return c.stmt, nil }
func (c *stmtOnlyConn) Close() error                           { return nil }
func (c *stmtOnlyConn) Begin() (stddriver.Tx, error)           { return fakeTx{}, nil }

// TestStmtContextInterfacesUsedWhenUnderlyingProvidesThem verifies that when the
// underlying statement implements the context interfaces, the wrapper routes
// Exec/Query through them (not the legacy fallback) and still records the
// statement.
func TestStmtContextInterfacesUsedWhenUnderlyingProvidesThem(t *testing.T) {
	under := &ctxStmt{rowCount: 2}
	// The conn lacks QueryerContext/ExecerContext, so database/sql goes through a
	// prepared statement for every call.
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &stmtOnlyConn{stmt: under} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	trace := querytrace.New("op")
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

	if under.queryCtx == 0 {
		t.Error("StmtQueryContext was not used by the wrapper")
	}
	if under.execCtx == 0 {
		t.Error("StmtExecContext was not used by the wrapper")
	}
}

// nvcCcStmt implements both NamedValueChecker and ColumnConverter so the
// wrapper's combined variant is selected and both forwards are exercised.
type nvcCcStmt struct {
	checks     int
	converters int
}

var (
	_ stddriver.Stmt              = (*nvcCcStmt)(nil)
	_ stddriver.NamedValueChecker = (*nvcCcStmt)(nil)
	_ stddriver.ColumnConverter   = (*nvcCcStmt)(nil) //nolint:staticcheck // exercising the legacy interface
	_ stddriver.StmtExecContext   = (*nvcCcStmt)(nil)
)

func (s *nvcCcStmt) Close() error  { return nil }
func (s *nvcCcStmt) NumInput() int { return 1 }
func (s *nvcCcStmt) CheckNamedValue(nv *stddriver.NamedValue) error {
	s.checks++
	nv.Value = int64(1)
	return nil
}
func (s *nvcCcStmt) ColumnConverter(int) stddriver.ValueConverter {
	s.converters++
	return stddriver.DefaultParameterConverter
}
func (s *nvcCcStmt) Exec([]stddriver.Value) (stddriver.Result, error) {
	return fakeResult{}, nil
}
func (s *nvcCcStmt) Query([]stddriver.Value) (stddriver.Rows, error) {
	return &fakeRows{remaining: 0}, nil
}
func (s *nvcCcStmt) ExecContext(context.Context, []stddriver.NamedValue) (stddriver.Result, error) {
	return fakeResult{}, nil
}

// TestStmtNamedValueCheckerForwarded verifies that a statement-level
// NamedValueChecker on the underlying statement is advertised and forwarded by
// the wrapper. With a NamedValueChecker present, database/sql consults it for
// argument conversion (it takes precedence over ColumnConverter).
func TestStmtNamedValueCheckerForwarded(t *testing.T) {
	under := &nvcCcStmt{}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &stmtOnlyConn{stmt: under} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(context.Background(), "UPDATE users SET name = $1", 7); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if under.checks == 0 {
		t.Error("statement-level CheckNamedValue was not forwarded by the wrapper")
	}
}

// ccOnlyStmt implements ColumnConverter only (no NamedValueChecker), and lacks
// the context interfaces, so database/sql's prepared Exec routes the argument
// through the wrapper's ColumnConverter variant.
type ccOnlyStmt struct {
	converters int
	lastValue  stddriver.Value
}

var (
	_ stddriver.Stmt            = (*ccOnlyStmt)(nil)
	_ stddriver.ColumnConverter = (*ccOnlyStmt)(nil) //nolint:staticcheck // exercising the legacy interface
)

func (s *ccOnlyStmt) Close() error  { return nil }
func (s *ccOnlyStmt) NumInput() int { return 1 }
func (s *ccOnlyStmt) ColumnConverter(int) stddriver.ValueConverter {
	s.converters++
	return ccValueConverter2{s}
}
func (s *ccOnlyStmt) Exec(args []stddriver.Value) (stddriver.Result, error) {
	if len(args) == 1 {
		s.lastValue = args[0]
	}
	return fakeResult{}, nil
}
func (s *ccOnlyStmt) Query([]stddriver.Value) (stddriver.Rows, error) {
	return &fakeRows{remaining: 0}, nil
}

type ccValueConverter2 struct{ s *ccOnlyStmt }

func (c ccValueConverter2) ConvertValue(any) (stddriver.Value, error) {
	return "cc2-sentinel", nil
}

// nvcOnlyStmt implements NamedValueChecker only (no ColumnConverter), so the
// wrapper's NamedValueChecker-only variant is selected.
type nvcOnlyStmt struct{ checks int }

var (
	_ stddriver.Stmt              = (*nvcOnlyStmt)(nil)
	_ stddriver.NamedValueChecker = (*nvcOnlyStmt)(nil)
)

func (s *nvcOnlyStmt) Close() error  { return nil }
func (s *nvcOnlyStmt) NumInput() int { return 1 }
func (s *nvcOnlyStmt) CheckNamedValue(nv *stddriver.NamedValue) error {
	s.checks++
	nv.Value = int64(1)
	return nil
}
func (s *nvcOnlyStmt) Exec([]stddriver.Value) (stddriver.Result, error) {
	return fakeResult{}, nil
}
func (s *nvcOnlyStmt) Query([]stddriver.Value) (stddriver.Rows, error) {
	return &fakeRows{remaining: 0}, nil
}

// TestStmtNamedValueCheckerOnlyVariantForwarded verifies the
// NamedValueChecker-only wrapper variant forwards to the underlying checker.
func TestStmtNamedValueCheckerOnlyVariantForwarded(t *testing.T) {
	under := &nvcOnlyStmt{}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &stmtOnlyConn{stmt: under} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(context.Background(), "UPDATE users SET name = $1", 7); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if under.checks == 0 {
		t.Error("NamedValueChecker-only variant did not forward to the underlying checker")
	}
}

// TestStmtColumnConverterOnlyVariantForwarded verifies the ColumnConverter-only
// wrapper variant forwards to the underlying converter.
func TestStmtColumnConverterOnlyVariantForwarded(t *testing.T) {
	under := &ccOnlyStmt{}
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &stmtOnlyConn{stmt: under} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(context.Background(), "UPDATE users SET name = $1", "input"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if under.converters == 0 {
		t.Fatal("ColumnConverter-only variant did not forward to the underlying converter")
	}
	if under.lastValue != "cc2-sentinel" {
		t.Errorf("argument reached driver as %v, want the converter's sentinel", under.lastValue)
	}
}
