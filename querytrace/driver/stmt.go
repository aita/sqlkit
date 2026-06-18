package driver

import (
	"context"
	stddriver "database/sql/driver"

	qt "github.com/aita/sqlkit/querytrace"
)

// stmt wraps a prepared driver.Stmt, remembering the SQL it was prepared from so
// each execution can be recorded with its statement text.
type stmt struct {
	cfg qt.Config
	sql string
	s   stddriver.Stmt
}

var (
	_ stddriver.Stmt              = (*stmt)(nil)
	_ stddriver.StmtExecContext   = (*stmt)(nil)
	_ stddriver.StmtQueryContext  = (*stmt)(nil)
	_ stddriver.NamedValueChecker = (*stmt)(nil)
)

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
	return instrumentExec(ctx, st.cfg, st.sql, args, func() (stddriver.Result, error) {
		if ec, ok := st.s.(stddriver.StmtExecContext); ok {
			return ec.ExecContext(ctx, args)
		}
		return st.s.Exec(namedToValue(args)) //nolint:staticcheck // fallback
	})
}

// QueryContext runs the prepared read, recording it and wrapping the cursor.
func (st *stmt) QueryContext(ctx context.Context, args []stddriver.NamedValue) (stddriver.Rows, error) {
	return instrumentQuery(ctx, st.cfg, st.sql, args, func() (stddriver.Rows, error) {
		if qc, ok := st.s.(stddriver.StmtQueryContext); ok {
			return qc.QueryContext(ctx, args)
		}
		return st.s.Query(namedToValue(args)) //nolint:staticcheck // fallback
	})
}

// CheckNamedValue forwards to the underlying statement's NamedValueChecker, or
// returns driver.ErrSkip for the default conversion.
func (st *stmt) CheckNamedValue(nv *stddriver.NamedValue) error {
	if c, ok := st.s.(stddriver.NamedValueChecker); ok {
		return c.CheckNamedValue(nv)
	}
	return stddriver.ErrSkip
}
