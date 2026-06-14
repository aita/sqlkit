package sql_test

import (
	"reflect"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

var (
	users = Table{Schema: "public", Name: "users"}
	orgs  = Table{Name: "orgs"}
)

func assertSQL(t *testing.T, query Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q, want %q", sqlText, wantSQL)
	}
	if len(wantArgs) == 0 {
		if len(args) != 0 {
			t.Fatalf("ToSQL() args = %#v, want none", args)
		}
		return
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}

func TestSelectQueryToSQL(t *testing.T) {
	query := Select(Col("public.users.id"), Col("public.users.email")).
		From(users).
		Where(Eq(Col("id"), 42))

	assertSQL(t, query,
		`SELECT "public"."users"."id", "public"."users"."email" FROM "public"."users" WHERE ("id" = $1)`,
		[]any{42})
}

func TestSelectQueryAcceptsAggregateSelection(t *testing.T) {
	query := Select(Count(Col("users.id"))).From(orgs)
	assertSQL(t, query, `SELECT COUNT("users"."id") FROM "orgs"`, nil)
}

func TestSelectQueryAcceptsJoin(t *testing.T) {
	query := Select(Col("users.email"), Col("orgs.name")).
		From(Table{Name: "users"}).
		Join(orgs, Eq(Col("users.org_id"), Col("orgs.id")))

	assertSQL(t, query,
		`SELECT "users"."email", "orgs"."name" FROM "users" JOIN "orgs" ON ("users"."org_id" = "orgs"."id")`,
		nil)
}

func TestNameSourceHelper(t *testing.T) {
	query := Select(Col("t.id")).From(Tbl("t")).Join(Tbl("u"), Eq(Col("t.id"), Col("u.id")))
	assertSQL(t, query,
		`SELECT "t"."id" FROM "t" JOIN "u" ON ("t"."id" = "u"."id")`,
		nil)
}

func TestSelectQueryAcceptsSubqueryFrom(t *testing.T) {
	subquery := Select(Col("users.id")).From(Table{Name: "users"}).Where(Eq(Col("users.id"), Int(7)))
	query := Select(Col("active_users.id")).From(Alias(subquery, "active_users"))

	assertSQL(t, query,
		`SELECT "active_users"."id" FROM (SELECT "users"."id" FROM "users" WHERE ("users"."id" = $1)) AS "active_users"`,
		[]any{7})
}

func TestSelectQueryAcceptsSubqueryDirectly(t *testing.T) {
	subquery := Select(Col("users.id")).From(Table{Name: "users"})
	query := Select(CountAll()).From(subquery)

	assertSQL(t, query, `SELECT COUNT(*) FROM (SELECT "users"."id" FROM "users")`, nil)
}

func TestSubqueryPlaceholdersShareNumbering(t *testing.T) {
	subquery := Select(Col("users.id")).From(Table{Name: "users"}).Where(Eq(Col("users.org_id"), Int(7)))
	query := Select(CountAll()).
		From(Alias(subquery, "u")).
		Where(Eq(Col("u.id"), Int(42)))

	assertSQL(t, query,
		`SELECT COUNT(*) FROM (SELECT "users"."id" FROM "users" WHERE ("users"."org_id" = $1)) AS "u" WHERE ("u"."id" = $2)`,
		[]any{7, 42})
}

func TestSelectQueryClauses(t *testing.T) {
	query := Select(Col("users.org_id")).
		From(Table{Name: "users"}).
		Where(IsNotNull(Col("users.email"))).
		GroupBy(Col("users.org_id")).
		Having(Gt(Raw("COUNT(*)"), Int(10))).
		OrderBy(Desc(Col("users.org_id"))).
		Limit(5).
		Offset(20)

	assertSQL(t, query,
		`SELECT "users"."org_id" FROM "users" WHERE ("users"."email" IS NOT NULL) GROUP BY "users"."org_id" HAVING (COUNT(*) > $1) ORDER BY "users"."org_id" DESC LIMIT 5 OFFSET 20`,
		[]any{10})
}

func TestLogicalOperators(t *testing.T) {
	query := Select(Col("id")).
		From(Table{Name: "users"}).
		Where(And(
			Or(Eq(Col("a"), Int(1)), Eq(Col("b"), Int(2))),
			Not(In(Col("c"), Int(3), Int(4))),
			IsNull(Col("d")),
		))

	assertSQL(t, query,
		`SELECT "id" FROM "users" WHERE ((("a" = $1) OR ("b" = $2)) AND (NOT ("c" IN ($3, $4))) AND ("d" IS NULL))`,
		[]any{1, 2, 3, 4})
}

func TestInsertQueryToSQL(t *testing.T) {
	query := InsertInto(users).
		Columns("email", "name").
		Values("a@example.com", "Alice").
		Values("b@example.com", "Bob").
		Returning(Col("id"))

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4) RETURNING "id"`,
		[]any{"a@example.com", "Alice", "b@example.com", "Bob"})
}

func TestUpdateQueryToSQL(t *testing.T) {
	query := Update(users).
		Set(Set("email", "new@example.com"), Set("name", "New")).
		Where(Eq(Col("id"), Int(7)))

	assertSQL(t, query,
		`UPDATE "public"."users" SET "email" = $1, "name" = $2 WHERE ("id" = $3)`,
		[]any{"new@example.com", "New", 7})
}

func TestDeleteQueryToSQL(t *testing.T) {
	query := DeleteFrom(users).Where(Eq(Col("id"), Int(7)))

	assertSQL(t, query, `DELETE FROM "public"."users" WHERE ("id" = $1)`, []any{7})
}

func TestRepeatedWhereCombinesWithAnd(t *testing.T) {
	query := Select(Col("id")).
		From(Table{Name: "users"}).
		Where(Eq(Col("a"), Int(1))).
		Where(Eq(Col("b"), Int(2)))

	assertSQL(t, query,
		`SELECT "id" FROM "users" WHERE (("a" = $1) AND ("b" = $2))`,
		[]any{1, 2})
}

func TestSourcelessSelect(t *testing.T) {
	assertSQL(t, Select(Raw("now()")), `SELECT now()`, nil)
	assertSQL(t, Select(Raw("1"), Raw("2")), `SELECT 1, 2`, nil)
}

func TestSelectQualifiedStar(t *testing.T) {
	query := Select(StarOf("users"), Col("orgs.name")).
		From(users).
		Join(orgs, Eq(Col("users.org_id"), Col("orgs.id")))

	assertSQL(t, query,
		`SELECT "users".*, "orgs"."name" FROM "public"."users" JOIN "orgs" ON ("users"."org_id" = "orgs"."id")`,
		nil)
}

func TestSelectUnqualifiedStar(t *testing.T) {
	query := Select(Star()).From(users)

	assertSQL(t, query, `SELECT * FROM "public"."users"`, nil)
}

func TestSelectStarWithoutSourceFails(t *testing.T) {
	if _, _, err := Select().ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("SELECT * without FROM compiled, want error")
	}
}

func TestJoinWithoutSourceFails(t *testing.T) {
	query := Select(Col("id")).Join(Table{Name: "users"}, Eq(Col("a"), Col("b")))
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("JOIN without FROM compiled, want error")
	}
}
