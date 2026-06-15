package driver

import (
	"context"
	stddriver "database/sql/driver"

	qt "github.com/aita/sqlkit/x/querytrace"
)

// conn wraps a driver.Conn. It implements the context-aware execution
// interfaces and forwards to the underlying connection's, returning
// driver.ErrSkip when the underlying connection lacks one so database/sql falls
// back to the prepare path (which the wrapped statement still instruments).
type conn struct {
	cfg qt.Config
	c   stddriver.Conn
}

var (
	_ stddriver.Conn               = (*conn)(nil)
	_ stddriver.QueryerContext     = (*conn)(nil)
	_ stddriver.ExecerContext      = (*conn)(nil)
	_ stddriver.ConnPrepareContext = (*conn)(nil)
	_ stddriver.ConnBeginTx        = (*conn)(nil)
	_ stddriver.Pinger             = (*conn)(nil)
	_ stddriver.NamedValueChecker  = (*conn)(nil)
)

// Prepare prepares a statement, returning an instrumented wrapper that carries
// the SQL text for later recording.
func (cn *conn) Prepare(query string) (stddriver.Stmt, error) {
	s, err := cn.c.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &stmt{cfg: cn.cfg, sql: query, s: s}, nil
}

// PrepareContext prepares a statement with a context when the underlying
// connection supports it.
func (cn *conn) PrepareContext(ctx context.Context, query string) (stddriver.Stmt, error) {
	if pc, ok := cn.c.(stddriver.ConnPrepareContext); ok {
		s, err := pc.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &stmt{cfg: cn.cfg, sql: query, s: s}, nil
	}
	return cn.Prepare(query)
}

// Close closes the underlying connection.
func (cn *conn) Close() error { return cn.c.Close() }

// Begin starts a transaction without a context; it cannot be tied to a Trace, so
// it forwards unchanged. database/sql uses BeginTx when available.
func (cn *conn) Begin() (stddriver.Tx, error) { return cn.c.Begin() } //nolint:staticcheck // required by driver.Conn

// QueryContext runs a read through the underlying QueryerContext, recording it.
// It returns driver.ErrSkip when the underlying connection is not a
// QueryerContext, so database/sql prepares and queries a statement instead.
func (cn *conn) QueryContext(ctx context.Context, query string, args []stddriver.NamedValue) (stddriver.Rows, error) {
	q, ok := cn.c.(stddriver.QueryerContext)
	if !ok {
		return nil, stddriver.ErrSkip
	}
	return instrumentQuery(ctx, cn.cfg, query, args, func() (stddriver.Rows, error) {
		return q.QueryContext(ctx, query, args)
	})
}

// ExecContext runs a write through the underlying ExecerContext, recording it.
// It returns driver.ErrSkip when the underlying connection is not an
// ExecerContext.
func (cn *conn) ExecContext(ctx context.Context, query string, args []stddriver.NamedValue) (stddriver.Result, error) {
	e, ok := cn.c.(stddriver.ExecerContext)
	if !ok {
		return nil, stddriver.ErrSkip
	}
	return instrumentExec(ctx, cn.cfg, query, args, func() (stddriver.Result, error) {
		return e.ExecContext(ctx, query, args)
	})
}

// BeginTx starts a transaction and records its begin against the context's
// Trace, returning a wrapped Tx that records its commit or rollback.
func (cn *conn) BeginTx(ctx context.Context, opts stddriver.TxOptions) (stddriver.Tx, error) {
	var (
		underlying stddriver.Tx
		err        error
	)
	if bt, ok := cn.c.(stddriver.ConnBeginTx); ok {
		underlying, err = bt.BeginTx(ctx, opts)
	} else {
		underlying, err = cn.c.Begin() //nolint:staticcheck // fallback for drivers without ConnBeginTx
	}
	if err != nil {
		return nil, err
	}
	tr, ok := qt.FromContext(ctx)
	if !ok || !cn.cfg.Enabled {
		return underlying, nil
	}
	tr.EnsureConfig(cn.cfg)
	return &tx{tr: tr, txID: tr.RecordTxBegin(), tx: underlying}, nil
}

// Ping forwards to the underlying Pinger, or reports healthy when the underlying
// connection is not a Pinger.
func (cn *conn) Ping(ctx context.Context) error {
	if p, ok := cn.c.(stddriver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// CheckNamedValue forwards to the underlying NamedValueChecker, or returns
// driver.ErrSkip so database/sql applies its default argument conversion.
func (cn *conn) CheckNamedValue(nv *stddriver.NamedValue) error {
	if c, ok := cn.c.(stddriver.NamedValueChecker); ok {
		return c.CheckNamedValue(nv)
	}
	return stddriver.ErrSkip
}
