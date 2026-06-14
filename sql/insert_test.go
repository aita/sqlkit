package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
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

func TestInsertOnConflictDoNothing(t *testing.T) {
	query := InsertInto(users).Columns("email").Values("a@example.com").
		OnConflict(OnColumns("email"), DoNothing())
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ("email") DO NOTHING`,
		[]any{"a@example.com"})
}

func TestInsertOnConflictDoUpdate(t *testing.T) {
	query := InsertInto(users).Columns("email", "name").Values("a@example.com", "Alice").
		OnConflict(OnColumns("email"), DoUpdate(Set("name", Excluded("name")))).
		Returning(Col("id"))
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) ON CONFLICT ("email") DO UPDATE SET "name" = excluded."name" RETURNING "id"`,
		[]any{"a@example.com", "Alice"})
}

func TestInsertOnConflictOnConstraint(t *testing.T) {
	doNothing := InsertInto(users).Columns("email").Values("a@x.com").
		OnConflict(OnConstraint("users_email_key"), DoNothing())
	assertSQL(t, doNothing,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ON CONSTRAINT "users_email_key" DO NOTHING`,
		[]any{"a@x.com"})

	doUpdate := InsertInto(users).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict(OnConstraint("users_email_key"), DoUpdate(Set("name", Excluded("name"))))
	assertSQL(t, doUpdate,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) ON CONFLICT ON CONSTRAINT "users_email_key" DO UPDATE SET "name" = excluded."name"`,
		[]any{"a@x.com", "Alice"})
}

func TestInsertOnConflictOnConstraintRejectedOnMySQL(t *testing.T) {
	q := InsertInto(users).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflict(OnConstraint("users_email_key"), DoUpdate(Set("name", Excluded("name"))))
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("ToSQL(mysql) error = %v, want constraint rejection", err)
	}
}

func TestInsertOnConflictDoUpdateWherePlaceholderOrder(t *testing.T) {
	query := InsertInto(users).Columns("email", "name").Values("a@example.com", "Alice").
		OnConflict(
			OnColumns("email"),
			DoUpdate(Set("name", Excluded("name"))),
			ConflictWhere(Ne(Col("users.name"), Val("locked"))),
		)
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) ON CONFLICT ("email") DO UPDATE SET "name" = excluded."name" WHERE ("users"."name" <> $3)`,
		[]any{"a@example.com", "Alice", "locked"})
}

func TestInsertOnConflictDoUpdateRequiresAssignment(t *testing.T) {
	query := InsertInto(users).Columns("email").Values("a@example.com").
		OnConflict(OnColumns("email"))
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("DO UPDATE without assignments compiled, want error")
	}
}

func TestInsertOnConflictDoUpdateRequiresTargetOnPostgres(t *testing.T) {
	query := InsertInto(users).Columns("email", "name").Values("a@example.com", "Alice").
		OnConflict(DoUpdate(Set("name", Excluded("name"))))
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("DO UPDATE without a conflict target compiled on PostgreSQL, want error")
	}
}

func TestInsertOnConflictWhereIgnoredOnDoNothing(t *testing.T) {
	// A WHERE alongside DO NOTHING is dropped, and must not leave a dangling
	// bind value behind (which would desync the placeholder count).
	query := InsertInto(users).Columns("email").Values("a@example.com").
		OnConflict(OnColumns("email"), DoNothing(), ConflictWhere(Ne(Col("users.name"), Val("locked"))))
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ("email") DO NOTHING`,
		[]any{"a@example.com"})
}
