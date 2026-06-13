package sqlkit

import (
	"context"
	stdsql "database/sql"

	"github.com/aita/sqlkit/sql"
)

// DeleteQuery is a DELETE statement.
type DeleteQuery struct {
	ex           Executor
	def          sql.TableDef
	query        sql.DeleteQuery
	returning    []sql.Selection
	hasReturning bool
}

// Delete creates an unbound DELETE builder, for ToSQL or tests. Executing it
// returns an error; start from a Database or Session to run it.
func Delete(table Table) DeleteQuery {
	def := tableDef(table)
	return DeleteQuery{def: def, query: sql.DeleteFrom(def.Table)}
}

// Using adds correlated sources to the DELETE (PostgreSQL DELETE ... USING /
// MySQL multi-table DELETE); correlate them with Where.
func (q DeleteQuery) Using(sources ...sql.Source) DeleteQuery {
	q.query = q.query.Using(sources...)
	return q
}

// Join joins source on the predicate. It renders as an inline join on MySQL and
// as a correlated USING with the predicate folded into WHERE on PostgreSQL.
func (q DeleteQuery) Join(source sql.Source, on Predicate) DeleteQuery {
	q.query = q.query.Join(source, on.Expression())
	return q
}

// LeftJoin is Join with a LEFT JOIN (MySQL inline form only).
func (q DeleteQuery) LeftJoin(source sql.Source, on Predicate) DeleteQuery {
	q.query = q.query.LeftJoin(source, on.Expression())
	return q
}

// UsingRel correlates the DELETE to a relation's target on its foreign-key
// condition (PostgreSQL DELETE ... USING / MySQL multi-table DELETE), so callers
// never spell out the ON clause.
func (q DeleteQuery) UsingRel(rel Relation) DeleteQuery {
	return q.Join(rel.Target(), rel.JoinOn())
}

// Where adds predicates, combined with AND.
func (q DeleteQuery) Where(predicates ...Predicate) DeleteQuery {
	if expr := combineWhere(predicates); expr != nil {
		q.query = q.query.Where(expr)
	}
	return q
}

// WithCTE prepends common table expressions to the DELETE, emitting a leading
// WITH clause. Reference them from a WHERE subquery.
func (q DeleteQuery) WithCTE(ctes ...sql.CTE) DeleteQuery {
	q.query = q.query.WithCTE(ctes...)
	return q
}

// Returning adds an explicit RETURNING clause and makes the statement yield
// rows: end the chain with All(...).Scan to read them. With no arguments it
// returns all table columns.
func (q DeleteQuery) Returning(selections ...sql.Selection) ReturningQuery {
	q.returning = selections
	q.hasReturning = true
	return ReturningQuery{ex: q.ex, query: q}
}

// ToSQL compiles the DELETE statement.
func (q DeleteQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
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

// Exec runs the DELETE statement and returns the database/sql result.
func (q DeleteQuery) Exec(ctx context.Context) (stdsql.Result, error) {
	return runExec(ctx, q.ex, q)
}
