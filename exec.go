package sqlkit

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"iter"
	"reflect"

	"github.com/aita/sqlkit/sql"
)

// Executor starts and runs queries. Both *Database (auto-commit) and
// *Session (a single transaction) implement it. Chains are bound to the
// executor they start from, so terminals take only a context:
//
//	db.Select().From(Users).All(ctx).Scan(&users)
//	session.Update(Users).Set(...).Exec(ctx)
type Executor interface {
	// Select starts a typed SELECT bound to this executor. With no arguments
	// it selects every column of the From source.
	Select(selections ...sql.Selection) SelectQuery

	// Insert starts an INSERT bound to this executor. Feed it rows with
	// Values, or a query with FromSelect.
	Insert(table Table) InsertQuery

	// Update starts an UPDATE bound to this executor.
	Update(table Table) UpdateQuery

	// Delete starts a DELETE bound to this executor.
	Delete(table Table) DeleteQuery

	// RawSQL prepares a hand-written SQL string (with args) or a built
	// sql.Query; materialize it with Scan.
	RawSQL(ctx context.Context, query any, args ...any) Rows

	sqlDialect() sql.Dialect
	queryContext(ctx context.Context, query string, args ...any) (*stdsql.Rows, error)
	execContext(ctx context.Context, query string, args ...any) (stdsql.Result, error)
}

// ErrNoRows is returned when a single-row Scan finds no row.
var ErrNoRows = stdsql.ErrNoRows

// ErrTooManyRows is returned when a One query yields more than one row.
var ErrTooManyRows = errors.New("sqlkit: query returned more than one row")

// errNoExecutor is returned when a chain built from the package-level
// constructors (meant for subqueries and ToSQL) is executed.
var errNoExecutor = errors.New(
	"sqlkit: query is not bound to an executor; start the chain from a Database or Session (db.Select(), session.Insert(), ...)")

// Rows is a pending query result. It is produced by All, First, One, and
// RawSQL, and materialized with Scan, or streamed row by row with the
// package-level Records function. Execution is deferred until either
// terminal, so build and query errors surface there.
type Rows struct {
	ctx     context.Context
	ex      Executor
	sqlText string
	args    []any
	err     error
	// exactlyOne makes Scan require a single-row result: ErrNoRows when
	// empty, ErrTooManyRows when a second row exists. Set by One.
	exactlyOne bool
	// preloads are relations eager-loaded after a successful Scan, set by
	// SelectQuery terminals when Preload was registered.
	preloads []Relation
}

func newRows(ctx context.Context, ex Executor, query sql.Query) Rows {
	if ex == nil {
		return Rows{err: errNoExecutor}
	}
	sqlText, args, err := query.ToSQL(ex.sqlDialect())
	return Rows{ctx: ctx, ex: ex, sqlText: sqlText, args: args, err: err}
}

// ErrRows returns a Rows that fails with err when scanned. It lets the builders
// defer a build-time error (e.g. SelectQuery.Filter's mapping error) to the
// Scan call, matching how the typed builders surface compilation errors.
func ErrRows(err error) Rows { return Rows{err: err} }

func newRawRows(ctx context.Context, ex Executor, query any, args []any) Rows {
	sqlText, sqlArgs, err := resolveQuery(ex.sqlDialect(), query, args)
	return Rows{ctx: ctx, ex: ex, sqlText: sqlText, args: sqlArgs, err: err}
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
	rows, err := r.ex.queryContext(r.ctx, r.sqlText, r.args...)
	if err != nil {
		return err
	}
	if err := func() error {
		defer rows.Close()
		if r.exactlyOne {
			return scanExactlyOne(rows, dest)
		}
		return scanInto(rows, dest)
	}(); err != nil {
		return err
	}
	// Eager-load any registered relations onto the just-scanned destination.
	if len(r.preloads) > 0 {
		return applyPreloads(r.ctx, r.ex, dest, r.preloads)
	}
	return nil
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

// Records runs the query and streams each row as an already-scanned T:
//
//	for user, err := range sqlkit.Records[User](db.RawSQL(ctx, query)) {
//		if err != nil { return err }
//		...
//	}
//
// T may be a struct, a pointer to struct, or a scalar, with columns matched
// to fields by the same rules as Rows.Scan. Rows are fetched on demand and
// scanned one at a time — the result set is never materialized as a whole —
// and breaking out of the loop releases it. It is a generic function rather
// than a method on Rows because Go methods cannot have type parameters.
// Like Scan, each range over the iterator executes the query anew.
func Records[T any](r Rows) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		if r.err != nil {
			yield(zero, r.err)
			return
		}
		rows, err := r.ex.queryContext(r.ctx, r.sqlText, r.args...)
		if err != nil {
			yield(zero, err)
			return
		}
		defer rows.Close()

		elemType := reflect.TypeFor[T]()
		pointerElems := elemType.Kind() == reflect.Pointer
		baseType := elemType
		if pointerElems {
			baseType = elemType.Elem()
		}
		scan, err := rowScanner(rows, baseType)
		if err != nil {
			yield(zero, err)
			return
		}
		for rows.Next() {
			item := reflect.New(baseType)
			if err := scan(item.Elem()); err != nil {
				yield(zero, err)
				return
			}
			var value T
			if pointerElems {
				value = item.Interface().(T)
			} else {
				value = item.Elem().Interface().(T)
			}
			if !yield(value, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(zero, err)
		}
	}
}

// runExec compiles and runs a statement, returning the database/sql result.
func runExec(ctx context.Context, ex Executor, query sql.Query) (stdsql.Result, error) {
	if ex == nil {
		return nil, errNoExecutor
	}
	sqlText, args, err := query.ToSQL(ex.sqlDialect())
	if err != nil {
		return nil, err
	}
	return ex.execContext(ctx, sqlText, args...)
}
