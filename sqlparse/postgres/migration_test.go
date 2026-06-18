package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestCreateExtension(t *testing.T) {
	assertRoundTrip(t,
		"CREATE EXTENSION IF NOT EXISTS pgcrypto",
		sql.CreateExtension("pgcrypto").IfNotExists(),
		`CREATE EXTENSION IF NOT EXISTS "pgcrypto";`)
}

func TestCreateExtensionWithSchema(t *testing.T) {
	assertRoundTrip(t,
		`CREATE EXTENSION "uuid-ossp" WITH SCHEMA extensions`,
		sql.CreateExtension("uuid-ossp").Schema("extensions"),
		`CREATE EXTENSION "uuid-ossp" WITH SCHEMA "extensions";`)
}

func TestDropExtensionIfExists(t *testing.T) {
	assertRoundTrip(t,
		"DROP EXTENSION IF EXISTS pgcrypto CASCADE",
		sql.DropExtension("pgcrypto").IfExists().Cascade(),
		`DROP EXTENSION IF EXISTS "pgcrypto" CASCADE;`)
}

func TestCreateEnum(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy')",
		sql.CreateEnum("mood", "sad", "ok", "happy"),
		`CREATE TYPE "mood" AS ENUM ('sad', 'ok', 'happy');`)
}

func TestDropTypeIfExistsCascade(t *testing.T) {
	assertRoundTrip(t,
		"DROP TYPE IF EXISTS app.mood CASCADE",
		sql.DropType("app.mood").IfExists().Cascade(),
		`DROP TYPE IF EXISTS "app"."mood" CASCADE;`)
}

func TestRenameTable(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users RENAME TO people",
		sql.RenameTable(sql.Table{Name: "users"}, "people"),
		`ALTER TABLE "users" RENAME TO "people";`)
}

func TestRenameColumn(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users RENAME COLUMN email TO email_address",
		sql.RenameColumn(sql.Table{Name: "users"}, "email", "email_address"),
		`ALTER TABLE "users" RENAME COLUMN "email" TO "email_address";`)
}
