package sqlkit

import (
	"context"

	"github.com/aita/sqlkit/sql"
)

// columnSource is implemented by sources (generated tables) that can list
// their columns, so Select with no explicit columns can expand to them.
type columnSource interface {
	Columns() []AnyColumn
}

// SelectQuery is a typed SELECT builder. Columns and predicates are typed via
// Column[T], so type mismatches are compile errors. Results are materialized
// into a caller-defined destination with All(ctx).Scan(&dst).
//
// Build it from a Database or a Session — the chain is bound to that
// executor — then set the source with From before adding clauses:
//
//	db.Select().From(Users).Where(Users.ID.Gt(10)).All(ctx).Scan(&dst)
type SelectQuery struct {
	ex         Executor
	selections []sql.Selection
	hasCols    bool
	query      sql.SelectQuery
}

// Select starts an unbound SELECT builder for use as a subquery (FromSelect,
// Union) or with ToSQL. With no arguments it selects every column of the From
// source; otherwise it selects the given columns or expressions (use sql.As
// to alias projections). Executing an unbound chain returns an error; start
// from a Database or Session to run it.
func Select(selections ...sql.Selection) SelectQuery {
	return SelectQuery{selections: selections, hasCols: len(selections) > 0}
}

// From sets the table or subquery to select from. It must be called before
// the other clause methods. When Select was given no columns and the source
// can list its columns, they are selected explicitly; otherwise SELECT *.
func (q SelectQuery) From(source sql.Source) SelectQuery {
	selections := q.selections
	if !q.hasCols {
		selections = allColumnsOf(source)
	}
	q.query = sql.Select(selections...).From(source)
	return q
}

// Where adds predicates, combined with AND.
func (q SelectQuery) Where(predicates ...Predicate) SelectQuery {
	if expr := combineWhere(predicates); expr != nil {
		q.query = q.query.Where(expr)
	}
	return q
}

// Join adds an INNER JOIN.
func (q SelectQuery) Join(source sql.Source, on Predicate) SelectQuery {
	q.query = q.query.Join(source, on.Expression())
	return q
}

// LeftJoin adds a LEFT JOIN.
func (q SelectQuery) LeftJoin(source sql.Source, on Predicate) SelectQuery {
	q.query = q.query.LeftJoin(source, on.Expression())
	return q
}

// GroupBy adds GROUP BY expressions (columns or sql expressions).
func (q SelectQuery) GroupBy(exprs ...sql.Expression) SelectQuery {
	q.query = q.query.GroupBy(exprs...)
	return q
}

// Having adds a HAVING predicate.
func (q SelectQuery) Having(predicate Predicate) SelectQuery {
	q.query = q.query.Having(predicate.Expression())
	return q
}

// OrderBy adds ORDER BY terms.
func (q SelectQuery) OrderBy(terms ...sql.OrderTerm) SelectQuery {
	q.query = q.query.OrderBy(terms...)
	return q
}

// Limit sets the LIMIT clause.
func (q SelectQuery) Limit(n int) SelectQuery {
	q.query = q.query.Limit(n)
	return q
}

// Offset sets the OFFSET clause.
func (q SelectQuery) Offset(n int) SelectQuery {
	q.query = q.query.Offset(n)
	return q
}

// Distinct marks the query as SELECT DISTINCT.
func (q SelectQuery) Distinct() SelectQuery {
	q.query = q.query.Distinct()
	return q
}

// SQLSource marks the query as a subquery source.
func (q SelectQuery) SQLSource() SourceExpression { return q.query.SQLSource() }

// ToSQL compiles the query.
func (q SelectQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	return q.query.ToSQL(dialect)
}

// All prepares the query for execution against the executor the chain was
// built from. The query runs when Scan is called; build and execution errors
// surface there.
func (q SelectQuery) All(ctx context.Context) Rows {
	return newRows(ctx, q.ex, q.query)
}

// First is All limited to the first row: LIMIT 1 is pushed into the query,
// and Scan into a struct or scalar yields that row, or ErrNoRows when the
// result is empty.
func (q SelectQuery) First(ctx context.Context) Rows {
	return newRows(ctx, q.ex, q.query.Limit(1))
}

// One is All restricted to exactly one row: Scan yields the row when the
// result has exactly one, ErrNoRows when it is empty, and ErrTooManyRows
// when there are more. LIMIT 2 is pushed into the query, so detecting the
// excess row never reads past the second.
func (q SelectQuery) One(ctx context.Context) Rows {
	rows := newRows(ctx, q.ex, q.query.Limit(2))
	rows.exactlyOne = true
	return rows
}

// Count runs SELECT COUNT(*) over this query (as a subquery, so joins,
// grouping, and predicates apply) and returns the number of matching rows.
func (q SelectQuery) Count(ctx context.Context) (int64, error) {
	count := sql.Select(sql.CountAll()).From(sql.Alias(q.query, "q"))
	var n int64
	err := newRows(ctx, q.ex, count).Scan(&n)
	return n, err
}

// Union combines this query with another using UNION. The result stays bound
// to this chain's executor.
func (q SelectQuery) Union(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Union(other.query)}
}

// UnionAll combines this query with another using UNION ALL.
func (q SelectQuery) UnionAll(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.UnionAll(other.query)}
}

// Intersect combines this query with another using INTERSECT.
func (q SelectQuery) Intersect(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Intersect(other.query)}
}

// Except combines this query with another using EXCEPT.
func (q SelectQuery) Except(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Except(other.query)}
}

// UnionQuery is a set operation over SELECT queries, materialized the same way
// with All(ctx).Scan(&dst).
type UnionQuery struct {
	ex    Executor
	query sql.CompoundQuery
}

// Union appends another UNION operand.
func (q UnionQuery) Union(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Union(other.query)}
}

// UnionAll appends another UNION ALL operand.
func (q UnionQuery) UnionAll(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.UnionAll(other.query)}
}

// OrderBy adds ORDER BY terms applied to the combined result. In a set
// operation, ORDER BY must reference the result's output columns by name or
// position (e.g. sql.Asc(sql.Col("email"))), not table-qualified columns.
func (q UnionQuery) OrderBy(terms ...sql.OrderTerm) UnionQuery {
	q.query = q.query.OrderBy(terms...)
	return q
}

// Limit sets the LIMIT clause of the combined result.
func (q UnionQuery) Limit(n int) UnionQuery {
	q.query = q.query.Limit(n)
	return q
}

// Offset sets the OFFSET clause of the combined result.
func (q UnionQuery) Offset(n int) UnionQuery {
	q.query = q.query.Offset(n)
	return q
}

// SQLSource marks the query as a subquery source.
func (q UnionQuery) SQLSource() SourceExpression { return q.query.SQLSource() }

// ToSQL compiles the query.
func (q UnionQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	return q.query.ToSQL(dialect)
}

// All prepares the set operation for execution against the executor the
// chain was built from.
func (q UnionQuery) All(ctx context.Context) Rows {
	return newRows(ctx, q.ex, q.query)
}

// First is All limited to the first row of the combined result; see
// SelectQuery.First.
func (q UnionQuery) First(ctx context.Context) Rows {
	return newRows(ctx, q.ex, q.query.Limit(1))
}

// One is All restricted to exactly one row of the combined result; see
// SelectQuery.One.
func (q UnionQuery) One(ctx context.Context) Rows {
	rows := newRows(ctx, q.ex, q.query.Limit(2))
	rows.exactlyOne = true
	return rows
}

func allColumnsOf(source sql.Source) []sql.Selection {
	if cs, ok := source.(columnSource); ok {
		return selectionsOf(cs.Columns())
	}
	return nil
}

func selectionsOf(columns []AnyColumn) []sql.Selection {
	selections := make([]sql.Selection, len(columns))
	for i, column := range columns {
		selections[i] = column
	}
	return selections
}
