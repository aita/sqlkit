package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

func TestSelectJoinUsingAndNatural(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).JoinUsing(Posts, "id"),
		`SELECT "public"."users"."id" FROM "public"."users" JOIN "public"."posts" USING ("id")`, nil)

	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).NaturalLeftJoin(Posts),
		`SELECT "public"."users"."id" FROM "public"."users" NATURAL LEFT JOIN "public"."posts"`, nil)
}

func TestSelectDistinctOn(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Posts.UserID, Posts.Title).From(Posts).
			DistinctOn(Posts.UserID).
			OrderBy(Posts.UserID.Asc(), Posts.ID.Desc()),
		`SELECT DISTINCT ON ("public"."posts"."user_id") "public"."posts"."user_id", "public"."posts"."title" `+
			`FROM "public"."posts" ORDER BY "public"."posts"."user_id", "public"."posts"."id" DESC`, nil)
}

func TestColumnSimilarTo(t *testing.T) {
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(sqlkit.SimilarTo(Users.Email, "%@example.com")),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" SIMILAR TO $1)`,
		[]any{"%@example.com"})
}
