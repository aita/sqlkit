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
	with       []CTE
	from       Source
	columns    []Selection
	joins      []JoinClause
	where      Expression
	groupBy    []Expression
	having     Expression
	orderBy    []OrderTerm
	limit      int
	hasLimit   bool
	offset     int
	hasOffset  bool
	distinct   bool
	distinctOn []Expression
	windows    []NamedWindow
}

// JoinClause represents a JOIN clause. Its condition is given in exactly one
// way: On for a JOIN ... ON predicate, Using for a JOIN ... USING (columns) on
// equally named columns, or Natural for a NATURAL JOIN that matches every
// equally named column implicitly (in which case On and Using are empty). A
// CROSS JOIN carries none of them.
type JoinClause struct {
	Kind    string
	Source  Source
	On      Expression
	Using   []string
	Natural bool
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

// DistinctOn marks the query as SELECT DISTINCT ON (exprs...), keeping the first
// row of each group of rows equal on the given expressions (PostgreSQL). It is a
// PostgreSQL extension: dialects without it reject the query at compile time.
// Pair it with an ORDER BY that begins with the same expressions to make "first"
// deterministic.
func (q SelectQuery) DistinctOn(exprs ...Expression) SelectQuery {
	q.distinctOn = append(append([]Expression(nil), q.distinctOn...), exprs...)
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

// RightJoin adds a RIGHT JOIN clause.
func (q SelectQuery) RightJoin(source Source, on Expression) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "RIGHT JOIN", Source: source, On: on})
	return q
}

// FullJoin adds a FULL OUTER JOIN clause.
func (q SelectQuery) FullJoin(source Source, on Expression) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "FULL JOIN", Source: source, On: on})
	return q
}

// CrossJoin adds a CROSS JOIN clause. A cross join has no ON condition; it
// produces the Cartesian product of its inputs.
func (q SelectQuery) CrossJoin(source Source) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "CROSS JOIN", Source: source})
	return q
}

// JoinUsing adds an INNER JOIN ... USING (columns) clause, matching rows on the
// equally named columns (which collapse to a single output column each).
func (q SelectQuery) JoinUsing(source Source, columns ...string) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "JOIN", Source: source, Using: columns})
	return q
}

// LeftJoinUsing adds a LEFT JOIN ... USING (columns) clause.
func (q SelectQuery) LeftJoinUsing(source Source, columns ...string) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "LEFT JOIN", Source: source, Using: columns})
	return q
}

// NaturalJoin adds a NATURAL JOIN clause, an inner join on every column the two
// sources share by name. It takes no condition.
func (q SelectQuery) NaturalJoin(source Source) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "JOIN", Source: source, Natural: true})
	return q
}

// NaturalLeftJoin adds a NATURAL LEFT JOIN clause.
func (q SelectQuery) NaturalLeftJoin(source Source) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "LEFT JOIN", Source: source, Natural: true})
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

// Window adds named window definitions to the SELECT's WINDOW clause, referenced
// from an OVER clause with WindowExpression.Named, e.g.
// Window(WindowDef("w").PartitionBy(Col("team")).OrderBy(Desc(Col("score")))).
func (q SelectQuery) Window(defs ...NamedWindow) SelectQuery {
	q.windows = append(append([]NamedWindow(nil), q.windows...), defs...)
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
