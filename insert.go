package sqlkit

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
	"github.com/aita/sqlkit/sql"
)

// InsertQuery is an INSERT statement, fed either by Values (row structs,
// mapped to columns by field name the same way Scan maps results) or by
// FromSelect (INSERT ... SELECT).
type InsertQuery struct {
	ex           Executor
	def          sql.TableDef
	rows         []any
	selectSrc    sql.Query
	columns      []AnyColumn
	conflict     *insertConflict
	returning    []sql.Selection
	hasReturning bool
	batchSize    int
}

// insertConflict holds a typed upsert action mirroring sql.conflictClause.
type insertConflict struct {
	doUpdate bool
	target   []AnyColumn
	sets     []sql.Assignment
	where    sql.Expression
}

// Insert creates an unbound INSERT builder, for ToSQL or tests. Executing it
// returns an error; start from a Database or Session to run it:
//
//	db.Insert(Users).Values([]User{{...}}).Exec(ctx)
//	db.Insert(Archive).Columns(...).FromSelect(sqlkit.Select(...).From(Posts)).Exec(ctx)
func Insert(table Table) InsertQuery {
	return InsertQuery{def: tableDef(table)}
}

// Values appends rows to insert. Each argument may be a struct, a pointer to
// a struct, or a slice of either. Struct fields map to columns by `sqlkit`
// tag, exact name, or snake_case — the same rules Scan uses —
// so both generated row structs and caller-defined DTOs work. Columns with no
// matching field are omitted, as are defaulted columns that are zero-valued
// in every row.
func (q InsertQuery) Values(rows ...any) InsertQuery {
	for _, row := range rows {
		rv := reflect.ValueOf(row)
		if rv.Kind() == reflect.Slice {
			for i := 0; i < rv.Len(); i++ {
				q.rows = append(q.rows, rv.Index(i).Interface())
			}
			continue
		}
		q.rows = append(q.rows, row)
	}
	return q
}

// Columns restricts the inserted columns for FromSelect; the order must match
// the SELECT list. Without it the database assigns the query's columns in
// table order.
func (q InsertQuery) Columns(columns ...AnyColumn) InsertQuery {
	q.columns = columns
	return q
}

// BatchSize splits a Values-based INSERT into separate statements of at most n
// rows each when executed, keeping large bulk inserts within the database's
// bind-parameter and statement-size limits. n <= 0 (the default) inserts every
// row in a single statement.
//
// It applies to Exec only: ToSQL always compiles the full statement, and it has
// no effect on FromSelect. Wrap the Exec in a transaction (a Session) if the
// batches must succeed or fail together.
func (q InsertQuery) BatchSize(n int) InsertQuery {
	q.batchSize = n
	return q
}

// FromSelect makes this an INSERT ... SELECT, inserting the rows produced by
// the query. It is mutually exclusive with Values.
func (q InsertQuery) FromSelect(query sql.Query) InsertQuery {
	// Unwrap the typed builders so the subquery shares placeholder numbering.
	q.selectSrc = unwrapQuery(query)
	return q
}

// OnConflictDoNothing makes this an upsert that skips conflicting rows
// (PostgreSQL ON CONFLICT [ (target...) ] DO NOTHING). target names the
// conflicting columns. MySQL has no DO NOTHING form.
func (q InsertQuery) OnConflictDoNothing(target ...AnyColumn) InsertQuery {
	q.conflict = &insertConflict{target: target}
	return q
}

// OnConflictDoUpdate makes this an upsert that updates conflicting rows
// (PostgreSQL ON CONFLICT (target...) DO UPDATE SET ..., MySQL ON DUPLICATE KEY
// UPDATE ...). Build assignments with Column.Set / SetExpr, referencing the
// proposed values with Column.Excluded:
//
//	db.Insert(Users).Values(u).
//		OnConflictDoUpdate([]sqlkit.AnyColumn{Users.ID}, Users.Name.SetExpr(Users.Name.Excluded())).
//		Exec(ctx)
func (q InsertQuery) OnConflictDoUpdate(target []AnyColumn, assignments ...sql.Assignment) InsertQuery {
	q.conflict = &insertConflict{doUpdate: true, target: target, sets: assignments}
	return q
}

// OnConflictWhere adds a predicate to a preceding OnConflictDoUpdate
// (PostgreSQL ON CONFLICT ... DO UPDATE SET ... WHERE ...). It is unsupported
// on MySQL and has no effect without a DO UPDATE action.
func (q InsertQuery) OnConflictWhere(predicates ...Predicate) InsertQuery {
	if q.conflict != nil {
		if expr := combineWhere(predicates); expr != nil {
			clause := *q.conflict
			clause.where = expr
			q.conflict = &clause
		}
	}
	return q
}

// Returning adds an explicit RETURNING clause and makes the statement yield
// rows: end the chain with All(...).Scan to read them (e.g. database-generated
// keys). With no arguments it returns all table columns; otherwise the given
// columns or expressions.
func (q InsertQuery) Returning(selections ...sql.Selection) ReturningQuery {
	q.returning = selections
	q.hasReturning = true
	return ReturningQuery{ex: q.ex, query: q}
}

// ToSQL compiles the INSERT statement.
func (q InsertQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	insert, err := q.build()
	if err != nil {
		return "", nil, err
	}
	if q.conflict != nil {
		insert = q.applyConflict(insert)
	}
	if q.hasReturning {
		insert = insert.Returning(q.returningSelections()...)
	}
	return insert.ToSQL(dialect)
}

// applyConflict copies this query's upsert configuration onto the sql builder.
func (q InsertQuery) applyConflict(insert sql.InsertQuery) sql.InsertQuery {
	target := columnNames(q.conflict.target)
	if q.conflict.doUpdate {
		insert = insert.OnConflictDoUpdate(target, q.conflict.sets...)
	} else {
		insert = insert.OnConflictDoNothing(target...)
	}
	if q.conflict.where != nil {
		insert = insert.OnConflictWhere(q.conflict.where)
	}
	return insert
}

func (q InsertQuery) returningSelections() []sql.Selection {
	if len(q.returning) > 0 {
		return q.returning
	}
	return q.def.Selections()
}

func (q InsertQuery) build() (sql.InsertQuery, error) {
	if q.selectSrc != nil {
		if len(q.rows) > 0 {
			return sql.InsertQuery{}, fmt.Errorf("sqlkit: INSERT cannot have both Values and FromSelect")
		}
		insert := sql.InsertInto(q.def.Table)
		if len(q.columns) > 0 {
			insert = insert.Columns(columnNames(q.columns)...)
		}
		return insert.FromSelect(q.selectSrc), nil
	}
	if len(q.columns) > 0 {
		return sql.InsertQuery{}, fmt.Errorf("sqlkit: Columns applies to FromSelect; Values maps struct fields to columns by name")
	}
	if len(q.rows) == 0 {
		return sql.InsertQuery{}, fmt.Errorf("sqlkit: INSERT requires Values or FromSelect")
	}
	names, valueRows, err := q.mapRows()
	if err != nil {
		return sql.InsertQuery{}, err
	}
	// Delegate to the sql layer with a definition narrowed to the mapped
	// columns, so the defaulted-and-zero omission applies to them.
	subDef := sql.TableDef{
		Table:     q.def.Table,
		Columns:   names,
		Defaulted: intersect(q.def.Defaulted, names),
	}
	return subDef.InsertRows(valueRows)
}

// mapRows resolves the rows' struct fields against the table columns and
// extracts the values, in table column order.
func (q InsertQuery) mapRows() ([]string, [][]any, error) {
	type plan struct {
		names []string
		paths [][]int
	}
	plans := map[reflect.Type]plan{}
	var names []string

	rows := make([][]any, 0, len(q.rows))
	for _, row := range q.rows {
		rv := reflect.ValueOf(row)
		for rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return nil, nil, fmt.Errorf("sqlkit: INSERT row is a nil pointer")
			}
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Struct {
			return nil, nil, fmt.Errorf("sqlkit: INSERT rows must be structs, got %T", row)
		}
		p, ok := plans[rv.Type()]
		if !ok {
			fields := reflectmap.FieldsByColumn(rv.Type())
			for _, name := range q.def.Columns {
				if path, found := fields[strings.ToLower(name)]; found {
					p.names = append(p.names, name)
					p.paths = append(p.paths, path)
				}
			}
			if len(p.names) == 0 {
				return nil, nil, fmt.Errorf("sqlkit: no fields of %s map to columns of %s",
					rv.Type(), q.def.Table.QualifiedName())
			}
			plans[rv.Type()] = p
		}
		if names == nil {
			names = p.names
		} else if !slices.Equal(names, p.names) {
			return nil, nil, fmt.Errorf("sqlkit: INSERT rows map to different column sets (%v vs %v)",
				names, p.names)
		}
		values := make([]any, len(p.paths))
		for i, path := range p.paths {
			values[i] = rv.FieldByIndex(path).Interface()
		}
		rows = append(rows, values)
	}
	return names, rows, nil
}

func intersect(names, allowed []string) []string {
	keep := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		keep[name] = true
	}
	var out []string
	for _, name := range names {
		if keep[name] {
			out = append(out, name)
		}
	}
	return out
}

// Exec runs the INSERT statement and returns the database/sql result. When
// BatchSize is set and the rows exceed it, the rows are inserted in several
// statements and the returned result aggregates them: RowsAffected sums every
// batch and LastInsertId reports the final batch.
func (q InsertQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	if q.batchSize > 0 && len(q.rows) > q.batchSize {
		return q.execBatched(ctx)
	}
	return runExec(ctx, q.ex, q)
}

// execBatched inserts q.rows in chunks of q.batchSize, one statement per chunk.
func (q InsertQuery) execBatched(ctx context.Context) (stdsql.Result, error) {
	if q.ex == nil {
		return nil, errNoExecutor
	}
	result := &batchResult{}
	for start := 0; start < len(q.rows); start += q.batchSize {
		batch := q
		batch.rows = q.rows[start:min(start+q.batchSize, len(q.rows))]
		res, err := runExec(ctx, q.ex, batch)
		if err != nil {
			return nil, err
		}
		result.add(res)
	}
	return result, nil
}

// batchResult aggregates the per-batch results of a BatchSize INSERT: it sums
// RowsAffected across batches and reports LastInsertId from the final batch.
type batchResult struct {
	last         stdsql.Result
	rowsAffected int64
	rowsErr      error
}

func (r *batchResult) add(res stdsql.Result) {
	r.last = res
	if n, err := res.RowsAffected(); err != nil {
		r.rowsErr = err
	} else {
		r.rowsAffected += n
	}
}

func (r *batchResult) LastInsertId() (int64, error) { return r.last.LastInsertId() }

func (r *batchResult) RowsAffected() (int64, error) {
	if r.rowsErr != nil {
		return 0, r.rowsErr
	}
	return r.rowsAffected, nil
}
