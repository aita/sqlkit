package sql_test

import (
	"database/sql"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

// bulkUpdate builds UPDATE users SET name = v.c0 FROM a two-row VALUES source.
func bulkUpdate() UpdateBuilder {
	rows := [][]Expression{
		{Val(int64(1)), Val("a")},
		{Val(int64(2)), Val("b")},
	}
	return Update(users).
		Set(Set("name", Col("v.c0"))).
		From(ValuesSource(rows, "v", "k", "c0")).
		Where(Eq(Col("public.users.id"), Col("v.k")))
}

func TestValuesSourcePostgres(t *testing.T) {
	// The first row's values are cast so PostgreSQL types the VALUES columns
	// (a bare parameter would default to text and break "id" = "v"."k").
	assertSQL(t, bulkUpdate(),
		`UPDATE "public"."users" SET "name" = "v"."c0" FROM (VALUES (CAST($1 AS bigint), CAST($2 AS text)), ($3, $4)) AS "v"("k", "c0") WHERE ("public"."users"."id" = "v"."k")`,
		[]any{int64(1), "a", int64(2), "b"})
}

func TestValuesSourceMySQL(t *testing.T) {
	// MySQL cannot alias VALUES columns, so the source renders as a UNION ALL of
	// SELECTs whose first arm names them, joined inline into the multi-table UPDATE.
	assertMySQL(t, bulkUpdate(),
		"UPDATE `public`.`users`, (SELECT ? AS `k`, ? AS `c0` UNION ALL SELECT ?, ?) AS `v` SET `name` = `v`.`c0` WHERE (`public`.`users`.`id` = `v`.`k`)",
		[]any{int64(1), "a", int64(2), "b"})
}

func TestValuesSourceRejectsMismatchedColumns(t *testing.T) {
	rows := [][]Expression{{Val(1), Val(2)}}
	q := Update(users).Set(Set("name", Col("v.c0"))).From(ValuesSource(rows, "v", "k"))
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("expected an error for a column-count mismatch")
	}
}

// TestValuesSourceCastsDriverValuer checks the first VALUES row is typed even when
// values arrive as database/sql Null* wrappers (driver.Valuer), so a bulk UPDATE's
// bigint key column is not inferred as text on PostgreSQL.
func TestValuesSourceCastsDriverValuer(t *testing.T) {
	rows := [][]Expression{
		{Val(sql.NullInt64{Int64: 1, Valid: true}), Val(sql.NullString{String: "a", Valid: true})},
		{Val(int64(2)), Val("b")},
	}
	q := Update(users).
		Set(Set("name", Col("v.c0"))).
		From(ValuesSource(rows, "v", "k", "c0")).
		Where(Eq(Col("public.users.id"), Col("v.k")))
	got, _, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL error = %v", err)
	}
	want := `UPDATE "public"."users" SET "name" = "v"."c0" FROM (VALUES (CAST($1 AS bigint), CAST($2 AS text)), ($3, $4)) AS "v"("k", "c0") WHERE ("public"."users"."id" = "v"."k")`
	if got != want {
		t.Fatalf("ToSQL =\n  %s\nwant\n  %s", got, want)
	}
}
