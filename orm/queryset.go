package orm

import (
	"context"
	"fmt"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// QuerySet is a relation-aware, deferred query over a base table, in the style
// of a Django QuerySet / SQLAlchemy Query. It is the Query side of CQRS: build
// up clauses with the chainable methods, then run a terminal (All, First, …)
// that returns sqlkit.Rows for the caller to Scan into their own DTO.
//
// A QuerySet is an immutable value: each method returns a new QuerySet, so a
// partially-built query can be shared and specialized safely. It does not carry
// a row type — DTO mapping is the caller's concern.
type QuerySet struct {
	ex    sqlkit.Executor
	base  sqlkit.Table
	query sqlkit.SelectQuery
	err   error // deferred build error, surfaced at the terminal
}

// NewQuerySet starts a QuerySet selecting every column of base, bound to ex.
// Generated Queryer/Q methods call it; it is also a usable constructor for
// hand-written bindings.
func NewQuerySet(ex sqlkit.Executor, base sqlkit.Table) QuerySet {
	return QuerySet{ex: ex, base: base, query: ex.Select().From(base)}
}

// fail returns a copy carrying err, keeping the first error if one is set.
func (qs QuerySet) fail(err error) QuerySet {
	if qs.err == nil {
		qs.err = err
	}
	return qs
}

// Where adds predicates, combined with AND (and ANDed with any earlier Where).
func (qs QuerySet) Where(predicates ...sqlkit.Predicate) QuerySet {
	qs.query = qs.query.Where(predicates...)
	return qs
}

// Join adds an INNER JOIN to the relation's target on its foreign-key
// condition, so callers never spell out the ON clause.
func (qs QuerySet) Join(rel Relation) QuerySet {
	qs.query = qs.query.Join(rel.Target(), rel.JoinOn())
	return qs
}

// LeftJoin adds a LEFT JOIN to the relation's target.
func (qs QuerySet) LeftJoin(rel Relation) QuerySet {
	qs.query = qs.query.LeftJoin(rel.Target(), rel.JoinOn())
	return qs
}

// Filter applies a reusable filter bundle: either a sqlkit.Predicate, or a
// struct whose set fields map to columns by name and hold FilterTerm values
// (orm.Le(20), …) or predicates. A mapping error is deferred to the terminal.
func (qs QuerySet) Filter(filter any) QuerySet {
	predicates, err := buildFilter(qs.base, filter)
	if err != nil {
		return qs.fail(err)
	}
	qs.query = qs.query.Where(predicates...)
	return qs
}

// OrderBy adds ORDER BY terms.
func (qs QuerySet) OrderBy(terms ...sql.OrderTerm) QuerySet {
	qs.query = qs.query.OrderBy(terms...)
	return qs
}

// Limit sets the LIMIT clause.
func (qs QuerySet) Limit(n int) QuerySet {
	qs.query = qs.query.Limit(n)
	return qs
}

// Offset sets the OFFSET clause.
func (qs QuerySet) Offset(n int) QuerySet {
	qs.query = qs.query.Offset(n)
	return qs
}

// Distinct marks the query as SELECT DISTINCT.
func (qs QuerySet) Distinct() QuerySet {
	qs.query = qs.query.Distinct()
	return qs
}

// All prepares the query and runs it when Scan is called.
func (qs QuerySet) All(ctx context.Context) sqlkit.Rows {
	if qs.err != nil {
		return sqlkit.ErrRows(qs.err)
	}
	return qs.query.All(ctx)
}

// First is All limited to the first row.
func (qs QuerySet) First(ctx context.Context) sqlkit.Rows {
	if qs.err != nil {
		return sqlkit.ErrRows(qs.err)
	}
	return qs.query.First(ctx)
}

// One is All restricted to exactly one row (ErrNoRows / ErrTooManyRows
// otherwise).
func (qs QuerySet) One(ctx context.Context) sqlkit.Rows {
	if qs.err != nil {
		return sqlkit.ErrRows(qs.err)
	}
	return qs.query.One(ctx)
}

// Page is All over a LIMIT/OFFSET window.
func (qs QuerySet) Page(ctx context.Context, limit, offset int) sqlkit.Rows {
	if qs.err != nil {
		return sqlkit.ErrRows(qs.err)
	}
	return qs.query.Page(ctx, limit, offset)
}

// Count returns the number of matching rows.
func (qs QuerySet) Count(ctx context.Context) (int64, error) {
	if qs.err != nil {
		return 0, qs.err
	}
	return qs.query.Count(ctx)
}

// Build is the escape hatch: it returns the underlying sqlkit.SelectQuery,
// which exposes ToSQL, All, Union, and the rest of the typed builder. It panics
// if a deferred Filter error is pending, since SelectQuery has no way to carry
// it; check it first with Err when mixing Filter and Build.
func (qs QuerySet) Build() sqlkit.SelectQuery {
	if qs.err != nil {
		panic(fmt.Sprintf("orm: Build on a QuerySet with a pending error: %v", qs.err))
	}
	return qs.query
}

// Err reports a deferred build error (e.g. from Filter), if any.
func (qs QuerySet) Err() error { return qs.err }

// ToSQL compiles the query without executing it.
func (qs QuerySet) ToSQL(dialect sql.Dialect) (string, []any, error) {
	if qs.err != nil {
		return "", nil, qs.err
	}
	return qs.query.ToSQL(dialect)
}

// Insert starts an INSERT into the base table, bound to this QuerySet's
// executor. Feed it rows with Values (or a query with FromSelect).
func (qs QuerySet) Insert() sqlkit.InsertQuery { return qs.ex.Insert(qs.base) }

// Update starts an UPDATE of the base table. It carries no clauses from the
// QuerySet; add assignments with Set and a filter with Where.
func (qs QuerySet) Update() sqlkit.UpdateQuery { return qs.ex.Update(qs.base) }

// Delete starts a DELETE from the base table. It carries no WHERE from the
// QuerySet; add one with Where.
func (qs QuerySet) Delete() sqlkit.DeleteQuery { return qs.ex.Delete(qs.base) }

// UpdateJoin starts an UPDATE of the base table correlated to rel's target on
// its foreign-key condition (PostgreSQL UPDATE ... FROM / MySQL inline JOIN,
// chosen by dialect at compile time). Add assignments with Set and narrow with
// Where; it carries no clauses from the QuerySet.
func (qs QuerySet) UpdateJoin(rel Relation) sqlkit.UpdateQuery {
	return qs.ex.Update(qs.base).Join(rel.Target(), rel.JoinOn())
}

// DeleteUsing starts a DELETE from the base table correlated to rel's target on
// its foreign-key condition (PostgreSQL DELETE ... USING / MySQL multi-table
// DELETE). Narrow with Where; it carries no clauses from the QuerySet.
func (qs QuerySet) DeleteUsing(rel Relation) sqlkit.DeleteQuery {
	return qs.ex.Delete(qs.base).Join(rel.Target(), rel.JoinOn())
}

// Get fetches the row matching the primary key and returns the first result.
// Pass one value per primary-key column, in declaration order.
func (qs QuerySet) Get(ctx context.Context, pk ...any) sqlkit.Rows {
	if qs.err != nil {
		return sqlkit.ErrRows(qs.err)
	}
	key := qs.base.PrimaryKey()
	if len(key) == 0 {
		return sqlkit.ErrRows(fmt.Errorf("orm: Get on %s, which has no primary key", qs.base.Info().QualifiedName()))
	}
	if len(pk) != len(key) {
		return sqlkit.ErrRows(fmt.Errorf("orm: Get on %s expects %d primary-key value(s), got %d",
			qs.base.Info().QualifiedName(), len(key), len(pk)))
	}
	predicates := make([]sqlkit.Predicate, len(key))
	for i, col := range key {
		predicates[i] = sqlkit.Cond(sql.Eq(col, sql.Val(pk[i])))
	}
	return qs.query.Where(predicates...).First(ctx)
}
