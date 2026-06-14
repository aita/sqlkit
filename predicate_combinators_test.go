package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

// TestPredicateMethodCombinators covers the And/Or methods on Predicate, which
// combine a receiver predicate with others.
func TestPredicateMethodCombinators(t *testing.T) {
	id := `"public"."users"."id"`
	where(t, Users.ID.Eq(1).And(Users.ID.Eq(2)),
		`((`+id+` = $1) AND (`+id+` = $2))`, []any{int64(1), int64(2)})
	where(t, Users.ID.Eq(1).Or(Users.ID.Eq(2)),
		`((`+id+` = $1) OR (`+id+` = $2))`, []any{int64(1), int64(2)})
}

// TestPredicateFuncCombinators covers the package-level Or and Not (And is
// already exercised by the WHERE folding tests).
func TestPredicateFuncCombinators(t *testing.T) {
	id := `"public"."users"."id"`
	where(t, sqlkit.Or(Users.ID.Eq(1), Users.ID.Eq(2)),
		`((`+id+` = $1) OR (`+id+` = $2))`, []any{int64(1), int64(2)})
	where(t, sqlkit.Not(Users.ID.Eq(1)), `(NOT (`+id+` = $1))`, []any{int64(1)})
}

// TestInValuesEscapeHatch covers InValues, the untyped chunkable IN built from a
// raw column expression rather than a typed Column.
func TestInValuesEscapeHatch(t *testing.T) {
	where(t, sqlkit.InValues(Users.ID, 1, 2, 3),
		`("public"."users"."id" IN ($1, $2, $3))`, []any{1, 2, 3})
}

// TestNotExistsPredicate covers NotExists (Exists is exercised elsewhere).
func TestNotExistsPredicate(t *testing.T) {
	sub := sqlkit.Select(Posts.UserID).From(Posts).Where(Posts.UserID.EqExpr(Users.ID))
	where(t, sqlkit.NotExists(sub),
		`(NOT EXISTS (SELECT "public"."posts"."user_id" FROM "public"."posts" WHERE ("public"."posts"."user_id" = "public"."users"."id")))`,
		nil)
}
