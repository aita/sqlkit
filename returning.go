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

// All prepares the statement for execution against the executor the chain
// was built from; Scan runs it and reads the returned rows.
func (q ReturningQuery) All(ctx context.Context) Rows {
	return newRows(ctx, q.ex, q.query)
}
