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

func TestCreateTableInlineConstraints(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (id int UNIQUE, uid int REFERENCES users(id) ON DELETE CASCADE)",
		`CREATE TABLE "t" (
    "id" integer UNIQUE,
    "uid" integer REFERENCES "users" ("id") ON DELETE CASCADE
);`)
}

func TestCreateTableTableUnique(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a int, b int, CONSTRAINT uq UNIQUE (a, b))",
		`CREATE TABLE "t" (
    "a" integer,
    "b" integer,
    CONSTRAINT "uq" UNIQUE ("a", "b")
);`)
}

func TestAlterTableAddConstraints(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD CONSTRAINT uq UNIQUE (a, b), ADD CONSTRAINT fk FOREIGN KEY (uid) REFERENCES users(id)",
		`ALTER TABLE "t" ADD CONSTRAINT "uq" UNIQUE ("a", "b"), ADD CONSTRAINT "fk" FOREIGN KEY ("uid") REFERENCES "users" ("id");`)
}

func TestDropTable(t *testing.T) {
	assertRoundTrip(t, "DROP TABLE users", `DROP TABLE "users";`)
}

func TestDropMultipleTables(t *testing.T) {
	assertRoundTrip(t, "DROP TABLE a, b, c", `DROP TABLE "a", "b", "c";`)
}

func TestTruncate(t *testing.T) {
	assertRoundTrip(t,
		"TRUNCATE TABLE a, b RESTART IDENTITY CASCADE",
		`TRUNCATE TABLE "a", "b" RESTART IDENTITY CASCADE;`)
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
