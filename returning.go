package sqlkit

import (
	"context"

	"github.com/aita/sqlkit/sql"
)

// ReturningQuery is an INSERT, UPDATE, or DELETE statement whose RETURNING
// clause was set explicitly with Returning(...); only then does the statement
// yield rows, ended with All(ctx).Scan(&dst). Statements without Returning end
// with Exec.
type ReturningQuery struct {
	ex    Executor
	query sql.Query
}

// ToSQL compiles the statement.
func (q ReturningQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	return q.query.ToSQL(dialect)
}

// All prepares the statement for execution against the executor the chain was
// built from; Scan runs it and reads the returned rows. When the underlying write
// would exceed the executor's bind-parameter limit, newRows splits it (via the
// builder's splitStatements) into several chunks — each carrying the RETURNING
// clause — run atomically as writes, and their rows are concatenated, mirroring the
// split SELECT read path.
func (q ReturningQuery) All(ctx context.Context) Rows {
	return newRows(ctx, q.ex, q.query)
}
