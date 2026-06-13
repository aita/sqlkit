package sqlkit

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"

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
	returning    []sql.Selection
	hasReturning bool
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

// FromSelect makes this an INSERT ... SELECT, inserting the rows produced by
// the query. It is mutually exclusive with Values.
func (q InsertQuery) FromSelect(query sql.Query) InsertQuery {
	// Unwrap the typed builders so the subquery shares placeholder numbering.
	switch s := query.(type) {
	case SelectQuery:
		q.selectSrc = s.query
	case UnionQuery:
		q.selectSrc = s.query
	default:
		q.selectSrc = query
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
	if q.hasReturning {
		insert = insert.Returning(q.returningSelections()...)
	}
	return insert.ToSQL(dialect)
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
			fields := fieldsByColumn(rv.Type())
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

// Exec runs the INSERT statement and returns the database/sql result.
func (q InsertQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	return runExec(ctx, q.ex, q)
}

// UpdateQuery is an UPDATE statement. Assignments come from typed columns, so
// it does not need to be generic over the row type.
type UpdateQuery struct {
	ex           Executor
	def          sql.TableDef
	query        sql.UpdateQuery
	returning    []sql.Selection
	hasReturning bool
}

// Update creates an unbound UPDATE builder, for ToSQL or tests. Assignments
// are created with Column.Set, so value types are checked at compile time.
// Executing it returns an error; start from a Database or Session to run it.
func Update(table Table) UpdateQuery {
	def := tableDef(table)
	return UpdateQuery{def: def, query: sql.Update(def.Table)}
}

// Set appends SET assignments.
func (q UpdateQuery) Set(assignments ...sql.Assignment) UpdateQuery {
	q.query = q.query.Set(assignments...)
	return q
}

// Where adds predicates, combined with AND.
func (q UpdateQuery) Where(predicates ...Predicate) UpdateQuery {
	if expr := combineWhere(predicates); expr != nil {
		q.query = q.query.Where(expr)
	}
	return q
}

// Returning adds an explicit RETURNING clause and makes the statement yield
// rows: end the chain with All(...).Scan to read them. With no arguments it
// returns all table columns.
func (q UpdateQuery) Returning(selections ...sql.Selection) ReturningQuery {
	q.returning = selections
	q.hasReturning = true
	return ReturningQuery{ex: q.ex, query: q}
}

// ToSQL compiles the UPDATE statement.
func (q UpdateQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	query := q.query
	if q.hasReturning {
		if len(q.returning) > 0 {
			query = query.Returning(q.returning...)
		} else {
			query = query.Returning(q.def.Selections()...)
		}
	}
	return query.ToSQL(dialect)
}

// Exec runs the UPDATE statement and returns the database/sql result.
func (q UpdateQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	return runExec(ctx, q.ex, q)
}

// DeleteQuery is a DELETE statement.
type DeleteQuery struct {
	ex           Executor
	def          sql.TableDef
	query        sql.DeleteQuery
	returning    []sql.Selection
	hasReturning bool
}

// Delete creates an unbound DELETE builder, for ToSQL or tests. Executing it
// returns an error; start from a Database or Session to run it.
func Delete(table Table) DeleteQuery {
	def := tableDef(table)
	return DeleteQuery{def: def, query: sql.DeleteFrom(def.Table)}
}

// Where adds predicates, combined with AND.
func (q DeleteQuery) Where(predicates ...Predicate) DeleteQuery {
	if expr := combineWhere(predicates); expr != nil {
		q.query = q.query.Where(expr)
	}
	return q
}

// Returning adds an explicit RETURNING clause and makes the statement yield
// rows: end the chain with All(...).Scan to read them. With no arguments it
// returns all table columns.
func (q DeleteQuery) Returning(selections ...sql.Selection) ReturningQuery {
	q.returning = selections
	q.hasReturning = true
	return ReturningQuery{ex: q.ex, query: q}
}

// ToSQL compiles the DELETE statement.
func (q DeleteQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	query := q.query
	if q.hasReturning {
		if len(q.returning) > 0 {
			query = query.Returning(q.returning...)
		} else {
			query = query.Returning(q.def.Selections()...)
		}
	}
	return query.ToSQL(dialect)
}

// Exec runs the DELETE statement and returns the database/sql result.
func (q DeleteQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	return runExec(ctx, q.ex, q)
}

// ReturningQuery is a DML statement whose RETURNING clause was set explicitly
// with Returning(...); only then does the statement yield rows, ended with
// All(ctx).Scan(&dst). Statements without Returning end with Exec.
type ReturningQuery struct {
	ex    Executor
	query sql.Query
}

// ToSQL compiles the statement.
func (q ReturningQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	return q.query.ToSQL(dialect)
}

// All prepares the statement for execution against the executor the chain
// was built from; Scan runs it and reads the returned rows.
func (q ReturningQuery) All(ctx context.Context) Rows {
	return newRows(ctx, q.ex, q.query)
}
