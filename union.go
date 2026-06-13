package sqlkit

import (
	"context"

	"github.com/aita/sqlkit/sql"
)

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

// Page is All over a LIMIT/OFFSET window of the combined result; see
// SelectQuery.Page.
func (q UnionQuery) Page(ctx context.Context, limit, offset int) Rows {
	return newRows(ctx, q.ex, q.query.Limit(limit).Offset(offset))
}

// Count runs SELECT COUNT(*) over the combined result (wrapped as a subquery,
// so the set operation applies) and returns the number of rows; see
// SelectQuery.Count.
func (q UnionQuery) Count(ctx context.Context) (int64, error) {
	count := sql.Select(sql.CountAll()).From(sql.Alias(q.query, "q"))
	var n int64
	err := newRows(ctx, q.ex, count).Scan(&n)
	return n, err
}
