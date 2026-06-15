package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestInsertOnConflictExpression(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (email, name) VALUES ('a', 'b') "+
			"ON CONFLICT ((lower(email))) DO UPDATE SET name = excluded.name",
		sql.InsertInto(sql.Table{Name: "users"}).Columns("email", "name").Values("a", "b").
			OnConflictExpr(sql.Func("lower", sql.Col("email"))).
			DoUpdate(sql.Set("name", sql.Excluded("name"))),
		`INSERT INTO "users" ("email", "name") VALUES ($1, $2) `+
			`ON CONFLICT ((lower("email"))) DO UPDATE SET "name" = excluded."name"`,
		"a", "b")
}

func TestUpdateSubscriptAssignment(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE t SET tags[1] = 'x' WHERE id = 5",
		sql.Update(sql.Table{Name: "t"}).
			Set(sql.SetAt(sql.Subscript(sql.Col("tags"), 1), "x")).
			Where(sql.Eq(sql.Col("id"), 5)),
		`UPDATE "t" SET "tags"[$1] = $2 WHERE ("id" = $3)`,
		1, "x", 5)
}

// Assignment to a composite field is not modeled, so the statement round-trips
// raw.
func TestUpdateFieldAssignmentFallsBackToRaw(t *testing.T) {
	assertRaw(t, "UPDATE t SET c.f = 1")
}
