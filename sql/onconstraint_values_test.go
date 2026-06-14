package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestInsertOnConflictOnConstraint(t *testing.T) {
	doNothing := InsertInto(users).Columns("email").Values("a@x.com").
		OnConflictOnConstraintDoNothing("users_email_key")
	assertSQL(t, doNothing,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ON CONSTRAINT "users_email_key" DO NOTHING`,
		[]any{"a@x.com"})

	doUpdate := InsertInto(users).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflictOnConstraintDoUpdate("users_email_key", Set("name", Excluded("name")))
	assertSQL(t, doUpdate,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) ON CONFLICT ON CONSTRAINT "users_email_key" DO UPDATE SET "name" = excluded."name"`,
		[]any{"a@x.com", "Alice"})
}

func TestInsertOnConflictOnConstraintRejectedOnMySQL(t *testing.T) {
	q := InsertInto(users).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflictOnConstraintDoUpdate("users_email_key", Set("name", Excluded("name")))
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("ToSQL(mysql) error = %v, want constraint rejection", err)
	}
}

func TestValuesQuery(t *testing.T) {
	// Standalone VALUES, built both from rows and via Row.
	assertSQL(t,
		Values([]Expression{Int(1), Val("a")}).Row(2, "b"),
		`VALUES ($1, $2), ($3, $4)`, []any{1, "a", 2, "b"})

	// As a derived-table source feeding a SELECT.
	src := Alias(Values().Row(1, "a").Row(2, "b"), "t")
	assertSQL(t, Select(Col("t.column1")).From(src),
		`SELECT "t"."column1" FROM (VALUES ($1, $2), ($3, $4)) AS "t"`, []any{1, "a", 2, "b"})
}

func TestValuesQueryRejectsRaggedRows(t *testing.T) {
	q := Values().Row(1, "a").Row(2)
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil || !strings.Contains(err.Error(), "want 2") {
		t.Fatalf("ToSQL() error = %v, want ragged-row rejection", err)
	}
}
