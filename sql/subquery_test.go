package sql

import "testing"

func TestSubqueryAsFromSource(t *testing.T) {
	sub := Select(Col("users.id")).
		From(Table{Name: NewIdent("users")}).
		Where(Gt(Col("users.id"), Int(10))).
		As("sub")
	query := Select(sub.Col("id")).From(sub)

	assertSQL(t, query,
		`SELECT "sub"."id" FROM (SELECT "users"."id" FROM "users" WHERE ("users"."id" > $1)) AS "sub"`,
		[]any{10})
}

func TestSubqueryAsJoinSource(t *testing.T) {
	sub := Select(Col("posts.user_id")).From(Table{Name: NewIdent("posts")}).As("p")
	query := Select(Col("users.id"), sub.Col("user_id")).
		From(Table{Name: NewIdent("users")}).
		Join(sub, Eq(Col("users.id"), sub.Col("user_id")))

	assertSQL(t, query,
		`SELECT "users"."id", "p"."user_id" FROM "users" JOIN (SELECT "posts"."user_id" FROM "posts") AS "p" ON ("users"."id" = "p"."user_id")`,
		nil)
}

func TestCompoundQueryAs(t *testing.T) {
	sub := Select(Col("id")).From(Table{Name: NewIdent("users")}).
		UnionAll(Select(Col("id")).From(Table{Name: NewIdent("admins")})).
		As("ids")
	query := Select(sub.Col("id")).From(sub)

	assertSQL(t, query,
		`SELECT "ids"."id" FROM ((SELECT "id" FROM "users") UNION ALL (SELECT "id" FROM "admins")) AS "ids"`,
		nil)
}

// TestDerivedTableAlias covers DerivedTable.Alias.
func TestDerivedTableAlias(t *testing.T) {
	d := Select(Col("id")).From(orgs).As("d")
	if d.Alias() != "d" {
		t.Fatalf("Alias() = %q, want d", d.Alias())
	}
}
