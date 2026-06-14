package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// TestSetOperationChains covers the multi-operand Intersect and Except helpers,
// which left-fold the trailing operands onto the started compound (the Union
// helper is exercised elsewhere; this pins the other set operations).
func TestSetOperationChains(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)
	c := sqlkit.Select(Posts.UserID).From(Posts)

	assertToSQL(t, sqlkit.Intersect(a, b, c),
		`(SELECT "public"."users"."id" FROM "public"."users") INTERSECT (SELECT "public"."posts"."id" FROM "public"."posts") INTERSECT (SELECT "public"."posts"."user_id" FROM "public"."posts")`,
		nil)

	assertToSQL(t, sqlkit.Except(a, b, c),
		`(SELECT "public"."users"."id" FROM "public"."users") EXCEPT (SELECT "public"."posts"."id" FROM "public"."posts") EXCEPT (SELECT "public"."posts"."user_id" FROM "public"."posts")`,
		nil)
}

// TestUnionOrderLimitOffset covers the OrderBy/Limit/Offset modifiers applied to
// the combined result of a set operation.
func TestUnionOrderLimitOffset(t *testing.T) {
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)

	assertToSQL(t, sqlkit.Union(a, b).OrderBy(sql.Asc(sql.Col("id"))).Limit(5).Offset(2),
		`(SELECT "public"."users"."id" FROM "public"."users") UNION (SELECT "public"."posts"."id" FROM "public"."posts") ORDER BY "id" LIMIT 5 OFFSET 2`,
		nil)
}
