package driver

import (
	"context"
	stddriver "database/sql/driver"

	qt "github.com/aita/sqlkit/querytrace"
)

// conn wraps a driver.Conn. It implements the context-aware execution
// interfaces and forwards to the underlying connection's, returning
// driver.ErrSkip when the underlying connection lacks one so database/sql falls
// back to the prepare path (which the wrapped statement still instruments).
//
// NamedValueChecker is handled by conditional composition (see newConn): like a
// statement's, database/sql discovers a connection's NamedValueChecker by a
// static type assertion, so the wrapper must advertise it exactly when the
// underlying does. SessionResetter and Validator, by contrast, are forwarded
// unconditionally: their absent-default behavior (ResetSession → nil, IsValid →
// true) is precisely what database/sql assumes when a connection does not
// implement them, so advertising them on a non-supporting underlying is a no-op
// and keeps the wrapper simple while never hiding an underlying that does
// implement them.
type conn struct {
	cfg qt.Config
	c   stddriver.Conn

	// txID is the identifier of the transaction currently open on this
	// connection, or "" outside one. database/sql holds a driver connection
	// exclusively for the life of a transaction and serializes all calls on it,
	// so a plain field — set by BeginTx, cleared by the wrapped Tx on
	// commit/rollback, and read when recording a statement — links each statement
	// to its transaction without a lock.
	txID string
}

var (
	_ stddriver.Conn               = (*conn)(nil)
	_ stddriver.QueryerContext     = (*conn)(nil)
	_ stddriver.ExecerContext      = (*conn)(nil)
	_ stddriver.ConnPrepareContext = (*conn)(nil)
	_ stddriver.ConnBeginTx        = (*conn)(nil)
	_ stddriver.Pinger             = (*conn)(nil)
	_ stddriver.SessionResetter    = (*conn)(nil)
	_ stddriver.Validator          = (*conn)(nil)
)

// newConn wraps an underlying connection, selecting a variant that advertises
// driver.NamedValueChecker exactly when the underlying connection implements it
// — so the wrapper neither hides the underlying's per-connection argument
// checking nor advertises a capability it lacks.
func newConn(cfg qt.Config, c stddriver.Conn) stddriver.Conn {
	base := conn{cfg: cfg, c: c}
	if _, ok := c.(stddriver.NamedValueChecker); ok {
		return &connNamedValueChecker{base}
	}
	return &base
}

// Prepare prepares a statement, returning an instrumented wrapper that carries
// the SQL text for later recording.
func (cn *conn) Prepare(query string) (stddriver.Stmt, error) {
	s, err := cn.c.Prepare(query)
	if err != nil {
		return nil, err
	}
	return newStmt(cn.cfg, cn.txID, query, s), nil
}

// PrepareContext prepares a statement with a context when the underlying
// connection supports it.
func (cn *conn) PrepareContext(ctx context.Context, query string) (stddriver.Stmt, error) {
	if pc, ok := cn.c.(stddriver.ConnPrepareContext); ok {
		s, err := pc.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return newStmt(cn.cfg, cn.txID, query, s), nil
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
	return instrumentQuery(ctx, cn.cfg, cn.txID, query, args, func() (stddriver.Rows, error) {
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
	return instrumentExec(ctx, cn.cfg, cn.txID, query, args, func() (stddriver.Result, error) {
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
	if !ok || !cn.cfg.Enabled() || !tr.Sampled() {
		return underlying, nil
	}
	tr.EnsureConfig(cn.cfg)
	cn.txID = tr.RecordTxBegin()
	return &tx{tr: tr, cn: cn, txID: cn.txID, tx: underlying}, nil
}

// Ping forwards to the underlying Pinger, or reports healthy when the underlying
// connection is not a Pinger.
func (cn *conn) Ping(ctx context.Context) error {
	if p, ok := cn.c.(stddriver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// ResetSession forwards to the underlying SessionResetter so database/sql can
// reset per-connection session state (temp tables, SET settings, search_path)
// when a connection is returned to the pool. When the underlying connection does
// not implement it, returning nil matches database/sql's no-op default for a
// connection that lacks the interface, so unconditional advertisement is safe.
func (cn *conn) ResetSession(ctx context.Context) error {
	if r, ok := cn.c.(stddriver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

// IsValid forwards to the underlying Validator so database/sql can drop broken
// connections from the pool (and keep healthy connections across a
// context-canceled rollback). When the underlying connection does not implement
// it, reporting valid matches database/sql's default for a connection that lacks
// the interface, so unconditional advertisement is safe.
func (cn *conn) IsValid() bool {
	if v, ok := cn.c.(stddriver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// checkNamedValue forwards to the underlying connection's NamedValueChecker. It
// is only reachable from the connNamedValueChecker variant, selected because the
// underlying implements it.
func (cn *conn) checkNamedValue(nv *stddriver.NamedValue) error {
	return cn.c.(stddriver.NamedValueChecker).CheckNamedValue(nv) //nolint:forcetypeassert // variant invariant
}

// connNamedValueChecker advertises driver.NamedValueChecker, forwarding to an
// underlying connection that implements it.
type connNamedValueChecker struct{ conn }

var _ stddriver.NamedValueChecker = (*connNamedValueChecker)(nil)

func (cn *connNamedValueChecker) CheckNamedValue(nv *stddriver.NamedValue) error {
	return cn.checkNamedValue(nv)
}
