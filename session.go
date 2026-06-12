package sqlkit

import (
	"context"
	stdsql "database/sql"
	"sync"

	"github.com/aita/sqlkit/sql"
)

// Session is a transaction-scoped database handle. The transaction begins
// lazily: nothing is opened until the first statement runs through the
// session, so a session that executes nothing costs nothing and its Commit
// and Close are no-ops. Once a statement has run, finish with Commit or
// Rollback (Close rolls back if neither happened).
//
// Session implements Executor, so query chains start from it and run inside
// its transaction: session.Select().From(t).All(ctx).Scan(&dst).
type Session struct {
	db      *stdsql.DB
	txOpts  *stdsql.TxOptions
	dialect sql.Dialect

	mu     sync.Mutex
	tx     *stdsql.Tx
	closed bool
}

// ensureTx returns the session's transaction, beginning it on first use.
func (s *Session) ensureTx(ctx context.Context) (*stdsql.Tx, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, stdsql.ErrTxDone
	}
	if s.tx == nil {
		tx, err := s.db.BeginTx(ctx, s.txOpts)
		if err != nil {
			return nil, err
		}
		s.tx = tx
	}
	return s.tx, nil
}

// Commit commits the transaction. If no statement ever ran, no transaction
// was begun and Commit just closes the session.
func (s *Session) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return stdsql.ErrTxDone
	}
	s.closed = true
	if s.tx == nil {
		return nil
	}
	return s.tx.Commit()
}

// Rollback rolls back the transaction. If no statement ever ran, it just
// closes the session.
func (s *Session) Rollback() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return stdsql.ErrTxDone
	}
	s.closed = true
	if s.tx == nil {
		return nil
	}
	return s.tx.Rollback()
}

// Close rolls back the transaction if the session has not been committed. It
// is safe to defer unconditionally; on an untouched or committed session it
// does nothing.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.Rollback()
}

// Select starts a typed SELECT bound to this session's transaction. With no
// arguments it selects every column of the From source.
func (s *Session) Select(selections ...sql.Selection) SelectQuery {
	q := Select(selections...)
	q.ex = s
	return q
}

// Insert starts an INSERT bound to this session's transaction. Feed it rows
// with Values, or a query with FromSelect.
func (s *Session) Insert(table TableMeta) InsertQuery {
	q := Insert(table)
	q.ex = s
	return q
}

// Update starts an UPDATE bound to this session's transaction.
func (s *Session) Update(table TableMeta) UpdateQuery {
	q := Update(table)
	q.ex = s
	return q
}

// Delete starts a DELETE bound to this session's transaction.
func (s *Session) Delete(table TableMeta) DeleteQuery {
	q := Delete(table)
	q.ex = s
	return q
}

// RawSQL prepares a hand-written SQL string (with args) or a built sql.Query
// for execution in this session's transaction; materialize it with Scan.
func (s *Session) RawSQL(ctx context.Context, query any, args ...any) Rows {
	return newRawRows(ctx, s, query, args)
}

// Exec compiles and runs a statement through the session's transaction.
func (s *Session) Exec(ctx context.Context, query sql.Query) (stdsql.Result, error) {
	sqlText, args, err := query.ToSQL(s.dialect)
	if err != nil {
		return nil, err
	}
	return s.execContext(ctx, sqlText, args...)
}

func (s *Session) sqlDialect() sql.Dialect { return s.dialect }

func (s *Session) queryContext(ctx context.Context, query string, args ...any) (*stdsql.Rows, error) {
	tx, err := s.ensureTx(ctx)
	if err != nil {
		return nil, err
	}
	return tx.QueryContext(ctx, query, args...)
}

func (s *Session) execContext(ctx context.Context, query string, args ...any) (stdsql.Result, error) {
	tx, err := s.ensureTx(ctx)
	if err != nil {
		return nil, err
	}
	return tx.ExecContext(ctx, query, args...)
}
