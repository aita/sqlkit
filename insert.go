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
	ex           executorBinding
	def          sql.TableDef
	rows         []any
	selectSrc    sql.Query
	columns      []AnyColumn
	conflict     *insertConflict
	returning    []sql.Selection
	hasReturning bool
	batchSize    int  // AutoChunk(ChunkSize): max rows per statement
	chunkOff     bool // AutoChunk(ChunkOff): run as one statement
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

// AutoChunk configures how a Values-based INSERT is split into several statements
// when executed: ChunkSize(n) caps each statement at n rows, ChunkOff() runs it as
// one statement. With no ChunkSize, the dialect's bind-parameter limit still
// sizes each statement to floor(limit / columns) so it never exceeds the limit;
// an explicit ChunkSize can only make statements smaller.
//
// It applies to Exec/Returning only: ToSQL always compiles the full statement, and
// it has no effect on FromSelect. Split statements run atomically — one transaction
// through a Database, the session's transaction through a Session.
func (q InsertQuery) AutoChunk(opts ...ChunkOption) InsertQuery {
	q.batchSize, q.chunkOff = applyChunkOptions(q.batchSize, q.chunkOff, opts)
	return q
}

// BatchSize splits a Values-based INSERT into statements of at most n rows.
//
// Deprecated: use AutoChunk(ChunkSize(n)).
func (q InsertQuery) BatchSize(n int) InsertQuery {
	return q.AutoChunk(ChunkSize(n))
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

// Exec runs the INSERT statement and returns the database/sql result. The rows
// are split into several statements when BatchSize is set or the dialect's
// bind-parameter limit would otherwise be
// exceeded; the returned result then aggregates the batches: RowsAffected sums
// every batch and LastInsertId reports the final batch. Split statements run
// atomically — in one transaction through a Database, joining the session's
// transaction through a Session.
func (q InsertQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	if q.ex == nil {
		return nil, errNoExecutor
	}
	// runExec consults splitStatements; a split runs the chunks atomically and
	// merges the result, otherwise the statement runs as one.
	return runExec(ctx, q.ex, q)
}

// effectiveBatchSize combines the explicit BatchSize with the executor's
// bind-parameter limit: the limit caps each statement at floor(limit / columns)
// rows so the parameters never exceed it, and an explicit BatchSize can only
// lower that. It returns 0 when neither bounds the row count (a single
// statement).
func (q InsertQuery) effectiveBatchSize(columns int) int {
	batch := q.batchSize
	// maxBindParams is already 0 when auto-split is off, so the explicit BatchSize
	// (if any) survives untouched; otherwise the limit caps it.
	if limit := q.ex.maxBindParams(); limit > 0 && columns > 0 {
		perStmt := limit / columns
		if perStmt < 1 {
			perStmt = 1
		}
		if batch <= 0 || perStmt < batch {
			batch = perStmt
		}
	}
	return batch
}

// splitStatements implements splitter: it splits a bulk Values INSERT into one
// compiled statement per chunk of rows, each carrying the same columns, conflict,
// and (when hasReturning) RETURNING clauses, which SplitRows preserves. The
// statement is built once so the mapped columns and the defaulted-and-zero omission
// mask derive from all rows (preserving non-batched Values semantics). It reports
// ok=false — running the statement unchanged — for ChunkOff, FromSelect, no rows, or
// when the rows already fit one statement. Inserts are always writes.
func (q InsertQuery) splitStatements(ex executorBinding) ([]compiledStmt, bool, bool, error) {
	if q.chunkOff || q.selectSrc != nil || len(q.rows) == 0 {
		return nil, false, true, nil
	}
	insert, err := q.build()
	if err != nil {
		return nil, false, true, err
	}
	if q.conflict != nil {
		insert = q.applyConflict(insert)
	}
	if q.hasReturning {
		insert = insert.Returning(q.returningSelections()...)
	}
	batch := q.effectiveBatchSize(insert.ColumnCount())
	if batch <= 0 || len(q.rows) <= batch {
		return nil, false, true, nil
	}
	dialect := ex.sqlDialect()
	parts := insert.SplitRows(batch)
	stmts := make([]compiledStmt, 0, len(parts))
	for _, part := range parts {
		sqlText, args, err := part.ToSQL(dialect)
		if err != nil {
			return nil, false, true, err
		}
		stmts = append(stmts, compiledStmt{sql: sqlText, args: args})
	}
	return stmts, true, true, nil
}
