package sqlkit

import (
	"context"
	stdsql "database/sql"

	"github.com/aita/sqlkit/sql"
)

// MergeQuery is a typed MERGE builder (SQL:2003, PostgreSQL 15+): it matches
// rows of a target table against a USING data source on a join condition and
// applies a list of WHEN clauses. Build it from a Database or Session, set the
// source with Using and the join with On, then add WHEN clauses with When:
//
//	db.Merge(Users).
//		Using(staging).
//		On(Cond(sql.Eq(Users.ID.Reference(), staging.Col("id")))).
//		When(sqlkit.WhenMatched().ThenUpdate(Users.Name.Set("x"))).
//		When(sqlkit.WhenNotMatched().ThenInsert([]string{"id", "name"}, 1, "x")).
//		Exec(ctx)
//
// MERGE is PostgreSQL/standard-SQL only; other dialects error at compile time.
type MergeQuery struct {
	ex    executorBinding
	query sql.MergeBuilder
}

// MergeWhen is one WHEN clause of a MERGE — a match kind, an optional AND
// condition, and the action to perform. It is an alias for sql.MergeWhen; build
// it with WhenMatched, WhenNotMatched, or WhenNotMatchedBySource.
type MergeWhen = sql.MergeWhen

// WhenMatched starts a WHEN MATCHED clause, applied to rows present in both the
// target and the source. Its action is ThenUpdate, ThenDelete, or ThenDoNothing.
func WhenMatched() MergeWhen { return sql.WhenMatched() }

// WhenNotMatched starts a WHEN NOT MATCHED [BY TARGET] clause, applied to source
// rows with no target match. Its action is ThenInsert or ThenDoNothing.
func WhenNotMatched() MergeWhen { return sql.WhenNotMatched() }

// WhenNotMatchedBySource starts a WHEN NOT MATCHED BY SOURCE clause, applied to
// target rows with no source match. Its action is ThenUpdate, ThenDelete, or
// ThenDoNothing.
func WhenNotMatchedBySource() MergeWhen { return sql.WhenNotMatchedBySource() }

// Merge creates an unbound MERGE builder, for ToSQL or tests. Executing it
// returns an error; start from a Database or Session to run it.
func Merge(target Table) MergeQuery {
	return MergeQuery{query: sql.MergeInto(tableDef(target).Table)}
}

// As sets an alias for the target table (MERGE INTO target AS alias).
func (q MergeQuery) As(alias string) MergeQuery {
	q.query = q.query.As(alias)
	return q
}

// Using sets the data source matched against the target — a table or subquery,
// optionally aliased with sql.SourceAlias/sql.Alias.
func (q MergeQuery) Using(source sql.Source) MergeQuery {
	q.query = q.query.Using(source)
	return q
}

// On sets the join condition that pairs source rows with target rows.
func (q MergeQuery) On(condition Predicate) MergeQuery {
	q.query = q.query.On(condition.Expression())
	return q
}

// When appends WHEN clauses, evaluated in order for each row.
func (q MergeQuery) When(whens ...MergeWhen) MergeQuery {
	q.query = q.query.When(whens...)
	return q
}

// WithCTE prepends common table expressions to the MERGE, emitting a leading
// WITH clause.
func (q MergeQuery) WithCTE(ctes ...sql.CTE) MergeQuery {
	q.query = q.query.WithCTE(ctes...)
	return q
}

// Returning adds an explicit RETURNING clause (PostgreSQL 17+) and makes the
// statement yield rows: end the chain with All(...).Scan to read them.
func (q MergeQuery) Returning(selections ...sql.Selection) ReturningQuery {
	return ReturningQuery{ex: q.ex, query: q.query.Returning(selections...)}
}

// ToSQL compiles the MERGE statement.
func (q MergeQuery) ToSQL(dialect sql.Dialect) (string, []any, error) {
	return q.query.ToSQL(dialect)
}

// Exec runs the MERGE statement and returns the database/sql result.
func (q MergeQuery) Exec(ctx context.Context, opts ...ExecOption) (stdsql.Result, error) {
	if q.ex == nil {
		return nil, errNoExecutor
	}
	ex, _ := applyExecOptions(q.ex, opts)
	return runExec(ctx, ex, q.query)
}
