package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

func TestColumnIsDistinctFrom(t *testing.T) {
	// IS DISTINCT FROM against a value and against another typed column.
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.Email.IsDistinctFrom("a@x.com")),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."email" IS DISTINCT FROM $1)`,
		[]any{"a@x.com"})

	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.IsNotDistinctFromExpr(Posts.UserID)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" IS NOT DISTINCT FROM "public"."posts"."user_id")`,
		nil)
}
