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

func TestAlterTableSetDefault(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ALTER COLUMN x SET DEFAULT 0",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).SetDefault("x", sql.Raw("0")),
		`ALTER TABLE "t" ALTER COLUMN "x" SET DEFAULT 0;`)
}

func TestAlterTableDropDefault(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ALTER COLUMN x DROP DEFAULT",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).DropDefault("x"),
		`ALTER TABLE "t" ALTER COLUMN "x" DROP DEFAULT;`)
}

func TestAlterTableDropNotNull(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ALTER COLUMN x DROP NOT NULL",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).DropNotNull("x"),
		`ALTER TABLE "t" ALTER COLUMN "x" DROP NOT NULL;`)
}

func TestAlterTableAddCheck(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD CONSTRAINT c CHECK (x > 0)",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).
			AddCheck("c", sql.BinaryExpression{Left: sql.Col("x"), Operator: ">", Right: sql.Raw("0")}),
		`ALTER TABLE "t" ADD CONSTRAINT "c" CHECK (("x" > 0));`)
}

func TestAlterTableDropConstraint(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t DROP CONSTRAINT c",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).DropConstraint("c"),
		`ALTER TABLE "t" DROP CONSTRAINT "c";`)
}

// A table-level foreign key with both referential actions exercises the SET
// NULL and RESTRICT cases of fkAction.
func TestAlterTableForeignKeyActions(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY (a) REFERENCES u(id) ON DELETE SET NULL ON UPDATE RESTRICT",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).AddForeignKey(sql.ForeignKeyConstraint{
			Name: sql.NewIdent("fk"), Columns: sql.Idents("a"),
			RefTable: sql.NewIdent("u"), RefColumns: sql.Idents("id"),
			OnDelete: "SET NULL", OnUpdate: "RESTRICT",
		}),
		`ALTER TABLE "t" ADD CONSTRAINT "fk" FOREIGN KEY ("a") REFERENCES "u" ("id") ON DELETE SET NULL ON UPDATE RESTRICT;`)
}

// An inline column reference with SET DEFAULT exercises the remaining fkAction
// branch.
func TestCreateTableInlineReferenceSetDefault(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (uid int REFERENCES users(id) ON DELETE SET DEFAULT)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("uid"), Type: sql.RawType("integer"),
				References: &sql.ColumnReference{Table: sql.NewIdent("users"), Column: sql.NewIdent("id"), OnDelete: "SET DEFAULT"}}),
		`CREATE TABLE "t" (
    "uid" integer REFERENCES "users" ("id") ON DELETE SET DEFAULT
);`)
}

// Column DEFAULTs cover the constant kinds staticConst renders verbatim:
// boolean true/false, an escaped string literal, a wide decimal, and NULL.
func TestCreateTableStaticDefaults(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a boolean DEFAULT true, b boolean DEFAULT false, c text DEFAULT 'a''b', d numeric DEFAULT 1.5, e int DEFAULT NULL)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("boolean"), Default: sql.Raw("true")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("boolean"), Default: sql.Raw("false")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("c"), Type: sql.RawType("text"), Default: sql.Raw("'a''b'")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("d"), Type: sql.RawType("numeric"), Default: sql.Raw("1.5")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("e"), Type: sql.RawType("integer"), Default: sql.Raw("NULL")}),
		`CREATE TABLE "t" (
    "a" boolean DEFAULT true,
    "b" boolean DEFAULT false,
    "c" text DEFAULT 'a''b',
    "d" numeric DEFAULT 1.5,
    "e" integer DEFAULT NULL
);`)
}

// A DEFAULT that is a function call exercises the FuncCall branch of staticExpr.
func TestCreateTableFunctionDefault(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (x text DEFAULT lower('A'))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("x"), Type: sql.RawType("text"), Default: sql.Func("lower", sql.Raw("'A'"))}),
		`CREATE TABLE "t" (
    "x" text DEFAULT lower('A')
);`)
}

// A DEFAULT that is an arithmetic expression exercises the AExpr branch of
// staticExpr.
func TestCreateTableArithmeticDefault(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (x int DEFAULT 1 + 2)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("x"), Type: sql.RawType("integer"),
				Default: sql.BinaryExpression{Left: sql.Raw("1"), Operator: "+", Right: sql.Raw("2")}}),
		`CREATE TABLE "t" (
    "x" integer DEFAULT (1 + 2)
);`)
}

// A DEFAULT that is a typecast exercises the TypeCast branch of staticExpr.
func TestCreateTableTypeCastDefault(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (x text DEFAULT ('a'::text))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("x"), Type: sql.RawType("text"), Default: sql.Cast(sql.Raw("'a'"), "text")}),
		`CREATE TABLE "t" (
    "x" text DEFAULT CAST('a' AS text)
);`)
}

// A table-level CHECK with AND/OR/NOT exercises the BoolExpr branch of
// staticExpr.
func TestCreateTableCheckBooleanExpr(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (y int, CONSTRAINT c CHECK (y > 0 AND y < 10))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("y"), Type: sql.RawType("integer")}).
			Check("c", sql.And(
				sql.BinaryExpression{Left: sql.Col("y"), Operator: ">", Right: sql.Raw("0")},
				sql.BinaryExpression{Left: sql.Col("y"), Operator: "<", Right: sql.Raw("10")},
			)),
		`CREATE TABLE "t" (
    "y" integer,
    CONSTRAINT "c" CHECK ((("y" > 0) AND ("y" < 10)))
);`)
}

func TestCreateTableCheckNot(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (y int, CONSTRAINT c CHECK (NOT y = 0))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("y"), Type: sql.RawType("integer")}).
			Check("c", sql.Not(sql.BinaryExpression{Left: sql.Col("y"), Operator: "=", Right: sql.Raw("0")})),
		`CREATE TABLE "t" (
    "y" integer,
    CONSTRAINT "c" CHECK ((NOT ("y" = 0)))
);`)
}

// A CHECK predicate that staticExpr cannot model (a multi-column comparison via
// a sub-expression form it does not support) keeps the whole statement raw.
func TestCreateTableUnsupportedCheckFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE t (x int CHECK (x = ANY(ARRAY[1, 2])))")
}

// A table-level PRIMARY KEY constraint over several columns exercises the
// CONSTR_PRIMARY branch of convertCreateTable.
func TestCreateTableCompositePrimaryKey(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a int, b int, PRIMARY KEY (a, b))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("integer")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("integer")}).
			PrimaryKey("a", "b"),
		`CREATE TABLE "t" (
    "a" integer,
    "b" integer,
    PRIMARY KEY ("a", "b")
);`)
}

// An inline column PRIMARY KEY is hoisted to a table-level PRIMARY KEY clause.
func TestCreateTableInlinePrimaryKey(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (id int PRIMARY KEY)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("integer")}).
			PrimaryKey("id"),
		`CREATE TABLE "t" (
    "id" integer,
    PRIMARY KEY ("id")
);`)
}

// An explicit NULL column constraint is the default and is recorded as nothing.
func TestCreateTableExplicitNull(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a int NULL)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("integer")}),
		`CREATE TABLE "t" (
    "a" integer
);`)
}

// A table-level FOREIGN KEY constraint exercises the CONSTR_FOREIGN branch of
// convertCreateTable.
func TestCreateTableForeignKeyConstraint(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (uid int, FOREIGN KEY (uid) REFERENCES users (id))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("uid"), Type: sql.RawType("integer")}).
			ForeignKey(sql.ForeignKeyConstraint{
				Columns: sql.Idents("uid"), RefTable: sql.NewIdent("users"), RefColumns: sql.Idents("id"),
			}),
		`CREATE TABLE "t" (
    "uid" integer,
    CONSTRAINT "" FOREIGN KEY ("uid") REFERENCES "users" ("id")
);`)
}

// An inline column REFERENCES to a schema-qualified table exercises the schema
// branch of convertColumnReference.
func TestCreateTableInlineReferenceQualified(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (uid int REFERENCES s.users (id))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("uid"), Type: sql.RawType("integer"),
				References: &sql.ColumnReference{Schema: sql.NewIdent("s"), Table: sql.NewIdent("users"), Column: sql.NewIdent("id")}}),
		`CREATE TABLE "t" (
    "uid" integer REFERENCES "s"."users" ("id")
);`)
}

// A table element the typed converter cannot model (an EXCLUDE constraint) keeps
// the whole statement raw.
func TestCreateTableExcludeConstraintFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE t (a int, EXCLUDE USING gist (a WITH =))")
}

// A generated-identity column is not modeled and round-trips raw.
func TestCreateTableGeneratedIdentityFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE t (a int GENERATED ALWAYS AS IDENTITY)")
}
