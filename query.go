package sqlkit

import (
	"context"
	stdsql "database/sql"

	"github.com/aita/sqlkit/sql"
)

// Query is any SQL statement this package compiles to text — a SELECT, a set
// operation, or an INSERT/UPDATE/DELETE, with or without RETURNING. Every
// builder satisfies it through ToSQL, so it is the umbrella to hold a
// statement whose only shared capability is being compiled. It is an alias for
// sql.Query. To also run the statement, use the row-reading Scannable /
// Selectable or the mutating Execable below.
type Query = sql.Query

// Scannable is a statement that produces rows to scan, read with
// All(ctx).Scan: a SELECT, a set operation (UNION and friends), or an
// INSERT/UPDATE/DELETE with a RETURNING clause. Hold one when a branch may
// build different row-producing statements that are all consumed the same way:
//
//	var q Scannable
//	if includeArchived {
//		q = db.Select().From(Users).UnionAll(db.Select().From(Archived))
//	} else {
//		q = db.Select().From(Users)
//	}
//	err := q.All(ctx).Scan(&users)
type Scannable interface {
	Query
	All(ctx context.Context) Rows
}

// Selectable is a SELECT-shaped query — a SELECT or a set operation — that also
// supports the single-row, paginated, and counting reads, not just All. It is
// the interface to reach for when a branch assigns either a SelectQuery or a
// UnionQuery and the caller still wants First/One/Page/Count:
//
//	var q Selectable = base
//	if onlyOne {
//		err = q.One(ctx).Scan(&u)
//	} else {
//		n, err = q.Count(ctx)
//	}
//
// ReturningQuery is deliberately only Scannable, not Selectable: a RETURNING
// statement yields its rows once through All, with no LIMIT/OFFSET or count of
// its own.
type Selectable interface {
	Scannable
	First(ctx context.Context) Rows
	One(ctx context.Context) Rows
	Page(ctx context.Context, limit, offset int) Rows
	Count(ctx context.Context) (int64, error)
}

// Execable is a statement run for its effect rather than its rows — an INSERT,
// UPDATE, or DELETE without RETURNING — executed with Exec(ctx). Hold one to
// run a mutation chosen at runtime:
//
//	var stmt Execable
//	if remove {
//		stmt = db.Delete(Users).Where(Users.ID.Eq(id))
//	} else {
//		stmt = db.Update(Users).Set(Users.Active.Set(false)).Where(Users.ID.Eq(id))
//	}
//	_, err := stmt.Exec(ctx)
type Execable interface {
	Query
	Exec(ctx context.Context) (stdsql.Result, error)
}

// unwrapQuery returns the underlying sql.Query for the typed builders so a
// subquery shares the outer statement's placeholder numbering, leaving any
// other Query (such as a raw sql.Query) untouched.
func unwrapQuery(query Query) sql.Query {
	switch q := query.(type) {
	case SelectQuery:
		return q.query
	case UnionQuery:
		return q.query
	default:
		return query
	}
}

// Compile-time checks that the concrete builders satisfy the interfaces. Every
// builder is a Query; reads add Scannable/Selectable, writes add Execable.
var (
	_ Query = SelectQuery{}
	_ Query = UnionQuery{}
	_ Query = ReturningQuery{}
	_ Query = InsertQuery{}
	_ Query = UpdateQuery{}
	_ Query = DeleteQuery{}

	_ Scannable = SelectQuery{}
	_ Scannable = UnionQuery{}
	_ Scannable = ReturningQuery{}

	_ Selectable = SelectQuery{}
	_ Selectable = UnionQuery{}

	_ Execable = InsertQuery{}
	_ Execable = UpdateQuery{}
	_ Execable = DeleteQuery{}
)
