package postgres_test

import "testing"

func TestCreateTable(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE s.users (id integer NOT NULL PRIMARY KEY, name varchar(255) NOT NULL DEFAULT 'x', age integer)",
		`CREATE TABLE "s"."users" (
    "id" integer NOT NULL,
    "name" varchar(255) NOT NULL DEFAULT 'x',
    "age" integer,
    PRIMARY KEY ("id")
);`)
}

func TestCreateTableIfNotExists(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE IF NOT EXISTS t (id integer)",
		`CREATE TABLE IF NOT EXISTS "t" (
    "id" integer
);`)
}

func TestCreateTableCheckConstraint(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (age integer, CONSTRAINT age_pos CHECK (age > 0))",
		`CREATE TABLE "t" (
    "age" integer,
    CONSTRAINT "age_pos" CHECK (("age" > 0))
);`)
}

func TestAlterTableAddDropColumn(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users ADD COLUMN email text, DROP COLUMN age",
		`ALTER TABLE "users" ADD COLUMN "email" text, DROP COLUMN "age";`)
}

func TestAlterTableSetNotNull(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users ALTER COLUMN name SET NOT NULL",
		`ALTER TABLE "users" ALTER COLUMN "name" SET NOT NULL;`)
}

func TestAlterTableColumnType(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users ALTER COLUMN age TYPE bigint",
		`ALTER TABLE "users" ALTER COLUMN "age" TYPE bigint;`)
}

func TestDropTable(t *testing.T) {
	assertRoundTrip(t, "DROP TABLE users", `DROP TABLE "users";`)
}

func TestDropTableIfExists(t *testing.T) {
	assertRoundTrip(t, "DROP TABLE IF EXISTS s.users", `DROP TABLE IF EXISTS "s"."users";`)
}

func TestCreateSchema(t *testing.T) {
	assertRoundTrip(t, "CREATE SCHEMA app", `CREATE SCHEMA "app";`)
}

func TestCreateSchemaIfNotExists(t *testing.T) {
	assertRoundTrip(t, "CREATE SCHEMA IF NOT EXISTS app", `CREATE SCHEMA IF NOT EXISTS "app";`)
}

func TestDropSchemaCascade(t *testing.T) {
	assertRoundTrip(t, "DROP SCHEMA IF EXISTS app CASCADE", `DROP SCHEMA IF EXISTS "app" CASCADE;`)
}
