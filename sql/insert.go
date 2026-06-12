package sql

// InsertQuery is the runtime representation for an INSERT statement, either
// INSERT ... VALUES or INSERT ... SELECT.
type InsertQuery struct {
	table     Table
	columns   []string
	rows      [][]Expression
	selectSrc Query
	returning []Selection
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

// Returning adds a RETURNING clause.
func (q InsertQuery) Returning(selections ...Selection) InsertQuery {
	q.returning = append(q.returning, selections...)
	return q
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
	table     Table
	sets      []Assignment
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
	table     Table
	where     Expression
	returning []Selection
}

// DeleteFrom creates a DELETE query builder for a table.
func DeleteFrom(table Table) DeleteQuery {
	return DeleteQuery{table: table}
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
