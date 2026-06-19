package sql

// InsertStmt is the immutable AST for an INSERT statement (INSERT ... VALUES or
// INSERT ... SELECT). Exactly one of Rows or Select carries the inserted data;
// Conflict holds an optional upsert action and Returning an optional RETURNING.
// Build one with InsertInto; read its exported fields directly to introspect.
type InsertStmt struct {
	posField
	Table     Table
	Columns   []string
	Rows      [][]Expression
	Select    Query
	Conflict  *ConflictClause
	Returning []Selection
	// Replace marks a MySQL REPLACE INTO (delete-then-insert) rather than a plain
	// INSERT.
	Replace bool
}

// ToSQL compiles the INSERT statement.
func (q InsertStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q InsertStmt) compile(c *Compiler) (string, []any, error) {
	return c.CompileInsert(q)
}

// ColumnCount reports the number of columns each VALUES row binds, the per-row
// bind-parameter count used to size batches against a parameter limit. It is the
// explicit Columns list when set, else the width of the first row, else 0.
func (q InsertStmt) ColumnCount() int {
	if len(q.Columns) > 0 {
		return len(q.Columns)
	}
	if len(q.Rows) > 0 {
		return len(q.Rows[0])
	}
	return 0
}

// InsertBuilder is the fluent constructor for an InsertStmt. Construction lives
// here and the data on InsertStmt; each method copies the in-progress statement
// and returns a new builder, so a builder value can be branched and reused.
type InsertBuilder struct{ s InsertStmt }

// InsertInto creates an INSERT query builder for a table.
func InsertInto(table Table) InsertBuilder {
	return InsertBuilder{s: InsertStmt{Table: table}}
}

// ReplaceInto creates a REPLACE query builder for a table — MySQL's
// delete-then-insert upsert. It shares InsertBuilder's Columns/Values/FromSelect
// methods but rejects ON CONFLICT and RETURNING, and compiles only on MySQL.
func ReplaceInto(table Table) InsertBuilder {
	return InsertBuilder{s: InsertStmt{Table: table, Replace: true}}
}

// Stmt returns the built INSERT statement — the pure AST value.
func (b InsertBuilder) Stmt() InsertStmt { return b.s }

// Columns sets the column list for the INSERT statement.
func (b InsertBuilder) Columns(columns ...string) InsertBuilder {
	b.s.Columns = columns
	return b
}

// Values appends one row of values. Non-expression values become bind values.
func (b InsertBuilder) Values(values ...any) InsertBuilder {
	row := make([]Expression, len(values))
	for i, value := range values {
		row[i] = Expr(value)
	}
	b.s.Rows = append(append([][]Expression(nil), b.s.Rows...), row)
	return b
}

// FromSelect makes this an INSERT ... SELECT, inserting the rows produced by
// the query. It is mutually exclusive with Values; the column list is
// optional (SQL then assigns the query's columns in table order).
func (b InsertBuilder) FromSelect(query Query) InsertBuilder {
	b.s.Select = astOf(query)
	return b
}

// withConflict copies the pending conflict clause (creating one if absent),
// applies mut, and stores it, keeping value semantics so a reused builder is
// not mutated through a shared pointer.
func (b InsertBuilder) withConflict(mut func(*ConflictClause)) InsertBuilder {
	var c ConflictClause
	if b.s.Conflict != nil {
		c = *b.s.Conflict
	}
	mut(&c)
	b.s.Conflict = &c
	return b
}

// OnConflict starts an upsert clause targeting a column list — PostgreSQL's
// ON CONFLICT (cols) — and is followed by an action, DoNothing or DoUpdate:
//
//	InsertInto(t).Columns("email", "name").Values("a@x", "Alice").
//		OnConflict("email").DoUpdate(Set("name", Excluded("name")))
//
// Call it with no columns for MySQL's ON DUPLICATE KEY UPDATE, which has no
// conflict target. Without a following action the clause is DO NOTHING.
func (b InsertBuilder) OnConflict(columns ...string) InsertBuilder {
	return b.withConflict(func(c *ConflictClause) {
		c.Target, c.TargetExprs, c.TargetConstraint = columns, nil, ""
	})
}

// OnConflictConstraint starts an upsert clause targeting a named constraint
// (PostgreSQL ON CONFLICT ON CONSTRAINT). PostgreSQL/SQLite only.
func (b InsertBuilder) OnConflictConstraint(name string) InsertBuilder {
	return b.withConflict(func(c *ConflictClause) {
		c.TargetConstraint, c.Target, c.TargetExprs = name, nil, nil
	})
}

// OnConflictExpr starts an upsert clause targeting an expression unique index
// (PostgreSQL ON CONFLICT ((lower(email)))).
func (b InsertBuilder) OnConflictExpr(exprs ...Expression) InsertBuilder {
	return b.withConflict(func(c *ConflictClause) {
		c.TargetExprs, c.Target, c.TargetConstraint = exprs, nil, ""
	})
}

// DoNothing makes the upsert skip conflicting rows (DO NOTHING). PostgreSQL and
// SQLite only — MySQL has no DO NOTHING form.
func (b InsertBuilder) DoNothing() InsertBuilder {
	return b.withConflict(func(c *ConflictClause) { c.DoUpdate = false })
}

// DoUpdate makes the upsert update conflicting rows with the given assignments
// (DO UPDATE SET ...). Reference the proposed row's values with Excluded. Refine
// it with Where.
func (b InsertBuilder) DoUpdate(assignments ...Assignment) InsertBuilder {
	return b.withConflict(func(c *ConflictClause) {
		c.DoUpdate = true
		c.Sets = assignments
	})
}

// Where adds a predicate to the upsert's DO UPDATE action (PostgreSQL ON CONFLICT
// ... DO UPDATE ... WHERE) — the only WHERE an INSERT carries. Repeated calls AND
// together. It is ignored by DO NOTHING.
func (b InsertBuilder) Where(predicate Expression) InsertBuilder {
	return b.withConflict(func(c *ConflictClause) {
		if c.Where == nil {
			c.Where = predicate
		} else {
			c.Where = And(c.Where, predicate)
		}
	})
}

// Returning adds a RETURNING clause.
func (b InsertBuilder) Returning(selections ...Selection) InsertBuilder {
	b.s.Returning = append(append([]Selection(nil), b.s.Returning...), selections...)
	return b
}

// ColumnCount reports the per-row bind-parameter count; see InsertStmt.ColumnCount.
func (b InsertBuilder) ColumnCount() int { return b.s.ColumnCount() }

// SplitRows partitions a VALUES insert into copies of at most n rows each, every
// copy keeping the same column list, conflict, and returning clauses. It lets a
// large insert run as several statements without re-deriving the column set per
// chunk — the columns are already fixed on b. n <= 0, or a query with n or fewer
// VALUES rows, yields b unchanged as a single element.
func (b InsertBuilder) SplitRows(n int) []InsertBuilder {
	if n <= 0 || len(b.s.Rows) <= n {
		return []InsertBuilder{b}
	}
	out := make([]InsertBuilder, 0, (len(b.s.Rows)+n-1)/n)
	for start := 0; start < len(b.s.Rows); start += n {
		batch := b
		batch.s.Rows = b.s.Rows[start:min(start+n, len(b.s.Rows))]
		out = append(out, batch)
	}
	return out
}

// ToSQL compiles the building INSERT statement.
func (b InsertBuilder) ToSQL(dialect Dialect) (string, []any, error) { return b.s.ToSQL(dialect) }

func (b InsertBuilder) compile(c *Compiler) (string, []any, error) { return b.s.compile(c) }

func (b InsertBuilder) buildStmt() Query { return b.s }

// UpdateStmt is the immutable AST for an UPDATE statement.
type UpdateStmt struct {
	posField
	With      []CTE
	Table     Table
	Sets      []Assignment
	From      []Source
	Joins     []JoinClause
	Where     Expression
	OrderBy   []OrderTerm
	Limit     int
	HasLimit  bool
	Returning []Selection
}

// ToSQL compiles the UPDATE statement.
func (q UpdateStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q UpdateStmt) compile(c *Compiler) (string, []any, error) {
	return c.CompileUpdate(q)
}

// UpdateBuilder is the fluent constructor for an UpdateStmt.
type UpdateBuilder struct{ s UpdateStmt }

// Update creates an UPDATE query builder for a table.
func Update(table Table) UpdateBuilder {
	return UpdateBuilder{s: UpdateStmt{Table: table}}
}

// Stmt returns the built UPDATE statement — the pure AST value.
func (b UpdateBuilder) Stmt() UpdateStmt { return b.s }

// Set appends SET assignments.
func (b UpdateBuilder) Set(assignments ...Assignment) UpdateBuilder {
	b.s.Sets = append(append([]Assignment(nil), b.s.Sets...), assignments...)
	return b
}

// From adds correlated sources to the UPDATE (PostgreSQL UPDATE ... FROM /
// MySQL multi-table UPDATE). Combine with Where to correlate rows.
func (b UpdateBuilder) From(sources ...Source) UpdateBuilder {
	b.s.From = append(append([]Source(nil), b.s.From...), sources...)
	return b
}

// Join adds an inner join to the UPDATE's source list.
func (b UpdateBuilder) Join(source Source, on Expression) UpdateBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "JOIN", Source: source, On: on})
	return b
}

// LeftJoin adds a left join to the UPDATE's source list.
func (b UpdateBuilder) LeftJoin(source Source, on Expression) UpdateBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "LEFT JOIN", Source: source, On: on})
	return b
}

// Where adds a WHERE predicate, combined with AND when called repeatedly.
func (b UpdateBuilder) Where(expr Expression) UpdateBuilder {
	if b.s.Where == nil {
		b.s.Where = expr
	} else {
		b.s.Where = And(b.s.Where, expr)
	}
	return b
}

// WithCTE prepends common table expressions to the UPDATE.
// OrderBy adds ORDER BY terms that bound which rows an UPDATE touches
// (UPDATE ... ORDER BY ... LIMIT). MySQL only; other dialects reject it.
func (b UpdateBuilder) OrderBy(terms ...OrderTerm) UpdateBuilder {
	b.s.OrderBy = append(append([]OrderTerm(nil), b.s.OrderBy...), terms...)
	return b
}

// Limit caps the number of rows an UPDATE touches (UPDATE ... LIMIT n). MySQL
// only; other dialects reject it.
func (b UpdateBuilder) Limit(n int) UpdateBuilder {
	b.s.Limit, b.s.HasLimit = n, true
	return b
}

func (b UpdateBuilder) WithCTE(ctes ...CTE) UpdateBuilder {
	b.s.With = append(append([]CTE(nil), b.s.With...), ctes...)
	return b
}

// Returning adds a RETURNING clause.
func (b UpdateBuilder) Returning(selections ...Selection) UpdateBuilder {
	b.s.Returning = append(append([]Selection(nil), b.s.Returning...), selections...)
	return b
}

// ToSQL compiles the building UPDATE statement.
func (b UpdateBuilder) ToSQL(dialect Dialect) (string, []any, error) { return b.s.ToSQL(dialect) }

func (b UpdateBuilder) compile(c *Compiler) (string, []any, error) { return b.s.compile(c) }

func (b UpdateBuilder) buildStmt() Query { return b.s }

// DeleteStmt is the immutable AST for a DELETE statement.
type DeleteStmt struct {
	posField
	With      []CTE
	Table     Table
	Using     []Source
	Joins     []JoinClause
	Where     Expression
	OrderBy   []OrderTerm
	Limit     int
	HasLimit  bool
	Returning []Selection
}

// ToSQL compiles the DELETE statement.
func (q DeleteStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q DeleteStmt) compile(c *Compiler) (string, []any, error) {
	return c.CompileDelete(q)
}

// DeleteBuilder is the fluent constructor for a DeleteStmt.
type DeleteBuilder struct{ s DeleteStmt }

// DeleteFrom creates a DELETE query builder for a table.
func DeleteFrom(table Table) DeleteBuilder {
	return DeleteBuilder{s: DeleteStmt{Table: table}}
}

// Stmt returns the built DELETE statement — the pure AST value.
func (b DeleteBuilder) Stmt() DeleteStmt { return b.s }

// Using adds correlated sources to the DELETE (PostgreSQL DELETE ... USING /
// MySQL multi-table DELETE). Combine with Where to correlate rows.
func (b DeleteBuilder) Using(sources ...Source) DeleteBuilder {
	b.s.Using = append(append([]Source(nil), b.s.Using...), sources...)
	return b
}

// Join adds an inner join to the DELETE's source list.
func (b DeleteBuilder) Join(source Source, on Expression) DeleteBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "JOIN", Source: source, On: on})
	return b
}

// LeftJoin adds a left join to the DELETE's source list.
func (b DeleteBuilder) LeftJoin(source Source, on Expression) DeleteBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "LEFT JOIN", Source: source, On: on})
	return b
}

// Where adds a WHERE predicate, combined with AND when called repeatedly.
func (b DeleteBuilder) Where(expr Expression) DeleteBuilder {
	if b.s.Where == nil {
		b.s.Where = expr
	} else {
		b.s.Where = And(b.s.Where, expr)
	}
	return b
}

// WithCTE prepends common table expressions to the DELETE.
// OrderBy adds ORDER BY terms that bound which rows a DELETE removes
// (DELETE ... ORDER BY ... LIMIT). MySQL only; other dialects reject it.
func (b DeleteBuilder) OrderBy(terms ...OrderTerm) DeleteBuilder {
	b.s.OrderBy = append(append([]OrderTerm(nil), b.s.OrderBy...), terms...)
	return b
}

// Limit caps the number of rows a DELETE removes (DELETE ... LIMIT n). MySQL
// only; other dialects reject it.
func (b DeleteBuilder) Limit(n int) DeleteBuilder {
	b.s.Limit, b.s.HasLimit = n, true
	return b
}

func (b DeleteBuilder) WithCTE(ctes ...CTE) DeleteBuilder {
	b.s.With = append(append([]CTE(nil), b.s.With...), ctes...)
	return b
}

// Returning adds a RETURNING clause.
func (b DeleteBuilder) Returning(selections ...Selection) DeleteBuilder {
	b.s.Returning = append(append([]Selection(nil), b.s.Returning...), selections...)
	return b
}

// ToSQL compiles the building DELETE statement.
func (b DeleteBuilder) ToSQL(dialect Dialect) (string, []any, error) { return b.s.ToSQL(dialect) }

func (b DeleteBuilder) compile(c *Compiler) (string, []any, error) { return b.s.compile(c) }

func (b DeleteBuilder) buildStmt() Query { return b.s }
