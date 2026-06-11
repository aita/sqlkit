package sqlkit

import (
	"context"
	stdsql "database/sql"

	"github.com/sqlkit-dev/sqlkit/sqlkit/rowset"
	querysql "github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

// Database is the runtime handle used by generated sqlkit accessors.
type Database struct {
	db      *stdsql.DB
	dialect querysql.Dialect
}

// Open opens a database/sql handle from a driver name and DSN string.
func Open(driverName, dsn string) (*Database, error) {
	db, err := stdsql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	return WrapDB(db), nil
}

// WrapDB creates a sqlkit database handle from an existing database/sql handle.
func WrapDB(db *stdsql.DB) *Database { return &Database{db: db, dialect: querysql.PostgreSQLDialect{}} }

// SQL returns the wrapped database/sql handle.
func (d *Database) SQL() *stdsql.DB { return d.db }

// NewSession starts a context-bound unit of work backed by a transaction.
func (d *Database) NewSession(ctx context.Context) (*Session, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Session{tx: tx, dialect: d.dialect}, nil
}

// Session represents a transactional unit of work for generated code.
type Session struct {
	tx      *stdsql.Tx
	dialect querysql.Dialect
	closed  bool
}

// Commit commits the unit of work.
func (s *Session) Commit() error {
	if s.closed {
		return stdsql.ErrTxDone
	}
	s.closed = true
	return s.tx.Commit()
}

// Rollback rolls back the unit of work.
func (s *Session) Rollback() error {
	if s.closed {
		return stdsql.ErrTxDone
	}
	s.closed = true
	return s.tx.Rollback()
}

// Close rolls back the unit of work if it has not already been committed.
func (s *Session) Close() error {
	if s.closed {
		return nil
	}
	return s.Rollback()
}

// Exec compiles and runs a statement through the session's transaction using ctx.
func (s *Session) Exec(ctx context.Context, query querysql.Query) (stdsql.Result, error) {
	sqlText, args, err := query.ToSQL(s.dialect)
	if err != nil {
		return nil, err
	}
	return s.tx.ExecContext(ctx, sqlText, args...)
}

// Query compiles and prepares a query object that can be materialized using ctx.
func (s *Session) Query(ctx context.Context, query querysql.Query) QueryObject {
	return QueryObject{ctx: ctx, tx: s.tx, dialect: s.dialect, query: query}
}

// QueryObject is a pending session query that can be materialized as rows.
type QueryObject struct {
	ctx     context.Context
	tx      *stdsql.Tx
	dialect querysql.Dialect
	query   querysql.Query
}

// All runs the query and returns all rows as a RowSet.
func (q QueryObject) All() (rowset.RowSet, error) {
	sqlText, args, err := q.query.ToSQL(q.dialect)
	if err != nil {
		return rowset.RowSet{}, err
	}
	rows, err := q.tx.QueryContext(q.ctx, sqlText, args...)
	if err != nil {
		return rowset.RowSet{}, err
	}
	return rowset.FromSQLRows(rows), nil
}
