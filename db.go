package sqlkit

import (
	"context"
	stdsql "database/sql"
	"errors"

	"github.com/aita/sqlkit/sql"
)

// Database is the entry point of sqlkit: it wraps a database/sql handle, runs
// statements in auto-commit mode, and opens transactional sessions.
type Database struct {
	db      *stdsql.DB
	dialect sql.Dialect
}

// DatabaseOption configures a Database.
type DatabaseOption func(*Database)

// WithDialect overrides the SQL dialect (PostgreSQL by default).
func WithDialect(dialect sql.Dialect) DatabaseOption {
	return func(d *Database) { d.dialect = dialect }
}

// Open opens a database/sql handle from a driver name and DSN string. The
// dialect is inferred from the driver name (see DialectForDriver); pass
// WithDialect to override.
func Open(driverName, dsn string, opts ...DatabaseOption) (*Database, error) {
	db, err := stdsql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	return WrapDB(db, append([]DatabaseOption{WithDialect(DialectForDriver(driverName))}, opts...)...), nil
}

// WrapDB creates a sqlkit database handle from an existing database/sql handle.
func WrapDB(db *stdsql.DB, opts ...DatabaseOption) *Database {
	d := &Database{db: db, dialect: sql.PostgreSQLDialect{}}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// SQL returns the wrapped database/sql handle.
func (d *Database) SQL() *stdsql.DB { return d.db }

// Dialect returns the configured SQL dialect.
func (d *Database) Dialect() sql.Dialect { return d.dialect }

// Select starts a typed SELECT builder executed against this database. With
// no arguments it selects every column of the From source.
func (d *Database) Select(selections ...sql.Selection) SelectQuery {
	q := Select(selections...)
	q.ex = d
	return q
}

// Union combines two or more SELECT queries with UNION, executed against this
// database. It mirrors the package-level Union but binds the result to this
// database regardless of the operands' own executors.
func (d *Database) Union(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := Union(first, second, rest...)
	q.ex = d
	return q
}

// UnionAll is Union with UNION ALL, keeping duplicate rows.
func (d *Database) UnionAll(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := UnionAll(first, second, rest...)
	q.ex = d
	return q
}

// Intersect combines two or more SELECT queries with INTERSECT, executed
// against this database.
func (d *Database) Intersect(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := Intersect(first, second, rest...)
	q.ex = d
	return q
}

// Except combines two or more SELECT queries with EXCEPT, executed against
// this database.
func (d *Database) Except(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	q := Except(first, second, rest...)
	q.ex = d
	return q
}

// Insert starts an INSERT builder for a table, mirroring Select. Feed it
// rows with Values, or a query with FromSelect.
func (d *Database) Insert(table Table) InsertQuery {
	q := Insert(table)
	q.ex = d
	return q
}

// Update starts an UPDATE builder for a table, mirroring Select.
func (d *Database) Update(table Table) UpdateQuery {
	q := Update(table)
	q.ex = d
	return q
}

// Delete starts a DELETE builder for a table, mirroring Select.
func (d *Database) Delete(table Table) DeleteQuery {
	q := Delete(table)
	q.ex = d
	return q
}

// RawSQL prepares a hand-written SQL string (with args) or a built sql.Query
// for execution against this database; materialize it with Scan.
func (d *Database) RawSQL(ctx context.Context, query any, args ...any) Rows {
	return newRawRows(ctx, d, query, args)
}

// NewSession creates a transaction-scoped session. No transaction is opened
// yet — it begins lazily when the first statement runs through the session —
// so creating one is free and there is no error to handle here.
func (d *Database) NewSession(opts ...SessionOption) *Session {
	options := sessionOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	return &Session{db: d.db, txOpts: options.tx, dialect: d.dialect}
}

// WithSession runs fn inside a session and commits when fn returns nil; on
// error or panic it rolls back and the error (or panic) propagates. Thanks to
// lazy transaction begin, a callback that runs no statements costs nothing.
//
//	err := db.WithSession(func(s *sqlkit.Session) error {
//		if _, err := s.Insert(Users).Values(u).Exec(ctx); err != nil {
//			return err
//		}
//		_, err := s.Update(Posts).Set(...).Where(...).Exec(ctx)
//		return err
//	})
func (d *Database) WithSession(fn func(s *Session) error, opts ...SessionOption) error {
	session := d.NewSession(opts...)
	defer func() {
		if p := recover(); p != nil {
			_ = session.Rollback()
			panic(p)
		}
	}()
	if err := fn(session); err != nil {
		if rollbackErr := session.Rollback(); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	return session.Commit()
}

// SessionOption configures a new session.
type SessionOption func(*sessionOptions)

type sessionOptions struct {
	tx *stdsql.TxOptions
}

// WithTxOptions sets transaction options for the session.
func WithTxOptions(options TxOptions) SessionOption {
	return func(o *sessionOptions) { o.tx = options.ToSQL() }
}

// Exec compiles and runs a statement outside a session (auto-commit). It
// accepts any sql.Query, including the DDL statements built with the sql package
// (sql.CreateTable, sql.AlterTable, ...), since those are Query values too. A
// statement that renders empty for the dialect (e.g. a standalone comment on a
// dialect that comments inline) is a no-op and returns a nil result.
func (d *Database) Exec(ctx context.Context, query sql.Query) (stdsql.Result, error) {
	sqlText, args, err := query.ToSQL(d.dialect)
	if err != nil {
		return nil, err
	}
	if sqlText == "" {
		return nil, nil
	}
	return d.db.ExecContext(ctx, sqlText, args...)
}

func (d *Database) sqlDialect() sql.Dialect { return d.dialect }

func (d *Database) queryContext(ctx context.Context, query string, args ...any) (*stdsql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

func (d *Database) execContext(ctx context.Context, query string, args ...any) (stdsql.Result, error) {
	return d.db.ExecContext(ctx, query, args...)
}
