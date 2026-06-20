package sql

import (
	"strings"
	"testing"
)

func TestCreateTableInlineConstraints(t *testing.T) {
	stmt := CreateTable(Table{Name: "posts"}).
		Column(ColumnDef{Name: "id", Type: EngineType{Name: "int8"}, Identity: IdentityAlways, NotNull: true}).
		Column(ColumnDef{Name: "slug", Type: EngineType{Name: "text"}, NotNull: true, Unique: true}).
		Column(ColumnDef{Name: "author_id", Type: EngineType{Name: "int8"}, NotNull: true,
			References: &ColumnReference{Table: "users", Column: "id", OnDelete: "CASCADE"}}).
		Column(ColumnDef{Name: "search", Type: EngineType{Name: "text"},
			Generated: &GeneratedColumn{Expression: Raw("lower(slug)"), Stored: true}}).
		PrimaryKey("id")

	got := mustDDL(t, stmt, "postgres")
	want := `CREATE TABLE "posts" (
    "id" bigint GENERATED ALWAYS AS IDENTITY NOT NULL,
    "slug" text NOT NULL UNIQUE,
    "author_id" bigint NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,
    "search" text GENERATED ALWAYS AS (lower(slug)) STORED,
    PRIMARY KEY ("id")
);`
	if got != want {
		t.Errorf("CreateTable(inline) =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableTableLevelUniqueAndForeignKey(t *testing.T) {
	stmt := CreateTable(Table{Schema: "public", Name: "memberships"}).
		Column(ColumnDef{Name: "user_id", Type: EngineType{Name: "int8"}, NotNull: true}).
		Column(ColumnDef{Name: "team_id", Type: EngineType{Name: "int8"}, NotNull: true}).
		Unique("uq_membership", "user_id", "team_id").
		ForeignKey(ForeignKeyConstraint{
			Name: "fk_user", Columns: []string{"user_id"},
			RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE",
		})

	got := mustDDL(t, stmt, "postgres")
	want := `CREATE TABLE "public"."memberships" (
    "user_id" bigint NOT NULL,
    "team_id" bigint NOT NULL,
    CONSTRAINT "uq_membership" UNIQUE ("user_id", "team_id"),
    CONSTRAINT "fk_user" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON DELETE CASCADE
);`
	if got != want {
		t.Errorf("CreateTable(table-level) =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableUnnamedUnique(t *testing.T) {
	stmt := CreateTable(Table{Name: "t"}).
		Column(ColumnDef{Name: "a", Type: EngineType{Name: "int8"}}).
		Unique("", "a")
	got := mustDDL(t, stmt, "postgres")
	if !strings.Contains(got, `    UNIQUE ("a")`) {
		t.Errorf("expected an unnamed UNIQUE (\"a\"), got:\n%s", got)
	}
}

func TestIdentityRejectedOnMySQL(t *testing.T) {
	stmt := CreateTable(Table{Name: "t"}).
		Column(ColumnDef{Name: "id", Type: EngineType{Name: "bigint"}, Identity: IdentityAlways})
	if _, _, err := stmt.ToSQL(mustDialect(t, "mysql")); err == nil || !strings.Contains(err.Error(), "IDENTITY") {
		t.Fatalf("ToSQL(mysql) error = %v, want IDENTITY rejection", err)
	}
}

func TestAlterTableAddUnique(t *testing.T) {
	stmt := AlterTable(Table{Name: "users"}).AddUnique("uq_email", "email")
	got := mustDDL(t, stmt, "postgres")
	if want := `ALTER TABLE "users" ADD CONSTRAINT "uq_email" UNIQUE ("email");`; got != want {
		t.Errorf("AddUnique() = %q, want %q", got, want)
	}
}

func TestGeneratedColumnMySQL(t *testing.T) {
	stmt := CreateTable(Table{Name: "t"}).
		Column(ColumnDef{Name: "a", Type: EngineType{Name: "int"}, NotNull: true}).
		Column(ColumnDef{Name: "b", Type: EngineType{Name: "int"},
			Generated: &GeneratedColumn{Expression: Raw("a * 2")}})
	got := mustDDL(t, stmt, "mysql")
	if !strings.Contains(got, "`b` int GENERATED ALWAYS AS (a * 2) VIRTUAL") {
		t.Errorf("expected a VIRTUAL generated column, got:\n%s", got)
	}
}

func mustDDL(t *testing.T, s Statement, dialect string) string {
	t.Helper()
	out, args, err := s.ToSQL(mustDialect(t, dialect))
	if err != nil {
		t.Fatalf("ToSQL(%s) error = %v", dialect, err)
	}
	if len(args) != 0 {
		t.Fatalf("ToSQL(%s) returned %d bind args, want 0 (DDL binds no values)", dialect, len(args))
	}
	return out
}

func TestCreateSchema(t *testing.T) {
	if got, want := mustDDL(t, CreateSchema("auth"), "postgres"), `CREATE SCHEMA "auth";`; got != want {
		t.Errorf("CreateSchema(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, CreateSchema("auth").IfNotExists(), "postgres"), `CREATE SCHEMA IF NOT EXISTS "auth";`; got != want {
		t.Errorf("CreateSchema().IfNotExists(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, CreateSchema("auth").IfNotExists(), "mysql"), "CREATE SCHEMA IF NOT EXISTS `auth`;"; got != want {
		t.Errorf("CreateSchema().IfNotExists(mysql) = %q, want %q", got, want)
	}
}

func TestDropSchema(t *testing.T) {
	if got, want := mustDDL(t, DropSchema("auth").IfExists(), "postgres"), `DROP SCHEMA IF EXISTS "auth";`; got != want {
		t.Errorf("DropSchema().IfExists(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, DropSchema("auth").IfExists().Cascade(), "postgres"), `DROP SCHEMA IF EXISTS "auth" CASCADE;`; got != want {
		t.Errorf("DropSchema().Cascade(postgres) = %q, want %q", got, want)
	}
	// MySQL has no CASCADE clause; the flag is ignored.
	if got, want := mustDDL(t, DropSchema("auth").IfExists().Cascade(), "mysql"), "DROP SCHEMA IF EXISTS `auth`;"; got != want {
		t.Errorf("DropSchema().Cascade(mysql) = %q, want %q", got, want)
	}
}

func TestSchemaStmtRequiresName(t *testing.T) {
	if _, _, err := CreateSchema("").ToSQL(mustDialect(t, "postgres")); err == nil {
		t.Error("CreateSchema(\"\"): expected error")
	}
	if _, _, err := DropSchema("").ToSQL(mustDialect(t, "postgres")); err == nil {
		t.Error("DropSchema(\"\"): expected error")
	}
}

func TestCreateTablePostgres(t *testing.T) {
	stmt := CreateTable(Table{Schema: "public", Name: "users"}).
		Column(ColumnDef{Name: "id", Type: EngineType{Name: "int8"}, NotNull: true, AutoIncrement: true}).
		Column(ColumnDef{Name: "email", Type: EngineType{Name: "varchar", Length: 255}, NotNull: true, Comment: "Unique login address"}).
		Column(ColumnDef{Name: "name", Type: EngineType{Name: "text"}}).
		Column(ColumnDef{Name: "balance", Type: EngineType{Name: "numeric", Precision: 10, Scale: 2}, NotNull: true}).
		Column(ColumnDef{Name: "created_at", Type: EngineType{Name: "timestamptz"}, NotNull: true, Default: Raw("now()")}).
		PrimaryKey("id").
		Check("ck_users_1", Raw("balance >= 0")).
		Comment("Registered blog authors")

	got := mustDDL(t, stmt, "postgres")
	want := `CREATE TABLE "public"."users" (
    "id" bigserial,
    "email" varchar(255) NOT NULL,
    "name" text,
    "balance" numeric(10, 2) NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "ck_users_1" CHECK (balance >= 0)
);`
	if got != want {
		t.Errorf("CreateTable() =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableMySQL(t *testing.T) {
	stmt := CreateTable(Table{Schema: "public", Name: "users"}).
		Column(ColumnDef{Name: "id", Type: EngineType{Name: "bigint"}, NotNull: true, AutoIncrement: true}).
		Column(ColumnDef{Name: "email", Type: EngineType{Name: "varchar", Length: 255}, NotNull: true, Comment: "Unique login address"}).
		PrimaryKey("id").
		Comment("Registered blog authors")

	got := mustDDL(t, stmt, "mysql")
	want := "CREATE TABLE `public`.`users` (\n" +
		"    `id` bigint NOT NULL AUTO_INCREMENT,\n" +
		"    `email` varchar(255) NOT NULL COMMENT 'Unique login address',\n" +
		"    PRIMARY KEY (`id`)\n" +
		") COMMENT='Registered blog authors';"
	if got != want {
		t.Errorf("CreateTable() =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableUnqualifiedAndIfNotExists(t *testing.T) {
	stmt := CreateTable(Table{Name: "items"}).
		Column(ColumnDef{Name: "id", Type: RawType("bigserial")}).
		PrimaryKey("id").
		IfNotExists()
	got := mustDDL(t, stmt, "postgres")
	want := `CREATE TABLE IF NOT EXISTS "items" (
    "id" bigserial,
    PRIMARY KEY ("id")
);`
	if got != want {
		t.Errorf("CreateTable() =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableDottedNameQuotedAsOne(t *testing.T) {
	stmt := CreateTable(Table{Name: "v1.users"}).
		Column(ColumnDef{Name: "id", Type: RawType("bigint")})
	got := mustDDL(t, stmt, "postgres")
	if want := `CREATE TABLE "v1.users" (`; got[:len(want)] != want {
		t.Errorf("dotted name should be one quoted identifier:\n%s", got)
	}
}

func TestDropTable(t *testing.T) {
	got := mustDDL(t, DropTable(Table{Schema: "public", Name: "users"}).IfExists(), "postgres")
	if want := `DROP TABLE IF EXISTS "public"."users";`; got != want {
		t.Errorf("DropTable() = %q, want %q", got, want)
	}
}

func TestDropTableMultiple(t *testing.T) {
	stmt := DropTable(
		Table{Schema: "public", Name: "users"},
		Table{Schema: "public", Name: "posts"},
	).IfExists()
	got := mustDDL(t, stmt, "postgres")
	if want := `DROP TABLE IF EXISTS "public"."users", "public"."posts";`; got != want {
		t.Errorf("DropTable(multiple) = %q, want %q", got, want)
	}
}

func TestCreateIndex(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}
	got := mustDDL(t, CreateIndex("uq_users_email", users, "email").Unique(), "postgres")
	if want := `CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");`; got != want {
		t.Errorf("CreateIndex() = %q, want %q", got, want)
	}
	got = mustDDL(t, CreateIndex("idx_users_name", users, "name"), "postgres")
	if want := `CREATE INDEX "idx_users_name" ON "public"."users" ("name");`; got != want {
		t.Errorf("CreateIndex() = %q, want %q", got, want)
	}
}

func TestDropIndex(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}
	if got, want := mustDDL(t, DropIndex("uq_users_email", users), "postgres"),
		`DROP INDEX IF EXISTS "public"."uq_users_email";`; got != want {
		t.Errorf("DropIndex(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, DropIndex("uq_users_email", users), "mysql"),
		"DROP INDEX `uq_users_email` ON `public`.`users`;"; got != want {
		t.Errorf("DropIndex(mysql) = %q, want %q", got, want)
	}
}

func TestAlterTableAddForeignKey(t *testing.T) {
	stmt := AlterTable(Table{Schema: "public", Name: "posts"}).
		AddForeignKey(ForeignKeyConstraint{
			Name: "fk_posts_user_id", Columns: []string{"user_id"},
			RefSchema: "public", RefTable: "users", RefColumns: []string{"id"},
		})
	got := mustDDL(t, stmt, "postgres")
	want := `ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id");`
	if got != want {
		t.Errorf("AlterTable() = %q, want %q", got, want)
	}
}

func TestDropForeignKey(t *testing.T) {
	posts := Table{Schema: "public", Name: "posts"}
	if got, want := mustDDL(t, DropForeignKey(posts, "fk_posts_user_id"), "postgres"),
		`ALTER TABLE "public"."posts" DROP CONSTRAINT IF EXISTS "fk_posts_user_id";`; got != want {
		t.Errorf("DropForeignKey(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, DropForeignKey(posts, "fk_posts_user_id"), "mysql"),
		"ALTER TABLE `public`.`posts` DROP FOREIGN KEY `fk_posts_user_id`;"; got != want {
		t.Errorf("DropForeignKey(mysql) = %q, want %q", got, want)
	}
}

func TestAlterTableColumnOps(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}
	cases := []struct {
		name string
		stmt Statement
		want string
	}{
		{"add column", AlterTable(users).AddColumn(ColumnDef{Name: "age", Type: EngineType{Name: "int4"}, NotNull: true}),
			`ALTER TABLE "public"."users" ADD COLUMN "age" integer NOT NULL;`},
		{"drop column", AlterTable(users).DropColumn("age"),
			`ALTER TABLE "public"."users" DROP COLUMN "age";`},
		{"alter type", AlterTable(users).AlterColumnType("age", EngineType{Name: "int8"}),
			`ALTER TABLE "public"."users" ALTER COLUMN "age" TYPE bigint;`},
		{"set not null", AlterTable(users).SetNotNull("name"),
			`ALTER TABLE "public"."users" ALTER COLUMN "name" SET NOT NULL;`},
		{"drop not null", AlterTable(users).DropNotNull("name"),
			`ALTER TABLE "public"."users" ALTER COLUMN "name" DROP NOT NULL;`},
		{"set default", AlterTable(users).SetDefault("created_at", Raw("now()")),
			`ALTER TABLE "public"."users" ALTER COLUMN "created_at" SET DEFAULT now();`},
		{"drop default", AlterTable(users).DropDefault("created_at"),
			`ALTER TABLE "public"."users" ALTER COLUMN "created_at" DROP DEFAULT;`},
		{"rename column", AlterTable(users).RenameColumn("name", "full_name"),
			`ALTER TABLE "public"."users" RENAME COLUMN "name" TO "full_name";`},
		{"rename table", AlterTable(users).RenameTo("accounts"),
			`ALTER TABLE "public"."users" RENAME TO "accounts";`},
		{"multi action", AlterTable(users).AddColumn(ColumnDef{Name: "a", Type: RawType("int")}).DropColumn("b"),
			`ALTER TABLE "public"."users" ADD COLUMN "a" int, DROP COLUMN "b";`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustDDL(t, tc.stmt, "postgres"); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAlterTableColumnModifyMySQLRejected(t *testing.T) {
	stmt := AlterTable(Table{Name: "users"}).SetNotNull("name")
	if _, _, err := stmt.ToSQL(mustDialect(t, "mysql")); err == nil {
		t.Error("expected error for column modify on mysql")
	}
}

func TestCommentStatements(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}
	if got, want := mustDDL(t, CommentOnTable(users, "Registered authors"), "postgres"),
		`COMMENT ON TABLE "public"."users" IS 'Registered authors';`; got != want {
		t.Errorf("CommentOnTable = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, CommentOnColumn(users, "email", "Login address"), "postgres"),
		`COMMENT ON COLUMN "public"."users"."email" IS 'Login address';`; got != want {
		t.Errorf("CommentOnColumn = %q, want %q", got, want)
	}
	// MySQL comments inline, so the standalone form renders empty.
	if got := mustDDL(t, CommentOnTable(users, "x"), "mysql"); got != "" {
		t.Errorf("CommentOnTable(mysql) = %q, want empty", got)
	}
}

func TestStatementsRender(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}
	stmts := Statements{
		CreateTable(users).Column(ColumnDef{Name: "id", Type: RawType("bigserial")}).PrimaryKey("id"),
		CreateIndex("idx_users_id", users, "id"),
		CommentOnTable(users, "x"), // empty on mysql, skipped
	}
	out, err := stmts.Render(mustDialect(t, "mysql"))
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	want := "CREATE TABLE `public`.`users` (\n" +
		"    `id` bigserial,\n" +
		"    PRIMARY KEY (`id`)\n" +
		");\n\n" +
		"CREATE INDEX `idx_users_id` ON `public`.`users` (`id`);"
	if out != want {
		t.Errorf("Render() =\n%s\nwant\n%s", out, want)
	}
}

func TestForeignKeyWithActions(t *testing.T) {
	stmt := AlterTable(Table{Schema: "public", Name: "posts"}).
		AddForeignKey(ForeignKeyConstraint{
			Name: "fk_posts_user_id", Columns: []string{"user_id"},
			RefSchema: "public", RefTable: "users", RefColumns: []string{"id"},
			OnDelete: "CASCADE", OnUpdate: "RESTRICT",
		})
	got := mustDDL(t, stmt, "postgres")
	want := `ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE RESTRICT;`
	if got != want {
		t.Errorf("AddForeignKey() =\n%s\nwant\n%s", got, want)
	}
}

func TestRestateColumn(t *testing.T) {
	col := ColumnDef{Name: "name", Type: EngineType{Name: "text"}, Default: Raw("''")}
	stmt := AlterTable(Table{Name: "users"}).RestateColumn(col)

	if got, want := mustDDL(t, stmt, "mysql"), "ALTER TABLE `users` MODIFY COLUMN `name` text DEFAULT '';"; got != want {
		t.Errorf("RestateColumn(mysql) =\n%s\nwant\n%s", got, want)
	}
	got := mustDDL(t, stmt, "postgres")
	want := `ALTER TABLE "users" ALTER COLUMN "name" TYPE text, ALTER COLUMN "name" DROP NOT NULL, ALTER COLUMN "name" SET DEFAULT '';`
	if got != want {
		t.Errorf("RestateColumn(postgres) =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableTemporary(t *testing.T) {
	stmt := CreateTable(Table{Name: "scratch"}).
		Column(ColumnDef{Name: "id", Type: RawType("bigint")}).
		Temporary()
	got := mustDDL(t, stmt, "postgres")
	want := `CREATE TEMPORARY TABLE "scratch" (
    "id" bigint
);`
	if got != want {
		t.Errorf("CreateTable().Temporary() =\n%s\nwant\n%s", got, want)
	}
	// The TEMPORARY keyword composes with IF NOT EXISTS and renders on MySQL too.
	got = mustDDL(t, stmt.IfNotExists(), "mysql")
	if want := "CREATE TEMPORARY TABLE IF NOT EXISTS `scratch` (\n    `id` bigint\n);"; got != want {
		t.Errorf("CreateTable().Temporary().IfNotExists(mysql) =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateView(t *testing.T) {
	body := Select(Col("id"), Col("email")).
		From(Table{Schema: "public", Name: "users"}).
		Where(Raw("active"))

	got := mustDDL(t, CreateView(Table{Schema: "public", Name: "active_users"}, body), "postgres")
	want := "CREATE VIEW \"public\".\"active_users\" AS\n" +
		`SELECT "id", "email" FROM "public"."users" WHERE active;`
	if got != want {
		t.Errorf("CreateView() =\n%s\nwant\n%s", got, want)
	}

	// OR REPLACE and explicit column names.
	got = mustDDL(t, CreateView(Table{Name: "v"}, body).OrReplace().Columns("a", "b"), "mysql")
	want = "CREATE OR REPLACE VIEW `v` (`a`, `b`) AS\n" +
		"SELECT `id`, `email` FROM `public`.`users` WHERE active;"
	if got != want {
		t.Errorf("CreateView().OrReplace().Columns() =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateViewRejectsBoundValues(t *testing.T) {
	// A view body that binds a value cannot be a DDL statement (no bind args);
	// the literal must be inlined with Raw.
	body := Select(Star()).From(Table{Name: "t"}).Where(Eq(Col("x"), Int(5)))
	if _, _, err := CreateView(Table{Name: "v"}, body).ToSQL(mustDialect(t, "postgres")); err == nil {
		t.Error("CreateView with a value-binding body: expected error")
	}
}

func TestDropView(t *testing.T) {
	v := Table{Schema: "public", Name: "active_users"}
	if got, want := mustDDL(t, DropView(v).IfExists(), "postgres"),
		`DROP VIEW IF EXISTS "public"."active_users";`; got != want {
		t.Errorf("DropView().IfExists(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, DropView(v).IfExists().Cascade(), "postgres"),
		`DROP VIEW IF EXISTS "public"."active_users" CASCADE;`; got != want {
		t.Errorf("DropView().Cascade(postgres) = %q, want %q", got, want)
	}
	// MySQL has no CASCADE clause for DROP VIEW; the flag is ignored.
	if got, want := mustDDL(t, DropView(v).Cascade(), "mysql"),
		"DROP VIEW `public`.`active_users`;"; got != want {
		t.Errorf("DropView().Cascade(mysql) = %q, want %q", got, want)
	}
}

func TestPrimaryKeyAlterations(t *testing.T) {
	add := AlterTable(Table{Name: "t"}).AddPrimaryKey("a", "b")
	if got, want := mustDDL(t, add, "postgres"), `ALTER TABLE "t" ADD PRIMARY KEY ("a", "b");`; got != want {
		t.Errorf("AddPrimaryKey =\n%s\nwant\n%s", got, want)
	}
	drop := AlterTable(Table{Name: "t"}).DropPrimaryKey()
	if got, want := mustDDL(t, drop, "mysql"), "ALTER TABLE `t` DROP PRIMARY KEY;"; got != want {
		t.Errorf("DropPrimaryKey(mysql) =\n%s\nwant\n%s", got, want)
	}
	if got, want := mustDDL(t, drop, "postgres"), `ALTER TABLE "t" DROP CONSTRAINT "t_pkey";`; got != want {
		t.Errorf("DropPrimaryKey(postgres) =\n%s\nwant\n%s", got, want)
	}
}

// TestStandardSQLDDL covers the ANSI/standard dialect's DDL rendering: it drops
// a foreign key with the standard DROP CONSTRAINT and an index without the
// vendor-specific IF EXISTS / ON table clauses, and has no COMMENT syntax.
func TestStandardSQLDDL(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}
	posts := Table{Schema: "public", Name: "posts"}

	if got, want := mustDDL(t, DropTable(users).IfExists(), "standard"),
		`DROP TABLE IF EXISTS "public"."users";`; got != want {
		t.Errorf("DropTable(standard) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, DropIndex("uq_users_email", users), "standard"),
		`DROP INDEX "public"."uq_users_email";`; got != want {
		t.Errorf("DropIndex(standard) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, DropForeignKey(posts, "fk_posts_user_id"), "standard"),
		`ALTER TABLE "public"."posts" DROP CONSTRAINT "fk_posts_user_id";`; got != want {
		t.Errorf("DropForeignKey(standard) = %q, want %q", got, want)
	}
	if got := mustDDL(t, CommentOnTable(users, "hi"), "standard"); got != "" {
		t.Errorf("CommentOnTable(standard) = %q, want empty", got)
	}
	if got := mustDDL(t, CommentOnColumn(users, "email", "addr"), "standard"); got != "" {
		t.Errorf("CommentOnColumn(standard) = %q, want empty", got)
	}
}

// TestMySQLCommentStatementsAreInline covers MySQL's standalone comment
// statements rendering empty: MySQL carries comments inline in the column/table
// definition, so CommentOnTable/CommentOnColumn produce no separate statement.
func TestMySQLCommentStatementsAreInline(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}
	if got := mustDDL(t, CommentOnTable(users, "hi"), "mysql"); got != "" {
		t.Errorf("CommentOnTable(mysql) = %q, want empty (comments are inline)", got)
	}
	if got := mustDDL(t, CommentOnColumn(users, "email", "addr"), "mysql"); got != "" {
		t.Errorf("CommentOnColumn(mysql) = %q, want empty (comments are inline)", got)
	}
}

func TestMigrationDDLCompile(t *testing.T) {
	cases := []struct {
		stmt Statement
		want string
	}{
		{CreateExtension("pgcrypto").IfNotExists(), `CREATE EXTENSION IF NOT EXISTS "pgcrypto";`},
		{CreateExtension("uuid-ossp").Schema("ext"), `CREATE EXTENSION "uuid-ossp" WITH SCHEMA "ext";`},
		{DropExtension("a", "b").IfExists(), `DROP EXTENSION IF EXISTS "a", "b";`},
		{CreateEnum("mood", "sad", "happy"), `CREATE TYPE "mood" AS ENUM ('sad', 'happy');`},
		{DropType("mood").IfExists().Cascade(), `DROP TYPE IF EXISTS "mood" CASCADE;`},
		{RenameTable(Table{Schema: "app", Name: "users"}, "people"), `ALTER TABLE "app"."users" RENAME TO "people";`},
		{RenameColumn(Table{Name: "users"}, "email", "addr"), `ALTER TABLE "users" RENAME COLUMN "email" TO "addr";`},
	}
	for _, tc := range cases {
		got, args, err := tc.stmt.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if got != tc.want {
			t.Errorf("ToSQL() = %q, want %q", got, tc.want)
		}
		if len(args) != 0 {
			t.Errorf("args = %#v, want none", args)
		}
	}
}

func TestMigrationDDLValidation(t *testing.T) {
	if _, _, err := CreateExtension("").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("CREATE EXTENSION without a name succeeded, want error")
	}
	if _, _, err := RenameColumn(Table{Name: "t"}, "a", "").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("RENAME COLUMN without a new name succeeded, want error")
	}
}

func TestTruncate(t *testing.T) {
	users := Table{Schema: "public", Name: "users"}

	if got, want := mustDDL(t, Truncate(users), "postgres"), `TRUNCATE TABLE "public"."users";`; got != want {
		t.Fatalf("TRUNCATE = %q, want %q", got, want)
	}

	if got, want := mustDDL(t, Truncate(users), "mysql"), "TRUNCATE TABLE `public`.`users`;"; got != want {
		t.Fatalf("TRUNCATE(mysql) = %q, want %q", got, want)
	}
}

func TestTruncateOptions(t *testing.T) {
	users := Table{Name: "users"}
	logs := Table{Name: "logs"}

	got := mustDDL(t, Truncate(users, logs).RestartIdentity().Cascade(), "postgres")
	want := `TRUNCATE TABLE "users", "logs" RESTART IDENTITY CASCADE;`
	if got != want {
		t.Fatalf("TRUNCATE options = %q, want %q", got, want)
	}

	// On MySQL, RESTART IDENTITY is implicit and renders nothing.
	if got, want := mustDDL(t, Truncate(users).RestartIdentity(), "mysql"), "TRUNCATE TABLE `users`;"; got != want {
		t.Fatalf("TRUNCATE(mysql, restart) = %q, want %q", got, want)
	}
}

func TestTruncateRejections(t *testing.T) {
	users := Table{Name: "users"}
	logs := Table{Name: "logs"}

	cases := []struct {
		name    string
		stmt    Statement
		dialect string
		want    string
	}{
		{"mysql multiple", Truncate(users, logs), "mysql", "multiple tables"},
		{"mysql cascade", Truncate(users).Cascade(), "mysql", "CASCADE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := mustDialect(t, tc.dialect)
			if _, _, err := tc.stmt.ToSQL(d); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ToSQL(%s) error = %v, want containing %q", tc.dialect, err, tc.want)
			}
		})
	}
}

func TestDDLStmt(t *testing.T) {
	users := Table{Name: "users"}
	orders := Table{Name: "orders"}

	alter := AlterTable(users).DropColumn("x").RenameColumn("a", "b").Stmt()
	if alter.Table.Name != "users" {
		t.Errorf("AlterTable Table = %q, want users", alter.Table.Name)
	}
	if len(alter.Alterations) != 2 {
		t.Fatalf("Alterations = %d, want 2", len(alter.Alterations))
	}
	if alter.Alterations[0].Kind != AlterDropColumn || alter.Alterations[0].Name != "x" {
		t.Errorf("first alteration = %+v, want DROP COLUMN x", alter.Alterations[0])
	}
	if a := alter.Alterations[1]; a.Kind != AlterRenameColumn || a.Name != "a" || a.NewName != "b" {
		t.Errorf("second alteration = %+v, want RENAME COLUMN a -> b", a)
	}

	dropped := DropTable(users, orders).Stmt().Tables
	if len(dropped) != 2 || dropped[0].Name != "users" || dropped[1].Name != "orders" {
		t.Errorf("DropTable Tables = %v, want [users orders]", dropped)
	}

	created := CreateTable(users).
		Column(ColumnDef{Name: "id", Type: RawType("int")}).
		PrimaryKey("id").Stmt()
	if created.Table.Name != "users" || len(created.Columns) != 1 || len(created.PrimaryKey) != 1 {
		t.Errorf("CreateTable parts = %+v, want one column and a primary key", created)
	}
}
