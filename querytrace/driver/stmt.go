package driver

import (
	"context"
	stddriver "database/sql/driver"

	qt "github.com/aita/sqlkit/querytrace"
)

// stmt wraps a prepared driver.Stmt, remembering the SQL it was prepared from so
// each execution can be recorded with its statement text, and the transaction it
// was prepared under (if any) so its executions link to that transaction.
//
// The base stmt deliberately implements NEITHER driver.NamedValueChecker NOR
// driver.ColumnConverter. Those are optional, capability-advertising interfaces:
// database/sql discovers them by a static type assertion on the statement object
// it holds (the wrapper), so a wrapper that advertised one the underlying lacks —
// or hid one the underlying has — would change argument conversion. In
// particular, advertising NamedValueChecker suppresses the underlying's
// ColumnConverter entirely (see database/sql's driverArgsConnLocked: a present
// NamedValueChecker is consulted first, and a ColumnConverter only as its
// fall-through and only when the wrapper also advertises it). To preserve
// fidelity we use conditional composition: newStmt returns a variant advertising
// exactly the subset of {NamedValueChecker, ColumnConverter} the underlying
// statement implements — never more, never less.
type stmt struct {
	cfg  qt.Config
	txID string
	sql  string
	s    stddriver.Stmt
}

var (
	_ stddriver.Stmt             = (*stmt)(nil)
	_ stddriver.StmtExecContext  = (*stmt)(nil)
	_ stddriver.StmtQueryContext = (*stmt)(nil)
)

// newStmt wraps an underlying statement, selecting a variant that advertises the
// same optional argument-conversion interfaces (NamedValueChecker,
// ColumnConverter) the underlying statement implements — and only those.
func newStmt(cfg qt.Config, txID, sql string, s stddriver.Stmt) stddriver.Stmt {
	base := stmt{cfg: cfg, txID: txID, sql: sql, s: s}
	_, nvc := s.(stddriver.NamedValueChecker)
	_, cc := s.(stddriver.ColumnConverter) //nolint:staticcheck // mirroring the underlying
	switch {
	case nvc && cc:
		return &stmtNamedValueCheckerColumnConverter{base}
	case nvc:
		return &stmtNamedValueChecker{base}
	case cc:
		return &stmtColumnConverter{base}
	default:
		return &base
	}
}

// Close closes the underlying statement.
func (st *stmt) Close() error { return st.s.Close() }

// NumInput reports the underlying statement's placeholder count.
func (st *stmt) NumInput() int { return st.s.NumInput() }

// Exec runs the statement without a context; it cannot be tied to a Trace, so it
// forwards unchanged. database/sql uses ExecContext when available.
func (st *stmt) Exec(args []stddriver.Value) (stddriver.Result, error) {
	return st.s.Exec(args) //nolint:staticcheck // required by driver.Stmt
}

// Query runs the statement without a context; see Exec.
func (st *stmt) Query(args []stddriver.Value) (stddriver.Rows, error) {
	return st.s.Query(args) //nolint:staticcheck // required by driver.Stmt
}

// ExecContext runs the prepared write, recording it.
func (st *stmt) ExecContext(ctx context.Context, args []stddriver.NamedValue) (stddriver.Result, error) {
	return instrumentExec(ctx, st.cfg, st.txID, st.sql, args, func() (stddriver.Result, error) {
		if ec, ok := st.s.(stddriver.StmtExecContext); ok {
			return ec.ExecContext(ctx, args)
		}
		return st.s.Exec(namedToValue(args)) //nolint:staticcheck // fallback
	})
}

// QueryContext runs the prepared read, recording it and wrapping the cursor.
func (st *stmt) QueryContext(ctx context.Context, args []stddriver.NamedValue) (stddriver.Rows, error) {
	return instrumentQuery(ctx, st.cfg, st.txID, st.sql, args, func() (stddriver.Rows, error) {
		if qc, ok := st.s.(stddriver.StmtQueryContext); ok {
			return qc.QueryContext(ctx, args)
		}
		return st.s.Query(namedToValue(args)) //nolint:staticcheck // fallback
	})
}

// checkNamedValue forwards to the underlying statement's NamedValueChecker. It is
// only reachable from a variant selected because the underlying implements it.
func (st *stmt) checkNamedValue(nv *stddriver.NamedValue) error {
	return st.s.(stddriver.NamedValueChecker).CheckNamedValue(nv) //nolint:forcetypeassert // variant invariant
}

// columnConverter forwards to the underlying statement's ColumnConverter. It is
// only reachable from a variant selected because the underlying implements it.
func (st *stmt) columnConverter(idx int) stddriver.ValueConverter {
	//nolint:staticcheck,forcetypeassert // mirroring the underlying; variant invariant
	return st.s.(stddriver.ColumnConverter).ColumnConverter(idx)
}

// stmtNamedValueChecker advertises NamedValueChecker only.
type stmtNamedValueChecker struct{ stmt }

var _ stddriver.NamedValueChecker = (*stmtNamedValueChecker)(nil)

func (st *stmtNamedValueChecker) CheckNamedValue(nv *stddriver.NamedValue) error {
	return st.checkNamedValue(nv)
}

// stmtColumnConverter advertises ColumnConverter only.
type stmtColumnConverter struct{ stmt }

var _ stddriver.ColumnConverter = (*stmtColumnConverter)(nil) //nolint:staticcheck // mirroring the underlying

func (st *stmtColumnConverter) ColumnConverter(idx int) stddriver.ValueConverter {
	return st.columnConverter(idx)
}

// stmtNamedValueCheckerColumnConverter advertises both.
type stmtNamedValueCheckerColumnConverter struct{ stmt }

var (
	_ stddriver.NamedValueChecker = (*stmtNamedValueCheckerColumnConverter)(nil)
	_ stddriver.ColumnConverter   = (*stmtNamedValueCheckerColumnConverter)(nil) //nolint:staticcheck // mirroring the underlying
)

func (st *stmtNamedValueCheckerColumnConverter) CheckNamedValue(nv *stddriver.NamedValue) error {
	return st.checkNamedValue(nv)
}

func (st *stmtNamedValueCheckerColumnConverter) ColumnConverter(idx int) stddriver.ValueConverter {
	return st.columnConverter(idx)
}
