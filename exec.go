package sqlkit

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/aita/sqlkit/sql"
)

// Querier starts typed query chains. Both *Database (auto-commit) and
// *Session (a single transaction) implement it. Chains are bound to the
// querier they start from, so terminals take only a context:
//
//	db.Select().From(Users).All(ctx).Scan(&users)
//	session.Update(Users).Set(...).Exec(ctx)
type Querier interface {
	// Select starts a typed SELECT bound to this querier. With no arguments
	// it selects every column of the From source.
	Select(selections ...sql.Selection) SelectQuery

	// Insert starts an INSERT bound to this querier. Feed it rows with
	// Values, or a query with FromSelect.
	Insert(table Table) InsertQuery

	// Update starts an UPDATE bound to this querier.
	Update(table Table) UpdateQuery

	// Delete starts a DELETE bound to this querier.
	Delete(table Table) DeleteQuery

	// Merge starts a MERGE bound to this querier. Set the source with Using,
	// the join with On, and the actions with When.
	Merge(target Table) MergeQuery

	// RawSQL prepares a hand-written SQL string (with args) or a built
	// sql.Query; materialize it with Scan.
	RawSQL(ctx context.Context, query any, args ...any) Rows

	// Dialect returns the SQL dialect the querier compiles against. Both
	// *Database and *Session expose it, so helpers bound to a Querier can
	// branch on dialect capabilities (see Dialect.SupportsReturning).
	Dialect() sql.Dialect
}

// Executor runs a single compiled Statement against the database. It is the
// terminal that runs a statement after its Before hooks have shaped it. Supply
// one with WrapExecutor to route, instrument, or fake statement execution; the
// default runs against the wrapped database/sql handle (or a Session's
// transaction).
type Executor interface {
	// Query runs a read statement and returns its rows.
	Query(ctx context.Context, stmt Statement) (*stdsql.Rows, error)
	// Exec runs a write statement and returns its result.
	Exec(ctx context.Context, stmt Statement) (stdsql.Result, error)
}

// Tx is a transaction-scoped Executor: its Query and Exec run inside the
// transaction, and Commit or Rollback ends it. It is what TxExecutor.Begin
// returns.
type Tx interface {
	Executor
	// Commit commits the transaction.
	Commit() error
	// Rollback aborts the transaction, discarding its work.
	Rollback() error
}

// TxExecutor is an Executor that can begin a transaction. A Session opened over a
// terminal that implements it runs inside a real transaction — Commit, Rollback,
// and savepoints take effect; over a terminal that does not, the session runs
// statements directly with no managed transaction. The default database/sql
// handle implements it (so WrapDB and Open sessions are transactional); a
// WrapExecutor terminal may implement it to give its own sessions transactions.
type TxExecutor interface {
	Executor
	// Begin starts a transaction with the given options (nil for the driver
	// default) and returns it as a Tx.
	Begin(ctx context.Context, opts *stdsql.TxOptions) (Tx, error)
}

// executorBinding is the execution context a query chain is bound to: the
// dialect it compiles for, the hooks and bind-parameter limit that shape
// execution, and the terminal Executor that runs the compiled statement. Both
// *Database and *Session implement it, and the typed builders (SelectQuery,
// InsertQuery, ...) and Rows depend only on it — not on Querier's building
// methods.
type executorBinding interface {
	sqlDialect() sql.Dialect
	// hooks returns the lifecycle hooks run around each statement, registered
	// with WithHooks and inherited by sessions.
	hooks() []Hooks
	// maxBindParams is the bind-parameter limit driving automatic splitting, or 0
	// when auto-chunk is off (WithAutoChunk) so a single <= 0 check disables every
	// automatic split. Explicit chunking (AutoChunk, InBatches) does
	// not consult it.
	maxBindParams() int
	// executor returns the terminal that runs compiled statements: the
	// connection-backed default, or a WrapExecutor database's custom one.
	executor() Executor
}

// ErrNoRows is returned when a single-row Scan finds no row.
var ErrNoRows = stdsql.ErrNoRows

// ErrTooManyRows is returned when a One query yields more than one row.
var ErrTooManyRows = errors.New("sqlkit: query returned more than one row")

// ErrVersionConflict is returned by an optimistic-locking UPDATE
// (UpdateQuery.CheckVersion) whose Exec matches no row, meaning another writer
// advanced the version column first or the row no longer exists. Test for it
// with errors.Is to reload and retry.
var ErrVersionConflict = errors.New("sqlkit: optimistic lock conflict: row was modified concurrently")

// errNoExecutor is returned when a chain built from the package-level
// constructors (meant for subqueries and ToSQL) is executed.
var errNoExecutor = errors.New(
	"sqlkit: query is not bound to an executor; start the chain from a Database or Session (db.Select(), session.Insert(), ...)")

// errNoTx is returned by a session's savepoint methods when the session is not
// backed by a transaction — its terminal Executor does not implement TxExecutor
// (a WrapExecutor terminal with only Query/Exec), so there is no transaction to
// scope a savepoint to.
var errNoTx = errors.New(
	"sqlkit: savepoints require a transaction; this session's executor does not implement TxExecutor")

// Rows is a pending query result. It is produced by All, First, One, and
// RawSQL, and materialized with Scan. Execution is deferred until Scan, so
// build and query errors surface there.
type Rows struct {
	ctx     context.Context
	ex      executorBinding
	query   sql.Query
	sqlText string
	args    []any
	err     error
	// exactlyOne makes Scan require a single-row result: ErrNoRows when
	// empty, ErrTooManyRows when a second row exists. Set by One.
	exactlyOne bool
	// preloads are relations eager-loaded after a successful Scan, set by
	// SelectQuery terminals when Preload was registered.
	preloads []preloadSpec
	// batches, when non-empty, holds several compiled statements whose rows Scan
	// concatenates into the destination slice. It is set by All when a large IN
	// list is split to stay within the bind-parameter limit; sqlText/args are
	// then unused.
	batches []compiledStmt
	// batchWrite marks the batches as RETURNING writes (split bulk
	// INSERT/UPDATE/DELETE) rather than reads, so Scan runs them in one writable
	// transaction for atomicity instead of the read-only snapshot session.
	batchWrite bool
}

func newRows(ctx context.Context, ex executorBinding, query sql.Query) Rows {
	if ex == nil {
		return Rows{err: errNoExecutor}
	}
	// A builder that splits itself (a large IN list, or a bulk RETURNING write)
	// runs as several chunked statements whose rows Scan concatenates.
	if s, ok := query.(splitter); ok {
		stmts, split, write, err := s.splitStatements(ex)
		if err != nil {
			return Rows{err: err}
		}
		if split {
			return Rows{ctx: ctx, ex: ex, batches: stmts, batchWrite: write}
		}
	}
	sqlText, args, err := query.ToSQL(ex.sqlDialect())
	return Rows{ctx: ctx, ex: ex, query: query, sqlText: sqlText, args: args, err: err}
}

// statement builds the read Statement a hook sees for this Rows.
func (r Rows) statement() Statement {
	return Statement{
		Op:      OpQuery,
		Query:   r.query,
		SQL:     r.sqlText,
		Args:    r.args,
		Dialect: r.ex.sqlDialect(),
	}
}

// ErrRows returns a Rows that fails with err when scanned. It lets the builders
// defer a build-time error (e.g. SelectQuery.Filter's mapping error) to the
// Scan call, matching how the typed builders surface compilation errors.
func ErrRows(err error) Rows { return Rows{err: err} }

func newRawRows(ctx context.Context, ex executorBinding, query any, args []any) Rows {
	sqlText, sqlArgs, err := resolveQuery(ex.sqlDialect(), query, args)
	// Carry the AST for hooks when one was supplied; a raw string has none.
	ast, _ := query.(sql.Query)
	return Rows{ctx: ctx, ex: ex, query: ast, sqlText: sqlText, args: sqlArgs, err: err}
}

// Scan runs the query and copies the result into dest. Columns are matched
// to struct fields by `sqlkit` tag, exact field name, or snake_case field
// name, with embedded structs flattened; fields implementing sql.Scanner
// (e.g. sql.NullString) receive the column value directly. dest may be:
//
//   - a pointer to a slice of structs, struct pointers, or scalars — every row;
//   - a pointer to a struct or scalar — the first row, or ErrNoRows if none.
func (r Rows) Scan(dest any) error {
	if r.err != nil {
		return r.err
	}
	if len(r.batches) > 0 {
		return r.scanBatched(dest)
	}
	stmt := r.statement()
	ctx, rows, err := runQuery(r.ctx, r.ex, stmt)
	if err != nil {
		return afterQuery(ctx, r.ex.hooks(), stmt, dest, err)
	}
	scanErr := func() error {
		defer rows.Close()
		if r.exactlyOne {
			return scanExactlyOne(rows, dest)
		}
		return scanInto(rows, dest)
	}()
	if err := afterQuery(ctx, r.ex.hooks(), stmt, dest, scanErr); err != nil {
		return err
	}
	// Eager-load any registered relations onto the just-scanned destination.
	if len(r.preloads) > 0 {
		return applyPreloads(r.ctx, r.ex, dest, r.preloads)
	}
	return nil
}

// scanBatched runs the split statements in turn and concatenates their rows
// into dest, a pointer to a slice. It backs a SELECT whose large IN list was
// split to stay within the bind-parameter limit; each batch is scanned into a
// fresh slice of the destination type and appended, then any preloads run once
// over the combined result.
func (r Rows) scanBatched(dest any) (err error) {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() || dv.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("sqlkit: a split query scans into a pointer to a slice, got %T", dest)
	}
	ex, finish := r.batchExecutor()
	defer func() { err = finish(err) }()

	sliceType := dv.Elem().Type()
	dialect := ex.sqlDialect()
	acc := reflect.MakeSlice(sliceType, 0, 0)
	// Before hooks run per chunk (one per statement actually sent), but the
	// AfterQuery hook runs once over the whole union below — a split query's
	// result is conceptually one result.
	scanErr := func() error {
		for _, batch := range r.batches {
			_, rows, runErr := runQuery(r.ctx, ex, Statement{Op: OpQuery, SQL: batch.sql, Args: batch.args, Dialect: dialect})
			if runErr != nil {
				return runErr
			}
			tmp := reflect.New(sliceType)
			if err := func() error {
				defer rows.Close()
				return scanInto(rows, tmp.Interface())
			}(); err != nil {
				return err
			}
			acc = reflect.AppendSlice(acc, tmp.Elem())
		}
		return nil
	}()
	if scanErr == nil {
		dv.Elem().Set(acc)
	}
	if err := afterQuery(r.ctx, ex.hooks(), Statement{Op: OpQuery, Dialect: dialect}, dest, scanErr); err != nil {
		return err
	}
	if len(r.preloads) > 0 {
		return applyPreloads(r.ctx, ex, dest, r.preloads)
	}
	return nil
}

// batchExecutor chooses the executor that runs this Rows' chunk statements and a
// finish function that commits or rolls back any transaction it opened (a no-op
// otherwise). A Session executor is reused as-is — its own transaction already
// wraps the chunks. Through an auto-committing Database, a write batch (RETURNING)
// always runs in one writable transaction so the chunks are atomic (raised to
// REPEATABLE READ by WithBatchWriteSnapshot), while a read batch (split SELECT)
// runs under the read-only REPEATABLE READ snapshot when WithBatchReadSnapshot is
// on, else directly on the database.
func (r Rows) batchExecutor() (executorBinding, func(error) error) {
	noop := func(err error) error { return err }
	db, ok := r.ex.(*Database)
	if !ok {
		return r.ex, noop
	}
	var session *Session
	if r.batchWrite {
		session = db.NewSession(db.batchWriteSessionOptions()...)
	} else if s, on := db.newBatchReadSession(); on {
		session = s
	}
	if session == nil {
		return db, noop
	}
	return session, func(err error) error {
		if err != nil {
			_ = session.Rollback()
			return err
		}
		return session.Commit()
	}
}

// scanExactlyOne scans the single result row into dest, requiring exactly
// one: ErrNoRows when the result is empty, ErrTooManyRows when a second row
// exists.
func scanExactlyOne(rows *stdsql.Rows, dest any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("sqlkit: Scan destination must be a non-nil pointer, got %T", dest)
	}
	if dv.Elem().Kind() == reflect.Slice {
		return fmt.Errorf("sqlkit: One expects a single-row destination, not a slice; use All for multiple rows")
	}
	found, err := structScanOne(rows, dest)
	if err != nil {
		return err
	}
	if !found {
		return ErrNoRows
	}
	if rows.Next() {
		return ErrTooManyRows
	}
	return rows.Err()
}

// ScanRows copies an externally produced *sql.Rows result into dest, using
// the same column-to-field mapping rules as Rows.Scan (`sqlkit` tag, exact
// name, snake_case, embedded structs flattened, sql.Scanner fields). It makes
// sqlkit's scanner available when the query is executed by another library —
// plain database/sql, sqlx, a pgx stdlib pool — typically over SQL produced
// by the unbound builders with ToSQL:
//
//	sqlText, args, _ := sqlkit.Select().From(Users).Where(...).ToSQL(dialect)
//	rows, _ := otherDB.QueryContext(ctx, sqlText, args...)
//	defer rows.Close()
//	err := sqlkit.ScanRows(rows, &users)
//
// dest may be a pointer to a slice of structs, struct pointers, or scalars
// (every remaining row), or a pointer to a struct or scalar (the next row,
// or ErrNoRows when none is left). The caller keeps ownership of rows and
// remains responsible for closing them.
func ScanRows(rows *stdsql.Rows, dest any) error {
	return scanInto(rows, dest)
}

func scanInto(rows *stdsql.Rows, dest any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("sqlkit: Scan destination must be a non-nil pointer, got %T", dest)
	}
	if dv.Elem().Kind() == reflect.Slice {
		return structScanAll(rows, dest)
	}
	found, err := structScanOne(rows, dest)
	if err != nil {
		return err
	}
	if !found {
		return ErrNoRows
	}
	return nil
}

// runExec compiles and runs a statement, returning the database/sql result.
func runExec(ctx context.Context, ex executorBinding, query sql.Query) (stdsql.Result, error) {
	if ex == nil {
		return nil, errNoExecutor
	}
	// A builder that splits itself (a bulk INSERT, or a large WHERE IN) runs its
	// chunks atomically; execStatements merges their result.
	if s, ok := query.(splitter); ok {
		stmts, split, _, err := s.splitStatements(ex)
		if err != nil {
			return nil, err
		}
		if split {
			return execStatements(ctx, ex, stmts)
		}
	}
	sqlText, args, err := query.ToSQL(ex.sqlDialect())
	if err != nil {
		return nil, err
	}
	return runExecStmt(ctx, ex, Statement{
		Op:      OpExec,
		Query:   query,
		SQL:     sqlText,
		Args:    args,
		Dialect: ex.sqlDialect(),
	})
}
