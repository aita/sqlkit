package sqlkit

import (
	"context"
	stdsql "database/sql"

	"github.com/aita/sqlkit/sql"
)

// UpdateQuery is an UPDATE statement. Assignments come from typed columns, so
// it does not need to be generic over the row type.
type UpdateQuery struct {
	ex           Executor
	def          sql.TableDef
	query        sql.UpdateQuery
	returning    []sql.Selection
	hasReturning bool
}

// Update creates an unbound UPDATE builder, for ToSQL or tests. Assignments
// are created with Column.Set, so value types are checked at compile time.
// Executing it returns an error; start from a Database or Session to run it.
func Update(table Table) UpdateQuery {
	def := tableDef(table)
	return UpdateQuery{def: def, query: sql.Update(def.Table)}
}

// Set appends SET assignments.
func (q UpdateQuery) Set(assignments ...sql.Assignment) UpdateQuery {
	q.query = q.query.Set(assignments...)
	return q
}

// WithCTE prepends common table expressions to the UPDATE, emitting a leading
// WITH clause. Reference them from a WHERE subquery.
func (q UpdateQuery) WithCTE(ctes ...sql.CTE) UpdateQuery {
	q.query = q.query.WithCTE(ctes...)
	return q
}

// Where adds predicates, combined with AND.
func (q UpdateQuery) Where(predicates ...Predicate) UpdateQuery {
	if expr := combineWhere(predicates); expr != nil {
		q.query = q.query.Where(expr)
	}
	return q
}

// Returning adds an explicit RETURNING clause and makes the statement yield
// rows: end the chain with All(...).Scan to read them. With no arguments it
// returns all table columns.
func (q UpdateQuery) Returning(selections ...sql.Selection) ReturningQuery {
	q.returning = selections
	q.hasReturning = true
	return ReturningQuery{ex: q.ex, query: q}
}

// ToSQL compiles the UPDATE statement.
func (q UpdateQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	query := q.query
	if q.hasReturning {
		if len(q.returning) > 0 {
			query = query.Returning(q.returning...)
		} else {
			query = query.Returning(q.def.Selections()...)
		}
	}
	return query.ToSQL(dialect)
}

// Exec runs the UPDATE statement and returns the database/sql result.
func (q UpdateQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	return runExec(ctx, q.ex, q)
}
