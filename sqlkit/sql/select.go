package sql

// Query is implemented by SQL statements that can be compiled.
type Query interface {
	ToSQL(dialect Dialect) (string, []any, error)
}

// compilable is implemented by statements that can compile through a shared
// compiler, keeping bind placeholder numbering consistent across subqueries.
type compilable interface {
	compile(c *Compiler) (string, []any, error)
}

// SelectQuery is the runtime representation for a SELECT statement.
type SelectQuery struct {
	from      Source
	columns   []Selection
	joins     []JoinClause
	where     Expression
	groupBy   []Expression
	having    Expression
	orderBy   []OrderTerm
	limit     int
	hasLimit  bool
	offset    int
	hasOffset bool
	distinct  bool
}

// JoinClause represents a JOIN clause.
type JoinClause struct {
	Kind   string
	Source Source
	On     Expression
}

// Select creates a SELECT query builder.
func Select(columns ...Selection) SelectQuery {
	return SelectQuery{columns: columns}
}

// Distinct marks the query as SELECT DISTINCT.
func (q SelectQuery) Distinct() SelectQuery {
	q.distinct = true
	return q
}

// From sets the table or subquery for the SELECT query.
func (q SelectQuery) From(source Source) SelectQuery {
	q.from = source
	return q
}

// Join adds an INNER JOIN clause.
func (q SelectQuery) Join(source Source, on Expression) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "JOIN", Source: source, On: on})
	return q
}

// LeftJoin adds a LEFT JOIN clause.
func (q SelectQuery) LeftJoin(source Source, on Expression) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "LEFT JOIN", Source: source, On: on})
	return q
}

// Where adds a WHERE predicate, combined with AND when called repeatedly.
func (q SelectQuery) Where(expr Expression) SelectQuery {
	if q.where == nil {
		q.where = expr
	} else {
		q.where = And(q.where, expr)
	}
	return q
}

// GroupBy adds GROUP BY expressions.
func (q SelectQuery) GroupBy(exprs ...Expression) SelectQuery {
	q.groupBy = append(q.groupBy, exprs...)
	return q
}

// Having sets the HAVING predicate.
func (q SelectQuery) Having(expr Expression) SelectQuery {
	q.having = expr
	return q
}

// OrderBy adds ORDER BY terms.
func (q SelectQuery) OrderBy(terms ...OrderTerm) SelectQuery {
	q.orderBy = append(q.orderBy, terms...)
	return q
}

// Limit sets the LIMIT clause.
func (q SelectQuery) Limit(n int) SelectQuery {
	q.limit = n
	q.hasLimit = true
	return q
}

// Offset sets the OFFSET clause.
func (q SelectQuery) Offset(n int) SelectQuery {
	q.offset = n
	q.hasOffset = true
	return q
}

// SQLSource marks SelectQuery as a subquery source.
func (q SelectQuery) SQLSource() SourceExpression { return SourceExpression{Query: q} }

// ToSQL compiles the SELECT query.
func (q SelectQuery) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q SelectQuery) compile(c *Compiler) (string, []any, error) {
	return c.CompileSelect(q)
}
