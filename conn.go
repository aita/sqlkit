package sqlkit

import (
	"context"
	stdsql "database/sql"
)

// OnConn returns a copy of d whose statements and sessions all run on conn, a
// single connection pinned from d's pool with (*sql.DB).Conn, while keeping d's
// dialect, hooks, metadata, and splitting options. Sessions opened from the
// copy begin their transactions on conn, so work that must share one physical
// connection — a session-level advisory lock held across several transactions,
// SET-scoped session state — can still use the full builder and session API.
//
// The copy does not own conn: closing it remains the caller's job, and the copy
// must not be used after conn is closed. Its SQL method returns nil, since it is
// not backed by a pool. Statements run on conn one at a time, so do not use the
// copy from several goroutines at once.
func (d *Database) OnConn(conn *stdsql.Conn) *Database {
	cp := *d
	cp.db = nil
	cp.exec = connExecutor{conn}
	return &cp
}

// connExecutor runs statements on one pinned *sql.Conn and begins transactions
// on it, so a database bound with OnConn keeps sessions transactional.
type connExecutor struct{ conn *stdsql.Conn }

func (e connExecutor) Query(ctx context.Context, stmt Statement) (*stdsql.Rows, error) {
	return e.conn.QueryContext(ctx, stmt.SQL, stmt.Args...)
}

func (e connExecutor) Exec(ctx context.Context, stmt Statement) (stdsql.Result, error) {
	return e.conn.ExecContext(ctx, stmt.SQL, stmt.Args...)
}

// Begin starts a database/sql transaction on the pinned connection.
func (e connExecutor) Begin(ctx context.Context, opts *stdsql.TxOptions) (Tx, error) {
	tx, err := e.conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return txExecutor{tx}, nil
}
