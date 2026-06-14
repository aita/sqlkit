package sql

// InsertQuery is the runtime representation for an INSERT statement, either
// INSERT ... VALUES or INSERT ... SELECT.
type InsertQuery struct {
	table     Table
	columns   []string
	rows      [][]Expression
	selectSrc Query
	conflict  *conflictClause
	returning []Selection
}

// conflictClause holds an upsert action: the conflict target — either a column
// list (target) or a named constraint (targetConstraint, PostgreSQL ON
// CONSTRAINT) — whether it is DO UPDATE (vs DO NOTHING), the SET assignments,
// and an optional WHERE.
type conflictClause struct {
	target           []string
	targetConstraint string
	doUpdate         bool
	sets             []Assignment
	where            Expression
}

// InsertInto creates an INSERT query builder for a table.
func InsertInto(table Table) InsertQuery {
	return InsertQuery{table: table}
}

// Columns sets the column list for the INSERT statement.
func (q InsertQuery) Columns(columns ...string) InsertQuery {
	q.columns = columns
	return q
}

// Values appends one row of values. Non-expression values become bind values.
func (q InsertQuery) Values(values ...any) InsertQuery {
	row := make([]Expression, len(values))
	for i, value := range values {
		row[i] = Expr(value)
	}
	q.rows = append(q.rows, row)
	return q
}

// FromSelect makes this an INSERT ... SELECT, inserting the rows produced by
// the query. It is mutually exclusive with Values; the column list is
// optional (SQL then assigns the query's columns in table order).
func (q InsertQuery) FromSelect(query Query) InsertQuery {
	q.selectSrc = query
	return q
}

// OnConflictDoNothing adds an upsert that skips conflicting rows: PostgreSQL
// ON CONFLICT [ (target...) ] DO NOTHING. target names the conflicting columns
// (optional on PostgreSQL). MySQL has no DO NOTHING form and rejects it at
// compile time.
func (q InsertQuery) OnConflictDoNothing(target ...string) InsertQuery {
	q.conflict = &conflictClause{target: target}
	return q
}

// OnConflictDoUpdate adds an upsert that updates conflicting rows: PostgreSQL
// ON CONFLICT (target...) DO UPDATE SET assignments, MySQL ON DUPLICATE KEY
// UPDATE assignments (which ignores target). Reference the proposed row's values
// with Excluded.
func (q InsertQuery) OnConflictDoUpdate(target []string, assignments ...Assignment) InsertQuery {
	q.conflict = &conflictClause{target: target, doUpdate: true, sets: assignments}
	return q
}

// OnConflictOnConstraintDoNothing adds an upsert that skips rows conflicting on
// a named constraint: PostgreSQL ON CONFLICT ON CONSTRAINT name DO NOTHING. It is
// PostgreSQL/SQLite-only (MySQL infers the key and cannot name a constraint).
func (q InsertQuery) OnConflictOnConstraintDoNothing(constraint string) InsertQuery {
	q.conflict = &conflictClause{targetConstraint: constraint}
	return q
}

// OnConflictOnConstraintDoUpdate adds an upsert that updates rows conflicting on
// a named constraint: PostgreSQL ON CONFLICT ON CONSTRAINT name DO UPDATE SET
// assignments. Reference the proposed row's values with Excluded. It is
// PostgreSQL/SQLite-only.
func (q InsertQuery) OnConflictOnConstraintDoUpdate(constraint string, assignments ...Assignment) InsertQuery {
	q.conflict = &conflictClause{targetConstraint: constraint, doUpdate: true, sets: assignments}
	return q
}

// OnConflictWhere adds a predicate to the most recent DO UPDATE action
// (PostgreSQL ON CONFLICT ... DO UPDATE SET ... WHERE ...). It has no effect
// without a preceding OnConflictDoUpdate, and is unsupported on MySQL.
func (q InsertQuery) OnConflictWhere(expr Expression) InsertQuery {
	if q.conflict != nil {
		clause := *q.conflict
		clause.where = expr
		q.conflict = &clause
	}
	return q
}

// Returning adds a RETURNING clause.
func (q InsertQuery) Returning(selections ...Selection) InsertQuery {
	q.returning = append(q.returning, selections...)
	return q
}

// ColumnCount reports the number of columns each VALUES row binds, the per-row
// bind-parameter count used to size batches against a parameter limit. It is the
// explicit Columns list when set, else the width of the first row, else 0.
func (q InsertQuery) ColumnCount() int {
	if len(q.columns) > 0 {
		return len(q.columns)
	}
	if len(q.rows) > 0 {
		return len(q.rows[0])
	}
	return 0
}

// SplitRows partitions a VALUES insert into copies of at most n rows each, every
// copy keeping the same column list, conflict, and returning clauses. It lets a
// large insert run as several statements without re-deriving the column set per
// chunk — the columns are already fixed on q. n <= 0, or a query with n or fewer
// VALUES rows, yields q unchanged as a single element.
func (q InsertQuery) SplitRows(n int) []InsertQuery {
	if n <= 0 || len(q.rows) <= n {
		return []InsertQuery{q}
	}
	out := make([]InsertQuery, 0, (len(q.rows)+n-1)/n)
	for start := 0; start < len(q.rows); start += n {
		batch := q
		batch.rows = q.rows[start:min(start+n, len(q.rows))]
		out = append(out, batch)
	}
	return out
}

// ToSQL compiles the INSERT query.
func (q InsertQuery) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q InsertQuery) compile(c *Compiler) (string, []any, error) {
	return c.CompileInsert(q)
}

// UpdateQuery is the runtime representation for an UPDATE statement.
type UpdateQuery struct {
	with      []CTE
	table     Table
	sets      []Assignment
	from      []Source
	joins     []JoinClause
	where     Expression
	returning []Selection
}

// Update creates an UPDATE query builder for a table.
func Update(table Table) UpdateQuery {
	return UpdateQuery{table: table}
}

// Set appends SET assignments.
func (q UpdateQuery) Set(assignments ...Assignment) UpdateQuery {
	q.sets = append(q.sets, assignments...)
	return q
}

// From adds correlated sources to the UPDATE (PostgreSQL UPDATE ... FROM /
// MySQL multi-table UPDATE). Combine with Where to correlate rows.
func (q UpdateQuery) From(sources ...Source) UpdateQuery {
	q.from = append(q.from, sources...)
	return q
}

// Join adds an inner join to the UPDATE's source list.
func (q UpdateQuery) Join(source Source, on Expression) UpdateQuery {
	q.joins = append(q.joins, JoinClause{Kind: "JOIN", Source: source, On: on})
	return q
}

// LeftJoin adds a left join to the UPDATE's source list.
func (q UpdateQuery) LeftJoin(source Source, on Expression) UpdateQuery {
	q.joins = append(q.joins, JoinClause{Kind: "LEFT JOIN", Source: source, On: on})
	return q
}

// Where adds a WHERE predicate, combined with AND when called repeatedly.
func (q UpdateQuery) Where(expr Expression) UpdateQuery {
	if q.where == nil {
		q.where = expr
	} else {
		q.where = And(q.where, expr)
	}
	return q
}

// Returning adds a RETURNING clause.
func (q UpdateQuery) Returning(selections ...Selection) UpdateQuery {
	q.returning = append(q.returning, selections...)
	return q
}

// ToSQL compiles the UPDATE query.
func (q UpdateQuery) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q UpdateQuery) compile(c *Compiler) (string, []any, error) {
	return c.CompileUpdate(q)
}

// DeleteQuery is the runtime representation for a DELETE statement.
type DeleteQuery struct {
	with      []CTE
	table     Table
	using     []Source
	joins     []JoinClause
	where     Expression
	returning []Selection
}

// DeleteFrom creates a DELETE query builder for a table.
func DeleteFrom(table Table) DeleteQuery {
	return DeleteQuery{table: table}
}

// Using adds correlated sources to the DELETE (PostgreSQL DELETE ... USING /
// MySQL multi-table DELETE). Combine with Where to correlate rows.
func (q DeleteQuery) Using(sources ...Source) DeleteQuery {
	q.using = append(q.using, sources...)
	return q
}

// Join adds an inner join to the DELETE's source list.
func (q DeleteQuery) Join(source Source, on Expression) DeleteQuery {
	q.joins = append(q.joins, JoinClause{Kind: "JOIN", Source: source, On: on})
	return q
}

// LeftJoin adds a left join to the DELETE's source list.
func (q DeleteQuery) LeftJoin(source Source, on Expression) DeleteQuery {
	q.joins = append(q.joins, JoinClause{Kind: "LEFT JOIN", Source: source, On: on})
	return q
}

// Where adds a WHERE predicate, combined with AND when called repeatedly.
func (q DeleteQuery) Where(expr Expression) DeleteQuery {
	if q.where == nil {
		q.where = expr
	} else {
		q.where = And(q.where, expr)
	}
	return q
}

// Returning adds a RETURNING clause.
func (q DeleteQuery) Returning(selections ...Selection) DeleteQuery {
	q.returning = append(q.returning, selections...)
	return q
}

// ToSQL compiles the DELETE query.
func (q DeleteQuery) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q DeleteQuery) compile(c *Compiler) (string, []any, error) {
	return c.CompileDelete(q)
}
