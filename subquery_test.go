package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

func TestTypedSelectFromSubquery(t *testing.T) {
	sub := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Gt(10)).As("sub")
	query := sqlkit.Select(sub.Col("id")).From(sub)

	assertToSQL(t, query,
		`SELECT "sub"."id" FROM (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" > $1)) AS "sub"`,
		[]any{int64(10)})
}

func TestTypedUnionAsSubquery(t *testing.T) {
	sub := sqlkit.Union(
		sqlkit.Select(Users.ID).From(Users),
		sqlkit.Select(Posts.ID).From(Posts),
	).As("ids")
	query := sqlkit.Select(sub.Col("id")).From(sub)

	assertToSQL(t, query,
		`SELECT "ids"."id" FROM ((SELECT "public"."users"."id" FROM "public"."users") UNION (SELECT "public"."posts"."id" FROM "public"."posts")) AS "ids"`,
		nil)
}
