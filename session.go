package sqlkit

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"sync"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// Session is a transaction-scoped database handle. The transaction begins
// lazily: nothing is opened until the first statement runs through the
// session, so a session that executes nothing costs nothing and its Commit
// and Close are no-ops. Once a statement has run, finish with Commit or
// Rollback (Close rolls back if neither happened).
//
// Session implements Querier, so query chains start from it and run inside
// its transaction: session.Select().From(t).All(ctx).Scan(&dst).
//
// The transaction comes from the database's terminal Executor: when it
// implements TxExecutor (the default database/sql handle, or a WrapExecutor
// terminal that opts in) the session runs inside a real transaction; otherwise
// statements run directly on the terminal and Commit/Rollback are no-ops.
type Session struct {
	// base is the database's terminal Executor, the source of the session's
	// transaction (when it implements TxExecutor) and the fallback it runs on
	// otherwise.
	base         Executor
	txOpts       *stdsql.TxOptions
	dialect      sql.Dialect
	autoChunk    bool
	preloadChunk int

	// hks are the lifecycle hooks inherited from the Database (see WithHooks),
	// run around each statement executed through this session.
	hks []Hooks

	// identities is inherited from the Database (see WithMetadata): a uow.Scope
	// opened over this session validates its bindings against it automatically.
	identities *meta.IdentityMap

	mu     sync.Mutex
	tx     Tx
	closed bool
	spSeq  int
}

// activeExecutor returns the Executor the session's statements run through: its
// transaction, begun lazily on first use when base is a TxExecutor, or base
// itself when base cannot transact (then Commit/Rollback are no-ops).
func (s *Session) activeExecutor(ctx context.Context) (Executor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, stdsql.ErrTxDone
	}
	if s.tx != nil {
		return s.tx, nil
	}
	beginner, ok := s.base.(TxExecutor)
	if !ok {
		return s.base, nil
	}
	tx, err := beginner.Begin(ctx, s.txOpts)
	if err != nil {
		return nil, err
	}
	s.tx = tx
	return tx, nil
}

// Commit commits the transaction. If no transaction was begun (no statement
// ran, or the terminal cannot transact), Commit just closes the session.
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

// Rollback rolls back the transaction. If no transaction was begun, it just
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

// Union combines two or more SELECT queries with UNION, bound to this
// session's transaction. It mirrors the package-level Union but binds the
// result to this session regardless of the operands' own executors.
func (s *Session) Union(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := Union(first, second, rest...)
	q.ex = s
	return q
}

// UnionAll is Union with UNION ALL, keeping duplicate rows.
func (s *Session) UnionAll(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := UnionAll(first, second, rest...)
	q.ex = s
	return q
}

// Intersect combines two or more SELECT queries with INTERSECT, bound to this
// session's transaction.
func (s *Session) Intersect(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := Intersect(first, second, rest...)
	q.ex = s
	return q
}

// Except combines two or more SELECT queries with EXCEPT, bound to this
// session's transaction.
func (s *Session) Except(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := Except(first, second, rest...)
	q.ex = s
	return q
}

// Insert starts an INSERT bound to this session's transaction. Feed it rows
// with Values, or a query with FromSelect.
func (s *Session) Insert(table Table) InsertQuery {
	q := Insert(table)
	q.ex = s
	return q
}

// Update starts an UPDATE bound to this session's transaction.
func (s *Session) Update(table Table) UpdateQuery {
	q := Update(table)
	q.ex = s
	return q
}

// Delete starts a DELETE bound to this session's transaction.
func (s *Session) Delete(table Table) DeleteQuery {
	q := Delete(table)
	q.ex = s
	return q
}

// Merge starts a MERGE bound to this session's transaction.
func (s *Session) Merge(target Table) MergeQuery {
	q := Merge(target)
	q.ex = s
	return q
}

// RawSQL prepares a hand-written SQL string (with args) or a built sql.Query
// for execution in this session's transaction; materialize it with Scan.
func (s *Session) RawSQL(ctx context.Context, query any, args ...any) Rows {
	return newRawRows(ctx, s, query, args)
}

// Exec compiles and runs a statement through the session's transaction. It
// accepts any sql.Query, including the DDL statements built with the sql package
// (sql.CreateTable, sql.AlterTable, ...). A statement that renders empty for the
// dialect is a no-op and returns a nil result.
func (s *Session) Exec(ctx context.Context, query sql.Query) (stdsql.Result, error) {
	sqlText, args, err := query.ToSQL(s.dialect)
	if err != nil {
		return nil, err
	}
	if sqlText == "" {
		return nil, nil
	}
	return runExecStmt(ctx, s, Statement{
		Op:      OpExec,
		Query:   query,
		SQL:     sqlText,
		Args:    args,
		Dialect: s.dialect,
	})
}

// Dialect returns the session's SQL dialect. It mirrors Database.Dialect and
// lets layers above sqlkit (such as the uow package) branch on dialect
// capabilities like RETURNING support without reaching into the executor.
func (s *Session) Dialect() sql.Dialect { return s.dialect }

// Identities returns the catalog identity map inherited from the database that
// opened this session (see WithMetadata), or nil if none was bound. A uow.Scope
// opened over this session reads it to validate table bindings automatically.
func (s *Session) Identities() *meta.IdentityMap { return s.identities }

// Savepoint opens a savepoint in the session's transaction and returns its
// generated name. The transaction begins on first use, so a savepoint may be
// the first statement. Undo work back to it with RollbackTo, or discard the
// savepoint with Release; the outer Commit releases any that remain.
func (s *Session) Savepoint(ctx context.Context) (string, error) {
	s.mu.Lock()
	s.spSeq++
	name := fmt.Sprintf("sp_%d", s.spSeq)
	s.mu.Unlock()
	if _, err := s.txControl(ctx, "SAVEPOINT "+name); err != nil {
		return "", err
	}
	return name, nil
}

// RollbackTo rolls the transaction back to the named savepoint, undoing every
// statement since it was opened while leaving the savepoint (and the outer
// transaction) in place.
func (s *Session) RollbackTo(ctx context.Context, name string) error {
	_, err := s.txControl(ctx, "ROLLBACK TO SAVEPOINT "+name)
	return err
}

// Release discards the named savepoint, merging its work into the enclosing
// transaction or savepoint.
func (s *Session) Release(ctx context.Context, name string) error {
	_, err := s.txControl(ctx, "RELEASE SAVEPOINT "+name)
	return err
}

// txControl runs a transaction-control statement (a savepoint) directly on the
// session's transaction, bypassing hooks. It requires a real transaction: a
// session whose terminal is not a TxExecutor has none, so it returns errNoTx
// rather than sending the SQL to a non-transactional executor.
func (s *Session) txControl(ctx context.Context, query string) (stdsql.Result, error) {
	if _, ok := s.base.(TxExecutor); !ok {
		return nil, errNoTx
	}
	ex, err := s.activeExecutor(ctx)
	if err != nil {
		return nil, err
	}
	return ex.Exec(ctx, Statement{Op: OpExec, SQL: query, Dialect: s.dialect})
}

func (s *Session) sqlDialect() sql.Dialect { return s.dialect }

func (s *Session) hooks() []Hooks { return s.hks }

// maxBindParams is the single splitting chokepoint: the dialect's bind-parameter
// limit when auto-chunk is on for this session, or 0 when it is off.
func (s *Session) maxBindParams() int {
	if !s.autoChunk {
		return 0
	}
	return resolveBindParamLimit(s.dialect)
}

// AutoChunkEnabled reports whether automatic, limit-driven statement splitting is
// enabled for this session (WithAutoChunk / WithSessionAutoChunk).
func (s *Session) AutoChunkEnabled() bool { return s.autoChunk }

func (s *Session) preloadChunkSize() int { return s.preloadChunk }

func (s *Session) executor() Executor { return sessionExecutor{s} }

// sessionExecutor is the terminal Executor for a Session: it routes each
// statement to the session's active executor (its transaction, or the bare
// terminal when none backs it), beginning the transaction on first use.
type sessionExecutor struct{ s *Session }

func (e sessionExecutor) Query(ctx context.Context, stmt Statement) (*stdsql.Rows, error) {
	ex, err := e.s.activeExecutor(ctx)
	if err != nil {
		return nil, err
	}
	return ex.Query(ctx, stmt)
}

func (e sessionExecutor) Exec(ctx context.Context, stmt Statement) (stdsql.Result, error) {
	ex, err := e.s.activeExecutor(ctx)
	if err != nil {
		return nil, err
	}
	return ex.Exec(ctx, stmt)
}
