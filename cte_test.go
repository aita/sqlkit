package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

func TestTypedSelectWithCTE(t *testing.T) {
	active := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Gt(10)).CTE("active")
	query := sqlkit.Select(active.Col("id")).From(active).WithCTE(active)

	assertToSQL(t, query,
		`WITH "active" AS (SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" > $1)) SELECT "active"."id" FROM "active"`,
		[]any{int64(10)})
}

// A recursive CTE: build the body with UnionAll first, then convert with the
// Recursive option, referencing the CTE by name in the recursive term.
func TestTypedSelectWithRecursiveCTE(t *testing.T) {
	base := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.Eq(1))
	step := sqlkit.Select(sql.Col("users.id")).
		From(Users).
		Join(sql.Tbl("tree"), sqlkit.Cond(sql.Eq(sql.Col("users.parent_id"), sql.Col("tree.id"))))
	tree := base.UnionAll(step).CTE("tree", sql.Recursive(), sql.Columns("id"))
	query := sqlkit.Select(tree.Col("id")).From(tree).WithCTE(tree)

	assertToSQL(t, query,
		`WITH RECURSIVE "tree" ("id") AS ((SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" = $1)) UNION ALL (SELECT "users"."id" FROM "public"."users" JOIN "tree" ON ("users"."parent_id" = "tree"."id"))) SELECT "tree"."id" FROM "tree"`,
		[]any{int64(1)})
}
