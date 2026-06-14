package sqlkit

import (
	"context"
	stdsql "database/sql"
	"errors"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// Database is the entry point of sqlkit: it wraps a database/sql handle, runs
// statements in auto-commit mode, and opens transactional sessions.
type Database struct {
	db      *stdsql.DB
	dialect sql.Dialect

	// identities is the catalog identity map compiled from the metadata bound
	// with WithMetadata, or nil if none was bound. A uow.Scope opened over this
	// database (or a session derived from it) reads it to validate bindings
	// automatically. metaErr holds a metadata-compilation failure for Open to
	// surface.
	identities *meta.IdentityMap
	metaErr    error

	ics []Interceptor

	// exec is the terminal Executor a WrapExecutor database runs every statement
	// through, inherited by its sessions. It is nil for a WrapDB/Open database,
	// which runs against its database/sql handle instead (dbExecutor here,
	// sessionExecutor — the session's own transaction — in a Session).
	exec Executor

	// resultObs are the observers (WithResultObserver) run over each scanned
	// result, in registration order, and inherited by sessions.
	resultObs []ResultObserver

	// batchReadSnapshot runs a split SELECT's chunk queries inside one REPEATABLE
	// READ transaction so they observe a single consistent snapshot, as an
	// unsplit statement would. It defaults to true (active only when splitting is
	// enabled); WithBatchReadSnapshot(false) turns it off, running the chunks as
	// independent reads.
	batchReadSnapshot bool
	// batchWriteSnapshot raises the isolation of the transaction wrapping a split
	// INSERT/UPDATE/DELETE to REPEATABLE READ, so data the statements read (a
	// reference UPDATE's SET subquery or FROM source) is consistent across the
	// chunks. It defaults to false because it can surface serialization failures
	// the caller must retry; WithBatchWriteSnapshot(true) opts in.
	batchWriteSnapshot bool
	// autoChunk enables automatic, limit-driven statement splitting. It defaults
	// to false, so statements run unsplit unless WithAutoChunk(true) turns
	// splitting on, after which it is active at the dialect's bind-parameter limit
	// (the BindParamLimiter capability, 65535 for PostgreSQL/MySQL). Explicit
	// requests (AutoChunk, InBatches) are unaffected. A session inherits this and
	// may override it with WithSessionAutoChunk.
	autoChunk bool
	// preloadChunkSize caps the parent keys per follow-up IN query of a Preload, so
	// an eager-load over many parents runs several smaller, plan-cache-friendly
	// statements (like SQLAlchemy selectinload) rather than one huge IN — on top of
	// the dialect's bind-parameter limit. It defaults to 1000; 0 leaves only that
	// hard limit.
	preloadChunk int
}

// DatabaseOption configures a Database.
type DatabaseOption func(*Database)

// WithDialect overrides the SQL dialect (PostgreSQL by default).
func WithDialect(dialect sql.Dialect) DatabaseOption {
	return func(d *Database) { d.dialect = dialect }
}

// WithMetadata binds a catalog's Metadata to the database. The metadata is
// compiled into an identity map (see meta.Metadata.Identities) that every
// uow.Scope opened over this database — or a session derived from it —
// validates its table bindings against automatically, without an explicit
// uow.WithIdentities. A conflict in the metadata (meta.ErrTableConflict) is
// surfaced by Open; WithMetadata is otherwise opt-in, and a database without
// it runs uow scopes that skip catalog validation.
func WithMetadata(m meta.Metadata) DatabaseOption {
	return func(d *Database) {
		im, err := m.Identities()
		if err != nil {
			d.metaErr = err
			return
		}
		d.identities = im
	}
}

// WithInterceptor registers interceptors that wrap every statement run through
// the database (and through sessions it opens). They run in registration order,
// the first wrapping the rest, around both reads and writes — see Interceptor.
// Multiple calls accumulate.
func WithInterceptor(interceptors ...Interceptor) DatabaseOption {
	return func(d *Database) { d.ics = append(d.ics, interceptors...) }
}

// WithResultObserver registers observers that inspect the result of every query
// scanned through the database (and through sessions it opens). Unlike an
// Interceptor, which wraps execution and cannot read the result without consuming
// it, a ResultObserver sees the result already materialized into its Go value, so
// it is the place to examine column values or accumulate statistics — see
// ResultObserver. Observers run in registration order; multiple calls accumulate.
func WithResultObserver(observers ...ResultObserver) DatabaseOption {
	return func(d *Database) { d.resultObs = append(d.resultObs, observers...) }
}

// dbExecutor is the default terminal Executor: it runs compiled statements
// against the wrapped database/sql handle in auto-commit mode, and begins
// transactions on it for sessions.
type dbExecutor struct{ db *stdsql.DB }

func (e dbExecutor) Query(ctx context.Context, stmt Statement) (*stdsql.Rows, error) {
	return e.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
}

func (e dbExecutor) Exec(ctx context.Context, stmt Statement) (stdsql.Result, error) {
	return e.db.ExecContext(ctx, stmt.SQL, stmt.Args...)
}

// Begin starts a database/sql transaction, satisfying TxExecutor so sessions of
// a WrapDB/Open database run inside a real transaction.
func (e dbExecutor) Begin(ctx context.Context, opts *stdsql.TxOptions) (Tx, error) {
	tx, err := e.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return txExecutor{tx}, nil
}

// txExecutor is the default Tx: it runs compiled statements on a database/sql
// transaction and ends it with Commit or Rollback.
type txExecutor struct{ tx *stdsql.Tx }

func (e txExecutor) Query(ctx context.Context, stmt Statement) (*stdsql.Rows, error) {
	return e.tx.QueryContext(ctx, stmt.SQL, stmt.Args...)
}

func (e txExecutor) Exec(ctx context.Context, stmt Statement) (stdsql.Result, error) {
	return e.tx.ExecContext(ctx, stmt.SQL, stmt.Args...)
}

func (e txExecutor) Commit() error   { return e.tx.Commit() }
func (e txExecutor) Rollback() error { return e.tx.Rollback() }

// WithBatchReadSnapshot controls whether a split SELECT (a large IN list chunked
// to stay within the bind-parameter limit) runs its chunk queries inside one
// REPEATABLE READ transaction, so they observe a single consistent snapshot just
// as the unsplit statement would. It is on by default whenever splitting is
// enabled; pass false to run the chunks as independent reads instead, trading
// the snapshot for not holding a connection across the chunks. It has no effect
// when splitting is disabled, or on chunks already running inside a Session
// (which use that session's transaction). It does not change the atomic
// transaction wrapping split INSERT/UPDATE/DELETE writes.
func WithBatchReadSnapshot(enabled bool) DatabaseOption {
	return func(d *Database) { d.batchReadSnapshot = enabled }
}

// WithBatchWriteSnapshot raises the isolation of the transaction wrapping a split
// INSERT/UPDATE/DELETE to REPEATABLE READ, so any data those statements read is
// consistent across the chunks — the case that matters for a reference UPDATE,
// whose SET subquery or FROM source would otherwise be re-read per chunk and
// could drift between them. It is off by default because REPEATABLE READ can
// surface serialization failures (PostgreSQL 40001) the caller must retry; the
// split is always atomic regardless, so enable this only when the chunks read
// data that must not change mid-batch. It has no effect on chunks already
// running inside a Session, which use that session's transaction.
func WithBatchWriteSnapshot(enabled bool) DatabaseOption {
	return func(d *Database) { d.batchWriteSnapshot = enabled }
}

// WithAutoChunk enables or disables automatic statement splitting. It is off by
// default; pass true so bulk INSERTs, large IN lists, RETURNING writes, and uow
// flushes are split to stay within the dialect's bind-parameter limit (the
// BindParamLimiter capability, 65535 for PostgreSQL/MySQL). With it off, explicit
// chunking via AutoChunk and InBatches still works. Sessions inherit the setting
// and can override it with WithSessionAutoChunk.
func WithAutoChunk(enabled bool) DatabaseOption {
	return func(d *Database) { d.autoChunk = enabled }
}

// WithPreloadBatchSize sets the maximum number of parent keys per follow-up IN
// query a Preload issues, so an eager-load over many parents runs several smaller
// statements (like SQLAlchemy's selectinload) instead of one large IN. It applies
// in addition to the dialect's bind-parameter limit and only when auto-split is
// enabled. The default is 1000; pass 0 to chunk only at that hard limit. Sessions
// inherit the setting.
func WithPreloadBatchSize(n int) DatabaseOption {
	return func(d *Database) { d.preloadChunk = n }
}

// Open opens a database/sql handle from a driver name and DSN string. The
// dialect is inferred from the driver name (see DialectForDriver); pass
// WithDialect to override.
func Open(driverName, dsn string, opts ...DatabaseOption) (*Database, error) {
	db, err := stdsql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	d := WrapDB(db, append([]DatabaseOption{WithDialect(DialectForDriver(driverName))}, opts...)...)
	if d.metaErr != nil {
		return nil, d.metaErr
	}
	return d, nil
}

// WrapDB creates a sqlkit database handle from an existing database/sql handle.
func WrapDB(db *stdsql.DB, opts ...DatabaseOption) *Database {
	return newDatabase(db, nil, opts)
}

// WrapExecutor creates a database that runs every statement through exec instead
// of a database/sql handle — the counterpart to WrapDB for a custom terminal
// (statement router, in-memory or remote backend, test double). exec is the
// innermost handler of the interceptor chain, and sessions opened from the
// database inherit it.
//
// Sessions opened from the database are transactional when exec also implements
// TxExecutor: their statements run in a transaction it begins, and Commit,
// Rollback, and savepoints take effect. When exec implements only Executor, a
// session has no managed transaction — its statements run directly through exec
// and Commit/Rollback are no-ops, so exec owns any transactional semantics.
func WrapExecutor(exec Executor, opts ...DatabaseOption) *Database {
	return newDatabase(nil, exec, opts)
}

// newDatabase builds a Database with the shared defaults, then applies opts. A
// WrapDB/Open database passes db (exec nil); WrapExecutor passes exec (db nil).
func newDatabase(db *stdsql.DB, exec Executor, opts []DatabaseOption) *Database {
	d := &Database{db: db, exec: exec, dialect: sql.PostgreSQLDialect{}, batchReadSnapshot: true, autoChunk: false, preloadChunk: defaultPreloadChunkSize}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// newBatchReadSession opens a REPEATABLE READ, read-only session for running a
// split SELECT's chunks under one snapshot, or returns false when the consistent
// snapshot is disabled.
func (d *Database) newBatchReadSession() (*Session, bool) {
	if !d.batchReadSnapshot {
		return nil, false
	}
	return d.NewSession(WithTxOptions(TxOptions{Isolation: stdsql.LevelRepeatableRead, ReadOnly: true})), true
}

// batchWriteSessionOptions returns the session options for the transaction that
// wraps a split write: REPEATABLE READ when the write snapshot is enabled, else
// none (the driver's default isolation).
func (d *Database) batchWriteSessionOptions() []SessionOption {
	if !d.batchWriteSnapshot {
		return nil
	}
	return []SessionOption{WithTxOptions(TxOptions{Isolation: stdsql.LevelRepeatableRead})}
}

// SQL returns the wrapped database/sql handle.
func (d *Database) SQL() *stdsql.DB { return d.db }

// Dialect returns the configured SQL dialect.
func (d *Database) Dialect() sql.Dialect { return d.dialect }

// Identities returns the catalog identity map compiled from the metadata bound
// with WithMetadata, or nil if none was bound. A uow.Scope opened over this
// database reads it to validate table bindings automatically.
func (d *Database) Identities() *meta.IdentityMap { return d.identities }

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
	autoChunk := d.autoChunk
	if options.autoChunk != nil {
		autoChunk = *options.autoChunk
	}
	return &Session{base: d.executor(), txOpts: options.tx, dialect: d.dialect, identities: d.identities, ics: d.ics, autoChunk: autoChunk, preloadChunk: d.preloadChunk, resultObs: d.resultObs}
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
	tx        *stdsql.TxOptions
	autoChunk *bool
}

// WithTxOptions sets transaction options for the session.
func WithTxOptions(options TxOptions) SessionOption {
	return func(o *sessionOptions) { o.tx = options.ToSQL() }
}

// WithSessionAutoChunk overrides, for this session, whether automatic
// limit-driven statement splitting is enabled. Without it a session inherits the
// database's WithAutoChunk setting; pass true or false to force it on or off for
// statements run through the session (and uow scopes opened over it).
func WithSessionAutoChunk(enabled bool) SessionOption {
	return func(o *sessionOptions) { o.autoChunk = &enabled }
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
	return runExecStmt(ctx, d, Statement{
		Op:      OpExec,
		Query:   query,
		SQL:     sqlText,
		Args:    args,
		Dialect: d.dialect,
	})
}

func (d *Database) sqlDialect() sql.Dialect { return d.dialect }

func (d *Database) interceptors() []Interceptor { return d.ics }

// maxBindParams is the single splitting chokepoint: the dialect's bind-parameter
// limit when auto-chunk is on, or 0 when it is off (suppressing every automatic
// split).
func (d *Database) maxBindParams() int {
	if !d.autoChunk {
		return 0
	}
	return resolveBindParamLimit(d.dialect)
}

// AutoChunkEnabled reports whether automatic, limit-driven statement splitting is
// enabled for this database (WithAutoChunk).
func (d *Database) AutoChunkEnabled() bool { return d.autoChunk }

func (d *Database) preloadChunkSize() int { return d.preloadChunk }

func (d *Database) executor() Executor {
	if d.exec != nil {
		return d.exec
	}
	return dbExecutor{d.db}
}

func (d *Database) resultObservers() []ResultObserver { return d.resultObs }
