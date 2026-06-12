package sqlkit

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
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
	Insert(table TableMeta) InsertQuery

	// Update starts an UPDATE bound to this executor.
	Update(table TableMeta) UpdateQuery

	// Delete starts a DELETE bound to this executor.
	Delete(table TableMeta) DeleteQuery

	// RawSQL prepares a hand-written SQL string (with args) or a built
	// sql.Query; materialize it with Scan.
	RawSQL(ctx context.Context, query any, args ...any) Rows

	sqlDialect() sql.Dialect
	queryContext(ctx context.Context, query string, args ...any) (*stdsql.Rows, error)
	execContext(ctx context.Context, query string, args ...any) (stdsql.Result, error)
}

// ErrNoRows is returned when a single-row Scan finds no row.
var ErrNoRows = stdsql.ErrNoRows

// errNoExecutor is returned when a chain built from the package-level
// constructors (meant for subqueries and ToSQL) is executed.
var errNoExecutor = errors.New(
	"sqlkit: query is not bound to an executor; start the chain from a Database or Session (db.Select(), session.Insert(), ...)")

// Rows is a pending query result. It is produced by All and Query, and
// materialized into a destination with Scan. Execution is deferred until
// Scan, so build and query errors surface there.
type Rows struct {
	ctx     context.Context
	ex      Executor
	sqlText string
	args    []any
	err     error
}

func newRows(ctx context.Context, ex Executor, query sql.Query) Rows {
	if ex == nil {
		return Rows{err: errNoExecutor}
	}
	sqlText, args, err := query.ToSQL(ex.sqlDialect())
	return Rows{ctx: ctx, ex: ex, sqlText: sqlText, args: args, err: err}
}

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
	defer rows.Close()
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
