package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestInsertValues(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (id, name) VALUES (1, 'a'), (2, 'b')",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("users")}).Columns("id", "name").Values(1, "a").Values(2, "b"),
		`INSERT INTO "users" ("id", "name") VALUES ($1, $2), ($3, $4)`,
		1, "a", 2, "b")
}

func TestInsertFromSelect(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO archive (id) SELECT id FROM users WHERE active",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("archive")}).Columns("id").
			FromSelect(sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Col("active"))),
		`INSERT INTO "archive" ("id") SELECT "id" FROM "users" WHERE "active"`)
}

func TestInsertOnConflictDoNothing(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (id) VALUES (1) ON CONFLICT (id) DO NOTHING",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("users")}).Columns("id").Values(1).
			OnConflict("id").DoNothing(),
		`INSERT INTO "users" ("id") VALUES ($1) ON CONFLICT ("id") DO NOTHING`,
		1)
}

func TestInsertOnConflictDoUpdate(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = excluded.name RETURNING id",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("users")}).Columns("id", "name").Values(1, "a").
			OnConflict("id").DoUpdate(sql.Set("name", sql.Excluded("name"))).
			Returning(sql.Col("id")),
		`INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name" RETURNING "id"`,
		1, "a")
}

func TestInsertOnConflictOnConstraint(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (id) VALUES (1) ON CONFLICT ON CONSTRAINT t_pkey DO NOTHING",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("t")}).Columns("id").Values(1).
			OnConflictConstraint("t_pkey").DoNothing(),
		`INSERT INTO "t" ("id") VALUES ($1) ON CONFLICT ON CONSTRAINT "t_pkey" DO NOTHING`,
		1)
}

func TestUpdateSetWhere(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET name = 'x', n = n + 1 WHERE id = 5",
		sql.Update(sql.Table{Name: sql.NewIdent("users")}).
			Set(sql.Set("name", "x"), sql.Set("n", sql.Add(sql.Col("n"), 1))).
			Where(sql.Eq(sql.Col("id"), 5)),
		`UPDATE "users" SET "name" = $1, "n" = ("n" + $2) WHERE ("id" = $3)`,
		"x", 1, 5)
}

func TestUpdateFrom(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET name = o.name FROM orders o WHERE o.uid = users.id",
		sql.Update(sql.Table{Name: sql.NewIdent("users")}).Set(sql.Set("name", sql.Col("o.name"))).
			From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o")).
			Where(sql.Eq(sql.Col("o.uid"), sql.Col("users.id"))),
		`UPDATE "users" SET "name" = "o"."name" FROM "orders" AS "o" WHERE ("o"."uid" = "users"."id")`)
}

func TestUpdateReturning(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET active = false WHERE id = 1 RETURNING id, active",
		sql.Update(sql.Table{Name: sql.NewIdent("users")}).Set(sql.Set("active", false)).
			Where(sql.Eq(sql.Col("id"), 1)).Returning(sql.Col("id"), sql.Col("active")),
		`UPDATE "users" SET "active" = $1 WHERE ("id" = $2) RETURNING "id", "active"`,
		false, 1)
}

func TestDeleteWhere(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM users WHERE id = 5",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Eq(sql.Col("id"), 5)),
		`DELETE FROM "users" WHERE ("id" = $1)`,
		5)
}

func TestDeleteUsing(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM users USING orders o WHERE o.uid = users.id",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("users")}).Using(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o")).
			Where(sql.Eq(sql.Col("o.uid"), sql.Col("users.id"))),
		`DELETE FROM "users" USING "orders" AS "o" WHERE ("o"."uid" = "users"."id")`)
}

func TestDeleteReturning(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM users WHERE id = 1 RETURNING id",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Eq(sql.Col("id"), 1)).Returning(sql.Col("id")),
		`DELETE FROM "users" WHERE ("id" = $1) RETURNING "id"`,
		1)
}

// A WITH clause attached to an UPDATE exercises the CTE branch of convertUpdate.
func TestUpdateWithCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH c AS (SELECT id FROM s) UPDATE t SET x = 1 WHERE id IN (SELECT id FROM c)",
		sql.Update(sql.Table{Name: sql.NewIdent("t")}).Set(sql.Set("x", 1)).
			Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("c")}))).
			WithCTE(sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("s")}).CTE("c")),
		`WITH "c" AS (SELECT "id" FROM "s") UPDATE "t" SET "x" = $1 WHERE ("id" IN (SELECT "id" FROM "c"))`,
		1)
}

// A WITH clause attached to a DELETE exercises the CTE branch of convertDelete.
func TestDeleteWithCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH c AS (SELECT id FROM s) DELETE FROM t WHERE id IN (SELECT id FROM c)",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("c")}))).
			WithCTE(sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("s")}).CTE("c")),
		`WITH "c" AS (SELECT "id" FROM "s") DELETE FROM "t" WHERE ("id" IN (SELECT "id" FROM "c"))`)
}

// UPDATE ... FROM with multiple comma-separated sources exercises the From list
// of convertUpdate.
func TestUpdateFromMultipleSources(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE t SET x = 1 FROM a, b WHERE t.id = a.id AND a.id = b.id",
		sql.Update(sql.Table{Name: sql.NewIdent("t")}).Set(sql.Set("x", 1)).
			From(sql.Table{Name: sql.NewIdent("a")}, sql.Table{Name: sql.NewIdent("b")}).
			Where(sql.And(sql.Eq(sql.Col("t.id"), sql.Col("a.id")), sql.Eq(sql.Col("a.id"), sql.Col("b.id")))),
		`UPDATE "t" SET "x" = $1 FROM "a", "b" WHERE (("t"."id" = "a"."id") AND ("a"."id" = "b"."id"))`,
		1)
}

// DELETE ... USING with multiple comma-separated sources exercises the Using
// list of convertDelete.
func TestDeleteUsingMultipleSources(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM t USING a, b WHERE t.id = a.id AND a.id = b.id",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("t")}).
			Using(sql.Table{Name: sql.NewIdent("a")}, sql.Table{Name: sql.NewIdent("b")}).
			Where(sql.And(sql.Eq(sql.Col("t.id"), sql.Col("a.id")), sql.Eq(sql.Col("a.id"), sql.Col("b.id")))),
		`DELETE FROM "t" USING "a", "b" WHERE (("t"."id" = "a"."id") AND ("a"."id" = "b"."id"))`)
}

// INSERT ... SELECT with RETURNING exercises the select-source and returning
// branches together.
func TestInsertFromSelectReturning(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (a) SELECT x FROM u RETURNING a",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("t")}).Columns("a").
			FromSelect(sql.Select(sql.Col("x")).From(sql.Table{Name: sql.NewIdent("u")})).Returning(sql.Col("a")),
		`INSERT INTO "t" ("a") SELECT "x" FROM "u" RETURNING "a"`)
}

// A MERGE with a RETURNING clause exercises the returning branch of convertMerge.
func TestMergeReturning(t *testing.T) {
	assertRoundTrip(t,
		"MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN DELETE RETURNING t.id",
		sql.MergeInto(sql.Table{Name: sql.NewIdent("t")}).
			Using(sql.Table{Name: sql.NewIdent("s")}).
			On(sql.Eq(sql.Col("t.id"), sql.Col("s.id"))).
			When(sql.WhenMatched().ThenDelete()).
			Returning(sql.Col("t.id")),
		`MERGE INTO "t" USING "s" ON ("t"."id" = "s"."id") WHEN MATCHED THEN DELETE RETURNING "t"."id"`)
}

// UPDATE ... SET col = DEFAULT is not modeled as a typed assignment and the
// statement round-trips raw.
func TestUpdateSetDefaultFallsBackToRaw(t *testing.T) {
	assertRaw(t, "UPDATE t SET a = DEFAULT")
}

// The array containment operators parse to binary expressions with the value
// bound as a parameter.
func TestExprArrayContainmentOperators(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t WHERE b @> '{1}' AND c <@ '{2}'",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.And(
				sql.BinaryExpression{Left: sql.Col("b"), Operator: "@>", Right: sql.Val("{1}")},
				sql.BinaryExpression{Left: sql.Col("c"), Operator: "<@", Right: sql.Val("{2}")},
			)),
		`SELECT "a" FROM "t" WHERE (("b" @> $1) AND ("c" <@ $2))`,
		"{1}", "{2}")
}

// libpg_query accepts an INSERT whose row arity differs from its column list, or
// one with no column list; sqlkit's compiler requires a column list and a
// matching arity, so both fall back to raw rather than fail to compile.
func TestInsertColumnValueMismatchFallsBackToRaw(t *testing.T) {
	assertRaw(t, "INSERT INTO t (a) VALUES (1, 2)")
}

func TestInsertWithoutColumnsFallsBackToRaw(t *testing.T) {
	assertRaw(t, "INSERT INTO t VALUES (1, 2, 3)")
}
