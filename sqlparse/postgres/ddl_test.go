package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestCreateTable(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE s.users (id integer NOT NULL PRIMARY KEY, name varchar(255) NOT NULL DEFAULT 'x', age integer)",
		sql.CreateTable(sql.Table{Schema: sql.NewIdent("s"), Name: sql.NewIdent("users")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("integer"), NotNull: true}).
			Column(sql.ColumnDef{Name: sql.NewIdent("name"), Type: sql.RawType("varchar(255)"), NotNull: true, Default: sql.Raw("'x'")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("age"), Type: sql.RawType("integer")}).
			PrimaryKey("id"),
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
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("integer")}).IfNotExists(),
		`CREATE TABLE IF NOT EXISTS "t" (
    "id" integer
);`)
}

func TestCreateTableCheckConstraint(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (age integer, CONSTRAINT age_pos CHECK (age > 0))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("age"), Type: sql.RawType("integer")}).
			Check("age_pos", sql.BinaryExpression{Left: sql.Col("age"), Operator: ">", Right: sql.Raw("0")}),
		`CREATE TABLE "t" (
    "age" integer,
    CONSTRAINT "age_pos" CHECK (("age" > 0))
);`)
}

func TestCreateTableInlineConstraints(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (id int UNIQUE, uid int REFERENCES users(id) ON DELETE CASCADE)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("integer"), Unique: true}).
			Column(sql.ColumnDef{Name: sql.NewIdent("uid"), Type: sql.RawType("integer"),
				References: &sql.ColumnReference{Table: sql.NewIdent("users"), Column: sql.NewIdent("id"), OnDelete: "CASCADE"}}),
		`CREATE TABLE "t" (
    "id" integer UNIQUE,
    "uid" integer REFERENCES "users" ("id") ON DELETE CASCADE
);`)
}

func TestCreateTableTableUnique(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a int, b int, CONSTRAINT uq UNIQUE (a, b))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("integer")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("integer")}).
			Unique("uq", "a", "b"),
		`CREATE TABLE "t" (
    "a" integer,
    "b" integer,
    CONSTRAINT "uq" UNIQUE ("a", "b")
);`)
}

func TestAlterTableAddDropColumn(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users ADD COLUMN email text, DROP COLUMN age",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).
			AddColumn(sql.ColumnDef{Name: sql.NewIdent("email"), Type: sql.RawType("text")}).DropColumn("age"),
		`ALTER TABLE "users" ADD COLUMN "email" text, DROP COLUMN "age";`)
}

func TestAlterTableSetNotNull(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users ALTER COLUMN name SET NOT NULL",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).SetNotNull("name"),
		`ALTER TABLE "users" ALTER COLUMN "name" SET NOT NULL;`)
}

func TestAlterTableColumnType(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users ALTER COLUMN age TYPE bigint",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).AlterColumnType("age", sql.RawType("bigint")),
		`ALTER TABLE "users" ALTER COLUMN "age" TYPE bigint;`)
}

func TestAlterTableAddConstraints(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD CONSTRAINT uq UNIQUE (a, b), ADD CONSTRAINT fk FOREIGN KEY (uid) REFERENCES users(id)",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).AddUnique("uq", "a", "b").
			AddForeignKey(sql.ForeignKeyConstraint{Name: sql.NewIdent("fk"), Columns: sql.Idents("uid"), RefTable: sql.NewIdent("users"), RefColumns: sql.Idents("id")}),
		`ALTER TABLE "t" ADD CONSTRAINT "uq" UNIQUE ("a", "b"), ADD CONSTRAINT "fk" FOREIGN KEY ("uid") REFERENCES "users" ("id");`)
}

func TestDropTable(t *testing.T) {
	assertRoundTrip(t, "DROP TABLE users",
		sql.DropTable(sql.Table{Name: sql.NewIdent("users")}),
		`DROP TABLE "users";`)
}

func TestDropMultipleTables(t *testing.T) {
	assertRoundTrip(t, "DROP TABLE a, b, c",
		sql.DropTable(sql.Table{Name: sql.NewIdent("a")}, sql.Table{Name: sql.NewIdent("b")}, sql.Table{Name: sql.NewIdent("c")}),
		`DROP TABLE "a", "b", "c";`)
}

func TestDropTableIfExists(t *testing.T) {
	assertRoundTrip(t, "DROP TABLE IF EXISTS s.users",
		sql.DropTable(sql.Table{Schema: sql.NewIdent("s"), Name: sql.NewIdent("users")}).IfExists(),
		`DROP TABLE IF EXISTS "s"."users";`)
}

func TestTruncate(t *testing.T) {
	assertRoundTrip(t,
		"TRUNCATE TABLE a, b RESTART IDENTITY CASCADE",
		sql.Truncate(sql.Table{Name: sql.NewIdent("a")}, sql.Table{Name: sql.NewIdent("b")}).RestartIdentity().Cascade(),
		`TRUNCATE TABLE "a", "b" RESTART IDENTITY CASCADE;`)
}

func TestCreateSchema(t *testing.T) {
	assertRoundTrip(t, "CREATE SCHEMA app",
		sql.CreateSchema("app"),
		`CREATE SCHEMA "app";`)
}

func TestCreateSchemaIfNotExists(t *testing.T) {
	assertRoundTrip(t, "CREATE SCHEMA IF NOT EXISTS app",
		sql.CreateSchema("app").IfNotExists(),
		`CREATE SCHEMA IF NOT EXISTS "app";`)
}

func TestDropSchemaCascade(t *testing.T) {
	assertRoundTrip(t, "DROP SCHEMA IF EXISTS app CASCADE",
		sql.DropSchema("app").IfExists().Cascade(),
		`DROP SCHEMA IF EXISTS "app" CASCADE;`)
}
