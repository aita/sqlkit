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
	ex           executorBinding
	base         Table // the target table, kept so Filter can resolve its columns
	def          sql.TableDef
	query        sql.UpdateBuilder
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
	// versionGuard records that CheckVersion added an optimistic-lock guard, so
	// Exec reports ErrVersionConflict when the statement matches no row.
	versionGuard bool
}

// Update creates an unbound UPDATE builder, for ToSQL or tests. Assignments
// are created with Column.Set, so value types are checked at compile time.
// Executing it returns an error; start from a Database or Session to run it.
func Update(table Table) UpdateQuery {
	def := tableDef(table)
	return UpdateQuery{base: table, def: def, query: sql.Update(def.Table)}
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
// primary key, which identifies the row and belongs in Where rather than SET,
// and except read-only fields (`sqlkit:",readonly"`, for database-generated
// columns). Pass columns to restrict the update to just those — a partial update
// that leaves every other column untouched; a primary key or read-only column is
// included when named explicitly. Unlike a non-zero "patch", zero-valued fields
// are written, so a column can be cleared to its zero value.
//
// A field tagged `sqlkit:",version"` enables optimistic locking on an implicit
// update: its column is bumped (col = col + 1) and the row is guarded on the
// field's current value, so Exec returns ErrVersionConflict when another writer
// advanced it first — the tag-driven equivalent of CheckVersion. A field tagged
// `sqlkit:",json"` is written as its JSON encoding.
//
// SetStruct only builds SET assignments (plus the version guard above); pair it
// with Where to choose the rows:
//
//	db.Update(Users).SetStruct(u, Users.Name, Users.Email).Where(Users.ID.Eq(u.ID)).Exec(ctx)
//
// row may be a struct or a pointer to one. A field-mapping failure (not a
// struct, a nil pointer, or an explicitly named column with no matching field)
// is deferred and returned by ToSQL and Exec.
func (q UpdateQuery) SetStruct(row any, columns ...AnyColumn) UpdateQuery {
	assignments, vcol, vcur, err := q.structAssignments(row, columns)
	if err != nil {
		if q.err == nil {
			q.err = err
		}
		return q
	}
	q.query = q.query.Set(assignments...)
	// A version-tagged field turns the implicit update into an optimistic-locked
	// one: the SET already bumps the column, and this guards the row on its
	// current value so a concurrently advanced row matches nothing and Exec
	// reports ErrVersionConflict.
	if vcol != "" {
		q.versionGuard = true
		q = q.Where(Cond(sql.Eq(sql.Col(vcol), sql.Val(vcur))))
	}
	return q
}

// structAssignments resolves a struct's fields against the target columns and
// builds SET assignments in target-column order. When a version-tagged field is
// part of an implicit update it is returned separately (versionCol/versionCur)
// so SetStruct can add the optimistic-lock guard rather than a plain assignment.
func (q UpdateQuery) structAssignments(row any, columns []AnyColumn) (assignments []sql.Assignment, versionCol string, versionCur any, err error) {
	rv := reflect.ValueOf(row)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, "", nil, fmt.Errorf("sqlkit: SetStruct row is a nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, "", nil, fmt.Errorf("sqlkit: SetStruct requires a struct, got %T", row)
	}
	fields := reflectmap.Fields(rv.Type())

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

	assignments = make([]sql.Assignment, 0, len(targets))
	for _, name := range targets {
		f, ok := fields[strings.ToLower(name)]
		if !ok {
			if explicit {
				return nil, "", nil, fmt.Errorf("sqlkit: SetStruct: no field of %s maps to column %q of %s",
					rv.Type(), name, q.def.Table.QualifiedName())
			}
			continue
		}
		// Read-only columns (database-generated) are never assigned in an
		// implicit update; name them explicitly to override.
		if f.ReadOnly && !explicit {
			continue
		}
		value := rv.FieldByIndex(f.Path).Interface()
		// A version field in an implicit update is bumped, not copied, and its
		// current value becomes the optimistic-lock guard.
		if f.Version && !explicit {
			assignments = append(assignments, sql.Set(name, sql.Add(sql.Col(name), sql.Val(1))))
			versionCol, versionCur = name, value
			continue
		}
		if f.JSON {
			value = jsonValue{v: value}
		}
		assignments = append(assignments, sql.Set(name, sql.Val(value)))
	}
	if len(assignments) == 0 {
		return nil, "", nil, fmt.Errorf("sqlkit: SetStruct: no fields of %s map to columns of %s",
			rv.Type(), q.def.Table.QualifiedName())
	}
	return assignments, versionCol, versionCur, nil
}

// CheckVersion enables optimistic locking on the UPDATE using col as the
// version column. It guards the statement with WHERE col = current and bumps
// SET col = col + 1, so a row another transaction already advanced past current
// matches nothing. Exec then returns ErrVersionConflict when the statement
// affects no row — the row was modified concurrently (or no longer exists) —
// letting the caller reload and retry:
//
//	res, err := db.Update(Users).SetStruct(u, Users.Name).
//	    Where(Users.ID.Eq(u.ID)).CheckVersion(Users.Version, u.Version).Exec(ctx)
//	if errors.Is(err, sqlkit.ErrVersionConflict) { /* reload and retry */ }
//
// Pair it with Where to identify the row (typically by primary key); the version
// guard is ANDed onto that predicate. The version column is referenced unqualified,
// so CheckVersion targets a single-table UPDATE.
func (q UpdateQuery) CheckVersion(col AnyColumn, current any) UpdateQuery {
	name := col.ColumnName()
	q.query = q.query.Set(sql.Set(name, sql.Add(sql.Col(name), sql.Val(1))))
	q.versionGuard = true
	return q.Where(Cond(sql.Eq(sql.Col(name), sql.Val(current))))
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

// Filter narrows the UPDATE with a reusable filter bundle applied against the
// target table's columns: either a Predicate, or a struct whose set fields map
// to columns by name and hold FilterTerm values (sqlkit.Le(20), …) or
// predicates. It is the UPDATE counterpart of SelectQuery.Filter, so one filter
// bundle narrows a read or a write alike. A mapping error is deferred to the
// terminal.
func (q UpdateQuery) Filter(filter any) UpdateQuery {
	predicates, err := buildFilter(q.base, filter)
	if err != nil {
		if q.err == nil {
			q.err = err
		}
		return q
	}
	return q.Where(predicates...)
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
func (q UpdateQuery) finalQuery() sql.UpdateBuilder {
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
	res, err := runExec(ctx, q.ex, q)
	if err != nil || !q.versionGuard {
		return res, err
	}
	// An optimistic-lock guard (CheckVersion) matched no row: another writer
	// advanced the version first, or the row is gone. Report the conflict.
	affected, err := res.RowsAffected()
	if err != nil {
		return res, err
	}
	if affected == 0 {
		return res, ErrVersionConflict
	}
	return res, nil
}

// splitStatements implements splitter: it splits a lone WHERE column IN whose
// value list exceeds the executor's bind-parameter limit into one statement per
// chunk, each carrying the RETURNING clause when set. It honors AutoChunk
// (ChunkSize caps the chunk; ChunkOff returns ok=false). Updates are always writes.
func (q UpdateQuery) splitStatements(ex executorBinding) ([]compiledStmt, bool, bool, error) {
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
