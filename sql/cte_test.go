package sql_test

import (
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestSelectWithCTE(t *testing.T) {
	active := Select(Col("users.id")).
		From(Table{Name: "users"}).
		Where(Eq(Col("users.active"), true)).
		CTE("active")
	query := Select(active.Col("id")).From(active).WithCTE(active)

	assertSQL(t, query,
		`WITH "active" AS (SELECT "users"."id" FROM "users" WHERE ("users"."active" = $1)) SELECT "active"."id" FROM "active"`,
		[]any{true})
}

func TestSelectWithCTEColumnList(t *testing.T) {
	totals := Select(Col("org_id"), Count(Col("id"))).
		From(Table{Name: "users"}).
		GroupBy(Col("org_id")).
		CTE("t", Columns("org_id", "n"))
	query := Select(totals.Col("org_id"), totals.Col("n")).From(totals).WithCTE(totals)

	assertSQL(t, query,
		`WITH "t" ("org_id", "n") AS (SELECT "org_id", COUNT("id") FROM "users" GROUP BY "org_id") SELECT "t"."org_id", "t"."n" FROM "t"`,
		nil)
}

// Multiple CTEs are emitted in the order passed to WithCTE, and their bodies
// receive the leading placeholder ordinals.
func TestSelectWithMultipleCTEsShareNumbering(t *testing.T) {
	a := Select(Col("id"), Col("flag")).From(Table{Name: "users"}).Where(Eq(Col("kind"), Int(1))).CTE("a")
	b := Select(Col("id")).From(Table{Name: "orgs"}).Where(Eq(Col("kind"), Int(2))).CTE("b")
	query := Select(CountAll()).
		From(a).
		Join(b, Eq(a.Col("id"), b.Col("id"))).
		Where(Eq(a.Col("flag"), Int(3))).
		WithCTE(a, b)

	assertSQL(t, query,
		`WITH "a" AS (SELECT "id", "flag" FROM "users" WHERE ("kind" = $1)), "b" AS (SELECT "id" FROM "orgs" WHERE ("kind" = $2)) SELECT COUNT(*) FROM "a" JOIN "b" ON ("a"."id" = "b"."id") WHERE ("a"."flag" = $3)`,
		[]any{1, 2, 3})
}

func TestSelectWithRecursiveCTE(t *testing.T) {
	base := Select(Col("id"), Col("parent_id")).
		From(Table{Name: "nodes"}).
		Where(IsNull(Col("parent_id")))
	step := Select(Col("nodes.id"), Col("nodes.parent_id")).
		From(Tbl("nodes")).
		Join(Tbl("tree"), Eq(Col("nodes.parent_id"), Col("tree.id")))
	tree := base.UnionAll(step).CTE("tree", Recursive(), Columns("id", "parent_id"))
	query := Select(tree.Col("id")).From(tree).WithCTE(tree)

	assertSQL(t, query,
		`WITH RECURSIVE "tree" ("id", "parent_id") AS ((SELECT "id", "parent_id" FROM "nodes" WHERE ("parent_id" IS NULL)) UNION ALL (SELECT "nodes"."id", "nodes"."parent_id" FROM "nodes" JOIN "tree" ON ("nodes"."parent_id" = "tree"."id"))) SELECT "tree"."id" FROM "tree"`,
		nil)
}

// A CTE referenced through a WHERE subquery works because WithCTE is explicit.
func TestUpdateWithCTE(t *testing.T) {
	stale := Select(Col("id")).From(Table{Name: "sessions"}).Where(Eq(Col("expired"), true)).CTE("stale")
	query := Update(Table{Name: "users"}).
		WithCTE(stale).
		Set(Set("active", false)).
		Where(InQuery(Col("id"), Select(stale.Col("id")).From(stale)))

	assertSQL(t, query,
		`WITH "stale" AS (SELECT "id" FROM "sessions" WHERE ("expired" = $1)) UPDATE "users" SET "active" = $2 WHERE ("id" IN (SELECT "stale"."id" FROM "stale"))`,
		[]any{true, false})
}

func TestDeleteWithCTE(t *testing.T) {
	doomed := Select(Col("id")).From(Table{Name: "orgs"}).Where(Eq(Col("closed"), true)).CTE("doomed")
	query := DeleteFrom(Table{Name: "users"}).
		WithCTE(doomed).
		Where(InQuery(Col("org_id"), Select(doomed.Col("id")).From(doomed)))

	assertSQL(t, query,
		`WITH "doomed" AS (SELECT "id" FROM "orgs" WHERE ("closed" = $1)) DELETE FROM "users" WHERE ("org_id" IN (SELECT "doomed"."id" FROM "doomed"))`,
		[]any{true})
}

// CTEs are emitted in the order given; list a dependency before its dependent.
func TestNestedCTEDependencyOrder(t *testing.T) {
	base := Select(Col("id")).From(Table{Name: "users"}).CTE("base")
	derived := Select(base.Col("id")).From(base).Where(Eq(base.Col("id"), Int(1))).CTE("derived")
	query := Select(derived.Col("id")).From(derived).WithCTE(base, derived)

	assertSQL(t, query,
		`WITH "base" AS (SELECT "id" FROM "users"), "derived" AS (SELECT "base"."id" FROM "base" WHERE ("base"."id" = $1)) SELECT "derived"."id" FROM "derived"`,
		[]any{1})
}

func TestWithEmptyCTENameFails(t *testing.T) {
	cte := Select(Col("id")).From(Table{Name: "u"}).CTE("")
	q := Select(Col("id")).From(cte).WithCTE(cte)
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("empty CTE name compiled, want error")
	}
}
