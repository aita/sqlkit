package sqlkit

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
	"github.com/aita/sqlkit/sql"
)

// UpdateQuery is an UPDATE statement. Assignments come from typed columns, so
// it does not need to be generic over the row type.
type UpdateQuery struct {
	ex           Executor
	def          sql.TableDef
	query        sql.UpdateQuery
	returning    []sql.Selection
	hasReturning bool
	err          error
	// batch holds a lone column IN back from the WHERE so Exec can split its
	// value list across statements when the bind-parameter limit would be
	// exceeded; finalQuery folds it back in for ToSQL and RETURNING.
	batch *batchIn
	// chunkSize/chunkOff are the per-query AutoChunk settings for splitting the
	// deferred IN: a positive chunkSize caps the values per statement; chunkOff
	// forces a single statement.
	chunkSize int
	chunkOff  bool
}

// Update creates an unbound UPDATE builder, for ToSQL or tests. Assignments
// are created with Column.Set, so value types are checked at compile time.
// Executing it returns an error; start from a Database or Session to run it.
func Update(table Table) UpdateQuery {
	def := tableDef(table)
	return UpdateQuery{def: def, query: sql.Update(def.Table)}
}

// AutoChunk configures how a lone WHERE column IN whose value list exceeds the
// executor's bind-parameter limit is split across statements: ChunkSize(n) caps
// each statement at n values, ChunkOff() runs it as one statement. The chunks run
// atomically and their RowsAffected sum; ToSQL is unaffected.
func (q UpdateQuery) AutoChunk(opts ...ChunkOption) UpdateQuery {
	q.chunkSize, q.chunkOff = applyChunkOptions(q.chunkSize, q.chunkOff, opts)
	return q
}

// Set appends SET assignments.
func (q UpdateQuery) Set(assignments ...sql.Assignment) UpdateQuery {
	q.query = q.query.Set(assignments...)
	return q
}

// SetStruct appends SET assignments taken from a struct's fields, mapping
// fields to columns by name the same way Insert().Values and Scan do (sqlkit
// tag, exact name, or snake_case). It is the UPDATE counterpart of
// Insert().Values for a single row, and composes with Set: assignments accumulate.
//
// With no columns, every column the struct maps to is assigned except the
// primary key, which identifies the row and belongs in Where rather than SET.
// Pass columns to restrict the update to just those — a partial update that
// leaves every other column untouched; a primary key column is included when
// named explicitly. Unlike a non-zero "patch", zero-valued fields are written,
// so a column can be cleared to its zero value.
//
// SetStruct only builds SET assignments; pair it with Where to choose the rows:
//
//	db.Update(Users).SetStruct(u, Users.Name, Users.Email).Where(Users.ID.Eq(u.ID)).Exec(ctx)
//
// row may be a struct or a pointer to one. A field-mapping failure (not a
// struct, a nil pointer, or an explicitly named column with no matching field)
// is deferred and returned by ToSQL and Exec.
func (q UpdateQuery) SetStruct(row any, columns ...AnyColumn) UpdateQuery {
	assignments, err := q.structAssignments(row, columns)
	if err != nil {
		if q.err == nil {
			q.err = err
		}
		return q
	}
	q.query = q.query.Set(assignments...)
	return q
}

// structAssignments resolves a struct's fields against the target columns and
// builds SET assignments in target-column order.
func (q UpdateQuery) structAssignments(row any, columns []AnyColumn) ([]sql.Assignment, error) {
	rv := reflect.ValueOf(row)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("sqlkit: SetStruct row is a nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("sqlkit: SetStruct requires a struct, got %T", row)
	}
	fields := reflectmap.FieldsByColumn(rv.Type())

	explicit := len(columns) > 0
	var targets []string
	if explicit {
		targets = columnNames(columns)
	} else {
		pk := make(map[string]bool, len(q.def.PrimaryKey))
		for _, name := range q.def.PrimaryKey {
			pk[name] = true
		}
		for _, name := range q.def.Columns {
			if !pk[name] {
				targets = append(targets, name)
			}
		}
	}

	assignments := make([]sql.Assignment, 0, len(targets))
	for _, name := range targets {
		path, ok := fields[strings.ToLower(name)]
		if !ok {
			if explicit {
				return nil, fmt.Errorf("sqlkit: SetStruct: no field of %s maps to column %q of %s",
					rv.Type(), name, q.def.Table.QualifiedName())
			}
			continue
		}
		value := rv.FieldByIndex(path).Interface()
		assignments = append(assignments, sql.Set(name, sql.Val(value)))
	}
	if len(assignments) == 0 {
		return nil, fmt.Errorf("sqlkit: SetStruct: no fields of %s map to columns of %s",
			rv.Type(), q.def.Table.QualifiedName())
	}
	return assignments, nil
}

// WithCTE prepends common table expressions to the UPDATE, emitting a leading
// WITH clause. Reference them from a WHERE subquery.
func (q UpdateQuery) WithCTE(ctes ...sql.CTE) UpdateQuery {
	q.query = q.query.WithCTE(ctes...)
	return q
}

// From adds correlated sources to the UPDATE (PostgreSQL UPDATE ... FROM /
// MySQL multi-table UPDATE); correlate them with Where.
func (q UpdateQuery) From(sources ...sql.Source) UpdateQuery {
	q.query = q.query.From(sources...)
	return q
}

// Join joins source on the predicate. It renders as an inline join on MySQL and
// as a correlated FROM with the predicate folded into WHERE on PostgreSQL.
func (q UpdateQuery) Join(source sql.Source, on Predicate) UpdateQuery {
	q.query = q.query.Join(source, on.Expression())
	return q
}

// LeftJoin is Join with a LEFT JOIN (MySQL inline form only).
func (q UpdateQuery) LeftJoin(source sql.Source, on Predicate) UpdateQuery {
	q.query = q.query.LeftJoin(source, on.Expression())
	return q
}

// JoinRel correlates the UPDATE to a relation's target on its foreign-key
// condition (PostgreSQL UPDATE ... FROM / MySQL inline JOIN), so callers never
// spell out the ON clause.
func (q UpdateQuery) JoinRel(rel Relation) UpdateQuery {
	return q.Join(rel.Target(), rel.JoinOn())
}

// Where adds predicates, combined with AND. A lone column IN whose value
// list is large enough to exceed the executor's bind-parameter limit is held
// back and split across statements by Exec; see WithAutoChunk.
func (q UpdateQuery) Where(predicates ...Predicate) UpdateQuery {
	flush, fold, deferred := resolveWhere(predicates, q.batch)
	if flush != nil {
		q.query = q.query.Where(flush)
	}
	if fold != nil {
		q.query = q.query.Where(fold)
	}
	q.batch = deferred
	return q
}

// finalQuery folds any deferred IN back into the query, for ToSQL and RETURNING
// (every use other than Exec's splitting path).
func (q UpdateQuery) finalQuery() sql.UpdateQuery {
	if q.batch == nil {
		return q.query
	}
	return q.query.Where(q.batch.exprFor(q.batch.values))
}

// Returning adds an explicit RETURNING clause and makes the statement yield
// rows: end the chain with All(...).Scan to read them. With no arguments it
// returns all table columns.
func (q UpdateQuery) Returning(selections ...sql.Selection) ReturningQuery {
	q.returning = selections
	q.hasReturning = true
	return ReturningQuery{ex: q.ex, query: q}
}

// ToSQL compiles the UPDATE statement. A deferred IN is folded into a single
// statement carrying the full value list, independent of any configured
// bind-parameter limit (which only affects Exec).
func (q UpdateQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	if q.err != nil {
		return "", nil, q.err
	}
	query := q.finalQuery()
	if q.hasReturning {
		query = query.Returning(q.returningSelections()...)
	}
	return query.ToSQL(dialect)
}

// returningSelections resolves the RETURNING columns: the explicit ones, or every
// table column when Returning was called with no arguments.
func (q UpdateQuery) returningSelections() []sql.Selection {
	if len(q.returning) > 0 {
		return q.returning
	}
	return q.def.Selections()
}

// Exec runs the UPDATE statement and returns the database/sql result. When a
// lone column IN in the WHERE carries more values than the executor's
// bind-parameter limit allows, the value list is split
// across several statements run atomically (one transaction through a Database,
// the session's transaction through a Session); the returned result then sums
// their RowsAffected.
func (q UpdateQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.ex == nil {
		return nil, errNoExecutor
	}
	// runExec consults splitStatements; a split runs the chunks atomically and
	// sums their RowsAffected, otherwise the statement runs as one.
	return runExec(ctx, q.ex, q)
}

// splitStatements implements splitter: it splits a lone WHERE column IN whose
// value list exceeds the executor's bind-parameter limit into one statement per
// chunk, each carrying the RETURNING clause when set. It honors AutoChunk
// (ChunkSize caps the chunk; ChunkOff returns ok=false). Updates are always writes.
func (q UpdateQuery) splitStatements(ex Executor) ([]compiledStmt, bool, bool, error) {
	if q.err != nil {
		return nil, false, true, q.err
	}
	if q.chunkOff {
		return nil, false, true, nil
	}
	dialect := ex.sqlDialect()
	stmts, ok, err := splitInStatements(ex, q.batch, q.chunkSize, func(in sql.Expression) (string, []any, error) {
		query := q.query
		if in != nil {
			query = query.Where(in)
		}
		if q.hasReturning {
			query = query.Returning(q.returningSelections()...)
		}
		return query.ToSQL(dialect)
	})
	return stmts, ok, true, err
}
