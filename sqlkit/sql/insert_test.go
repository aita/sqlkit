package sql_test

import (
	"testing"

	. "github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

func TestInsertFromSelectSQL(t *testing.T) {
	source := Select(Col("email"), Col("name")).
		From(Table{Name: "old_users"}).
		Where(Eq(Col("active"), Val(true)))

	query := InsertInto(users).Columns("email", "name").FromSelect(source)

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") SELECT "email", "name" FROM "old_users" WHERE ("active" = $1)`,
		[]any{true})
}

func TestInsertFromSelectWithoutColumnsSQL(t *testing.T) {
	query := InsertInto(users).FromSelect(Select(Col("id")).From(Table{Name: "old_users"}))

	assertSQL(t, query,
		`INSERT INTO "public"."users" SELECT "id" FROM "old_users"`,
		nil)
}

func TestInsertFromSelectReturning(t *testing.T) {
	query := InsertInto(users).
		Columns("email").
		FromSelect(Select(Col("email")).From(Table{Name: "old_users"})).
		Returning(Col("id"))

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") SELECT "email" FROM "old_users" RETURNING "id"`,
		nil)
}

func TestInsertFromSelectSharesPlaceholderNumbering(t *testing.T) {
	source := Select(Col("email")).
		From(Table{Name: "old_users"}).
		Where(And(Eq(Col("a"), Int(1)), Eq(Col("b"), Int(2))))

	query := InsertInto(users).Columns("email").FromSelect(source)

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") SELECT "email" FROM "old_users" WHERE (("a" = $1) AND ("b" = $2))`,
		[]any{1, 2})
}

func TestInsertFromCompoundQuery(t *testing.T) {
	a := Select(Col("email")).From(Table{Name: "a"})
	b := Select(Col("email")).From(Table{Name: "b"})

	query := InsertInto(users).Columns("email").FromSelect(a.Union(b))

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") (SELECT "email" FROM "a") UNION (SELECT "email" FROM "b")`,
		nil)
}

func TestInsertRejectsValuesAndSelectSource(t *testing.T) {
	query := InsertInto(users).
		Columns("email").
		Values("a@example.com").
		FromSelect(Select(Col("email")).From(Table{Name: "old_users"}))

	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("INSERT with both VALUES and SELECT compiled, want error")
	}
}

func TestInsertRequiresRowsOrSelect(t *testing.T) {
	if _, _, err := InsertInto(users).Columns("email").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("INSERT without VALUES or SELECT compiled, want error")
	}
	if _, _, err := InsertInto(Table{}).Values(1).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("INSERT without table compiled, want error")
	}
}
