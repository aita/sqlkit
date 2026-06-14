package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestInsertValues(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (id, name) VALUES (1, 'a'), (2, 'b')",
		sql.InsertInto(sql.Table{Name: "users"}).Columns("id", "name").Values(1, "a").Values(2, "b"),
		`INSERT INTO "users" ("id", "name") VALUES ($1, $2), ($3, $4)`,
		1, "a", 2, "b")
}

func TestInsertFromSelect(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO archive (id) SELECT id FROM users WHERE active",
		sql.InsertInto(sql.Table{Name: "archive"}).Columns("id").
			FromSelect(sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).Where(sql.Col("active"))),
		`INSERT INTO "archive" ("id") SELECT "id" FROM "users" WHERE "active"`)
}

func TestInsertOnConflictDoNothing(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (id) VALUES (1) ON CONFLICT (id) DO NOTHING",
		sql.InsertInto(sql.Table{Name: "users"}).Columns("id").Values(1).
			OnConflict(sql.OnColumns("id"), sql.DoNothing()),
		`INSERT INTO "users" ("id") VALUES ($1) ON CONFLICT ("id") DO NOTHING`,
		1)
}

func TestInsertOnConflictDoUpdate(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (id, name) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = excluded.name RETURNING id",
		sql.InsertInto(sql.Table{Name: "users"}).Columns("id", "name").Values(1, "a").
			OnConflict(sql.OnColumns("id"), sql.DoUpdate(sql.Set("name", sql.Excluded("name")))).
			Returning(sql.Col("id")),
		`INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name" RETURNING "id"`,
		1, "a")
}

func TestInsertOnConflictOnConstraint(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (id) VALUES (1) ON CONFLICT ON CONSTRAINT t_pkey DO NOTHING",
		sql.InsertInto(sql.Table{Name: "t"}).Columns("id").Values(1).
			OnConflict(sql.OnConstraint("t_pkey"), sql.DoNothing()),
		`INSERT INTO "t" ("id") VALUES ($1) ON CONFLICT ON CONSTRAINT "t_pkey" DO NOTHING`,
		1)
}

func TestUpdateSetWhere(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET name = 'x', n = n + 1 WHERE id = 5",
		sql.Update(sql.Table{Name: "users"}).
			Set(sql.Set("name", "x"), sql.Set("n", sql.Add(sql.Col("n"), 1))).
			Where(sql.Eq(sql.Col("id"), 5)),
		`UPDATE "users" SET "name" = $1, "n" = ("n" + $2) WHERE ("id" = $3)`,
		"x", 1, 5)
}

func TestUpdateFrom(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET name = o.name FROM orders o WHERE o.uid = users.id",
		sql.Update(sql.Table{Name: "users"}).Set(sql.Set("name", sql.Col("o.name"))).
			From(sql.SourceAlias(sql.Table{Name: "orders"}, "o")).
			Where(sql.Eq(sql.Col("o.uid"), sql.Col("users.id"))),
		`UPDATE "users" SET "name" = "o"."name" FROM "orders" AS "o" WHERE ("o"."uid" = "users"."id")`)
}

func TestUpdateReturning(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET active = false WHERE id = 1 RETURNING id, active",
		sql.Update(sql.Table{Name: "users"}).Set(sql.Set("active", false)).
			Where(sql.Eq(sql.Col("id"), 1)).Returning(sql.Col("id"), sql.Col("active")),
		`UPDATE "users" SET "active" = $1 WHERE ("id" = $2) RETURNING "id", "active"`,
		false, 1)
}

func TestDeleteWhere(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM users WHERE id = 5",
		sql.DeleteFrom(sql.Table{Name: "users"}).Where(sql.Eq(sql.Col("id"), 5)),
		`DELETE FROM "users" WHERE ("id" = $1)`,
		5)
}

func TestDeleteUsing(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM users USING orders o WHERE o.uid = users.id",
		sql.DeleteFrom(sql.Table{Name: "users"}).Using(sql.SourceAlias(sql.Table{Name: "orders"}, "o")).
			Where(sql.Eq(sql.Col("o.uid"), sql.Col("users.id"))),
		`DELETE FROM "users" USING "orders" AS "o" WHERE ("o"."uid" = "users"."id")`)
}

func TestDeleteReturning(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM users WHERE id = 1 RETURNING id",
		sql.DeleteFrom(sql.Table{Name: "users"}).Where(sql.Eq(sql.Col("id"), 1)).Returning(sql.Col("id")),
		`DELETE FROM "users" WHERE ("id" = $1) RETURNING "id"`,
		1)
}
