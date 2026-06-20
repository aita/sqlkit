package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestForUpdate(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE active FOR UPDATE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Col("active")).ForUpdate(),
		`SELECT "id" FROM "users" WHERE "active" FOR UPDATE`)
}

func TestForShare(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM users FOR SHARE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForShare(),
		`SELECT "id" FROM "users" FOR SHARE`)
}

func TestForNoKeyUpdateNoWait(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR NO KEY UPDATE NOWAIT",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForNoKeyUpdate(sql.NoWait()),
		`SELECT "id" FROM "users" FOR NO KEY UPDATE NOWAIT`)
}

func TestForKeyShare(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM users FOR KEY SHARE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForKeyShare(),
		`SELECT "id" FROM "users" FOR KEY SHARE`)
}

func TestForUpdateOfSkipLocked(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u JOIN orders o ON o.uid = u.id FOR UPDATE OF u SKIP LOCKED",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u")).
			Join(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o"), sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))).
			ForUpdate(sql.LockOf("u"), sql.SkipLocked()),
		`SELECT "id" FROM "users" AS "u" JOIN "orders" AS "o" ON ("o"."uid" = "u"."id") FOR UPDATE OF "u" SKIP LOCKED`)
}
