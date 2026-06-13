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

// Union combines two or more SELECT queries with UNION, flattening what would
// otherwise be a left-folded chain of SelectQuery.Union calls:
//
//	sqlkit.Union(
//		db.Select(Users.ID).From(Users),
//		db.Select(Admins.ID).From(Admins),
//		db.Select(Guests.ID).From(Guests),
//	).OrderBy(sql.Asc(sql.Col("id"))).All(ctx).Scan(&ids)
//
// The result is bound to the first operand's executor, so start the list with a
// query built from a Database or Session to run it. At least two operands are
// required, enforced by the signature.
func Union(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	return foldSetOp(first.Union(second), UnionQuery.Union, rest)
}

// UnionAll is Union with UNION ALL, keeping duplicate rows.
func UnionAll(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	return foldSetOp(first.UnionAll(second), UnionQuery.UnionAll, rest)
}

// Intersect combines two or more SELECT queries with INTERSECT, keeping rows
// present in every operand. See Union for operand and executor rules.
func Intersect(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	return foldSetOp(first.Intersect(second), UnionQuery.Intersect, rest)
}

// Except combines two or more SELECT queries with EXCEPT, keeping rows of the
// first operand absent from the rest. See Union for operand and executor rules.
//
// EXCEPT is left-associative: with operands a, b, c the result is
// (a EXCEPT b) EXCEPT c.
func Except(first, second SelectQuery, rest ...SelectQuery) UnionQuery {
	return foldSetOp(first.Except(second), UnionQuery.Except, rest)
}

// foldSetOp left-folds the remaining operands into an already-started compound,
// applying op (one of UnionQuery's set-operation methods) to each in turn.
func foldSetOp(q UnionQuery, op func(UnionQuery, SelectQuery) UnionQuery, rest []SelectQuery) UnionQuery {
	for _, r := range rest {
		q = op(q, r)
	}
	return q
}

// Union appends another UNION operand.
func (q UnionQuery) Union(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Union(other.query)}
}

// UnionAll appends another UNION ALL operand.
func (q UnionQuery) UnionAll(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.UnionAll(other.query)}
}

// Intersect appends another INTERSECT operand.
func (q UnionQuery) Intersect(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Intersect(other.query)}
}

// Except appends another EXCEPT operand.
func (q UnionQuery) Except(other SelectQuery) UnionQuery {
	return UnionQuery{ex: q.ex, query: q.query.Except(other.query)}
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

// CTE wraps this set-operation query as a common table expression named name,
// for use as a FROM source and column handle (typically a recursive CTE). Pass
// sql.Recursive() / sql.Columns(...) options to configure it.
func (q UnionQuery) CTE(name string, opts ...sql.CTEOption) sql.CTE {
	return q.query.CTE(name, opts...)
}

// As wraps this set-operation query as an aliased subquery (derived table), for
// use as a FROM/JOIN source and column handle; see SelectQuery.As.
func (q UnionQuery) As(alias string) sql.Subquery {
	return q.query.As(alias)
}

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
