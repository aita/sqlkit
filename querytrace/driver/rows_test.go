package driver_test

import (
	"context"
	stdsql "database/sql"
	stddriver "database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	qtdriver "github.com/aita/sqlkit/querytrace/driver"
)

// richRows implements the full set of optional column-type interfaces plus
// multi-result-set support, so the wrapper's delegating methods are exercised.
type richRows struct {
	resultSets int // number of further result sets still available
	nextErr    error
}

var (
	_ stddriver.Rows                           = (*richRows)(nil)
	_ stddriver.RowsNextResultSet              = (*richRows)(nil)
	_ stddriver.RowsColumnTypeScanType         = (*richRows)(nil)
	_ stddriver.RowsColumnTypeDatabaseTypeName = (*richRows)(nil)
	_ stddriver.RowsColumnTypeNullable         = (*richRows)(nil)
	_ stddriver.RowsColumnTypeLength           = (*richRows)(nil)
	_ stddriver.RowsColumnTypePrecisionScale   = (*richRows)(nil)
)

func (r *richRows) Columns() []string            { return []string{"id"} }
func (r *richRows) Close() error                 { return nil }
func (r *richRows) Next([]stddriver.Value) error { return io.EOF }
func (r *richRows) HasNextResultSet() bool       { return r.resultSets > 0 }
func (r *richRows) NextResultSet() error {
	if r.resultSets <= 0 {
		return io.EOF
	}
	r.resultSets--
	return r.nextErr
}
func (r *richRows) ColumnTypeScanType(int) reflect.Type {
	return reflect.TypeOf(int64(0))
}
func (r *richRows) ColumnTypeDatabaseTypeName(int) string { return "BIGINT" }
func (r *richRows) ColumnTypeNullable(int) (bool, bool)   { return true, true }
func (r *richRows) ColumnTypeLength(int) (int64, bool)    { return 42, true }
func (r *richRows) ColumnTypePrecisionScale(int) (int64, int64, bool) {
	return 10, 2, true
}

// richConn hands out richRows from QueryContext.
type richConn struct {
	rows func() stddriver.Rows
}

var (
	_ stddriver.Conn           = (*richConn)(nil)
	_ stddriver.QueryerContext = (*richConn)(nil)
)

func (c *richConn) QueryContext(context.Context, string, []stddriver.NamedValue) (stddriver.Rows, error) {
	return c.rows(), nil
}
func (c *richConn) Prepare(string) (stddriver.Stmt, error) { return &fakeStmt{rowCount: 0}, nil }
func (c *richConn) Close() error                           { return nil }
func (c *richConn) Begin() (stddriver.Tx, error)           { return fakeTx{}, nil }

func richDB(t *testing.T, mk func() stddriver.Rows) *stdsql.DB {
	t.Helper()
	db := stdsql.OpenDB(qtdriver.WrapConnector(connFactory(func() stddriver.Conn { return &richConn{rows: mk} }), testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestRowsColumnTypesDelegated verifies the wrapper forwards every optional
// column-type interface to an underlying cursor that implements them, so
// wrapping does not flatten the type metadata database/sql surfaces.
func TestRowsColumnTypesDelegated(t *testing.T) {
	db := richDB(t, func() stddriver.Rows { return &richRows{} })
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort

	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	if len(cts) != 1 {
		t.Fatalf("got %d column types, want 1", len(cts))
	}
	ct := cts[0]

	if got := ct.DatabaseTypeName(); got != "BIGINT" {
		t.Errorf("DatabaseTypeName = %q, want BIGINT", got)
	}
	if got := ct.ScanType(); got != reflect.TypeOf(int64(0)) {
		t.Errorf("ScanType = %v, want int64", got)
	}
	if nullable, ok := ct.Nullable(); !ok || !nullable {
		t.Errorf("Nullable = (%v,%v), want (true,true)", nullable, ok)
	}
	if length, ok := ct.Length(); !ok || length != 42 {
		t.Errorf("Length = (%v,%v), want (42,true)", length, ok)
	}
	if prec, scale, ok := ct.DecimalSize(); !ok || prec != 10 || scale != 2 {
		t.Errorf("DecimalSize = (%v,%v,%v), want (10,2,true)", prec, scale, ok)
	}
}

// TestRowsColumnTypesDefaultsWhenUnsupported verifies that wrapping a bare cursor
// (the fakeRows, which implements none of the optional interfaces) yields
// database/sql's documented defaults rather than panicking: the empty-interface
// scan type and unset metadata.
func TestRowsColumnTypesDefaultsWhenUnsupported(t *testing.T) {
	db := richDB(t, func() stddriver.Rows { return &fakeRows{remaining: 0} })
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort

	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	if len(cts) != 1 {
		t.Fatalf("got %d column types, want 1", len(cts))
	}
	ct := cts[0]

	if got := ct.ScanType(); got != reflect.TypeOf((*any)(nil)).Elem() {
		t.Errorf("ScanType = %v, want interface{}", got)
	}
	if got := ct.DatabaseTypeName(); got != "" {
		t.Errorf("DatabaseTypeName = %q, want empty", got)
	}
	if _, ok := ct.Nullable(); ok {
		t.Error("Nullable ok = true, want false for an unsupported cursor")
	}
	if _, ok := ct.Length(); ok {
		t.Error("Length ok = true, want false for an unsupported cursor")
	}
	if _, _, ok := ct.DecimalSize(); ok {
		t.Error("DecimalSize ok = true, want false for an unsupported cursor")
	}
}

// TestRowsNextResultSetDelegated verifies HasNextResultSet/NextResultSet forward
// to an underlying cursor that supports multiple result sets.
func TestRowsNextResultSetDelegated(t *testing.T) {
	db := richDB(t, func() stddriver.Rows { return &richRows{resultSets: 1} })
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort

	// First result set drains to EOF, then a second one is available.
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
	}
	if !rows.NextResultSet() {
		t.Fatalf("expected a second result set: %v", rows.Err())
	}
	// No third set: HasNextResultSet now false.
	if rows.NextResultSet() {
		t.Error("reported a third result set, want only two")
	}
}

// TestRowsNextResultSetErrorDelegated verifies a NextResultSet error from the
// underlying cursor propagates through the wrapper.
func TestRowsNextResultSetErrorDelegated(t *testing.T) {
	wantErr := errors.New("next result set boom")
	db := richDB(t, func() stddriver.Rows { return &richRows{resultSets: 1, nextErr: wantErr} })
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort

	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
	}
	if rows.NextResultSet() {
		t.Fatal("NextResultSet reported a usable set despite an error")
	}
	if !errors.Is(rows.Err(), wantErr) {
		t.Errorf("rows.Err() = %v, want %v", rows.Err(), wantErr)
	}
}

// TestRowsNextResultSetDefaultsWhenUnsupported verifies that a bare cursor
// reports no further result sets (HasNextResultSet false) so database/sql sees
// the documented single-result-set default.
func TestRowsNextResultSetDefaultsWhenUnsupported(t *testing.T) {
	db := richDB(t, func() stddriver.Rows { return &fakeRows{remaining: 0} })
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort

	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
	}
	if rows.NextResultSet() {
		t.Error("a bare cursor should report no further result sets")
	}
}

// TestRowsNextErrorRecordedAsIterErr verifies that a non-EOF error ending
// iteration is captured as RowsInfo.IterErr (distinct from the cancellation
// path covered elsewhere).
func TestRowsNextErrorRecordedAsIterErr(t *testing.T) {
	wantErr := errors.New("scan stream broke")
	db := richDB(t, func() stddriver.Rows { return &errOnNextRows{err: wantErr} })
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	rows, err := db.QueryContext(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
	}
	_ = rows.Close()

	ev := trace.Events()[0]
	if ev.Rows == nil {
		t.Fatal("rows lifecycle not recorded")
	}
	if !errors.Is(ev.Rows.IterErr, wantErr) {
		t.Errorf("IterErr = %v, want %v", ev.Rows.IterErr, wantErr)
	}
}

// errOnNextRows returns a non-EOF error on the first Next, so the wrapper's
// default (error) branch records it.
type errOnNextRows struct{ err error }

func (r *errOnNextRows) Columns() []string            { return []string{"id"} }
func (r *errOnNextRows) Close() error                 { return nil }
func (r *errOnNextRows) Next([]stddriver.Value) error { return r.err }
