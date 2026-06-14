package postgres_test

import "testing"

func TestForUpdate(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE active FOR UPDATE",
		`SELECT "id" FROM "users" WHERE "active" FOR UPDATE`)
}

func TestForShare(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM users FOR SHARE", `SELECT "id" FROM "users" FOR SHARE`)
}

func TestForNoKeyUpdateNoWait(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR NO KEY UPDATE NOWAIT",
		`SELECT "id" FROM "users" FOR NO KEY UPDATE NOWAIT`)
}

func TestForKeyShare(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM users FOR KEY SHARE", `SELECT "id" FROM "users" FOR KEY SHARE`)
}

func TestForUpdateOfSkipLocked(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u JOIN orders o ON o.uid = u.id FOR UPDATE OF u SKIP LOCKED",
		`SELECT "id" FROM "users" AS "u" JOIN "orders" AS "o" ON ("o"."uid" = "u"."id") FOR UPDATE OF "u" SKIP LOCKED`)
}
