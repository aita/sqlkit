package sql

import (
	"fmt"
	"strings"
	"testing"
)

func TestCreateTableInlineConstraints(t *testing.T) {
	stmt := CreateTable(Table{Name: NewIdent("posts")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: EngineType{Name: "int8"}, Identity: IdentityAlways, NotNull: true}).
		Column(ColumnDef{Name: NewIdent("slug"), Type: EngineType{Name: "text"}, NotNull: true, Unique: true}).
		Column(ColumnDef{Name: NewIdent("author_id"), Type: EngineType{Name: "int8"}, NotNull: true,
			References: &ColumnReference{Table: NewIdent("users"), Column: NewIdent("id"), OnDelete: "CASCADE"}}).
		Column(ColumnDef{Name: NewIdent("search"), Type: EngineType{Name: "text"},
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
	stmt := CreateTable(Table{Schema: NewIdent("public"), Name: NewIdent("memberships")}).
		Column(ColumnDef{Name: NewIdent("user_id"), Type: EngineType{Name: "int8"}, NotNull: true}).
		Column(ColumnDef{Name: NewIdent("team_id"), Type: EngineType{Name: "int8"}, NotNull: true}).
		Unique("uq_membership", "user_id", "team_id").
		ForeignKey(ForeignKeyConstraint{
			Name: NewIdent("fk_user"), Columns: Idents("user_id"),
			RefSchema: NewIdent("public"), RefTable: NewIdent("users"), RefColumns: Idents("id"), OnDelete: "CASCADE",
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

func TestCreateTableEmptyErrors(t *testing.T) {
	// A table with no columns or constraints would emit "CREATE TABLE t ();", which
	// is invalid SQL; the compiler must error instead.
	stmt := CreateTable(Table{Name: NewIdent("t")})
	if _, _, err := stmt.ToSQL(mustDialect(t, "postgres")); err == nil || !strings.Contains(err.Error(), "at least one column") {
		t.Fatalf("ToSQL() error = %v, want empty-table rejection", err)
	}
}

func TestForeignKeyEmptyColumnsErrors(t *testing.T) {
	// An empty referencing or referenced column list would emit "FOREIGN KEY ()
	// REFERENCES t ()", which is invalid; the compiler must error, like UNIQUE/KEY.
	stmt := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "int8"}}).
		ForeignKey(ForeignKeyConstraint{
			Name: NewIdent("fk"), Columns: nil,
			RefTable: NewIdent("users"), RefColumns: Idents("id"),
		})
	if _, _, err := stmt.ToSQL(mustDialect(t, "postgres")); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY requires") {
		t.Fatalf("ToSQL() error = %v, want empty-FK rejection", err)
	}
}

func TestCreateTableUnnamedUnique(t *testing.T) {
	stmt := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "int8"}}).
		Unique("", "a")
	got := mustDDL(t, stmt, "postgres")
	if !strings.Contains(got, `    UNIQUE ("a")`) {
		t.Errorf("expected an unnamed UNIQUE (\"a\"), got:\n%s", got)
	}
}

func TestIdentityRejectedOnMySQL(t *testing.T) {
	stmt := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: EngineType{Name: "bigint"}, Identity: IdentityAlways})
	if _, _, err := stmt.ToSQL(mustDialect(t, "mysql")); err == nil || !strings.Contains(err.Error(), "IDENTITY") {
		t.Fatalf("ToSQL(mysql) error = %v, want IDENTITY rejection", err)
	}
}

func TestAlterTableAddUnique(t *testing.T) {
	stmt := AlterTable(Table{Name: NewIdent("users")}).AddUnique("uq_email", "email")
	got := mustDDL(t, stmt, "postgres")
	if want := `ALTER TABLE "users" ADD CONSTRAINT "uq_email" UNIQUE ("email");`; got != want {
		t.Errorf("AddUnique() = %q, want %q", got, want)
	}
}

func TestGeneratedColumnMySQL(t *testing.T) {
	stmt := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "int"}, NotNull: true}).
		Column(ColumnDef{Name: NewIdent("b"), Type: EngineType{Name: "int"},
			Generated: &GeneratedColumn{Expression: Raw("a * 2")}})
	got := mustDDL(t, stmt, "mysql")
	if !strings.Contains(got, "`b` int GENERATED ALWAYS AS (a * 2) VIRTUAL") {
		t.Errorf("expected a VIRTUAL generated column, got:\n%s", got)
	}
}

// TestGeneratedColumnVirtualRejectedOnPostgreSQL covers PostgreSQL's lack of
// VIRTUAL generated columns: a Stored:false generated column must be rejected at
// compile time rather than emit "GENERATED ALWAYS AS (...) VIRTUAL", which the
// PostgreSQL grammar rejects. STORED still compiles.
func TestGeneratedColumnVirtualRejectedOnPostgreSQL(t *testing.T) {
	virtual := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "int"}}).
		Column(ColumnDef{Name: NewIdent("b"), Type: EngineType{Name: "int"},
			Generated: &GeneratedColumn{Expression: Raw("a + 1")}})
	if _, _, err := virtual.ToSQL(mustDialect(t, "postgres")); err == nil || !strings.Contains(err.Error(), "VIRTUAL") {
		t.Fatalf("ToSQL(postgres) err = %v, want a VIRTUAL rejection", err)
	}

	stored := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("b"), Type: EngineType{Name: "int"},
			Generated: &GeneratedColumn{Expression: Raw("a + 1"), Stored: true}})
	if got := mustDDL(t, stored, "postgres"); !strings.Contains(got, "STORED") {
		t.Errorf("STORED generated column should compile on postgres, got:\n%s", got)
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
	stmt := CreateTable(Table{Schema: NewIdent("public"), Name: NewIdent("users")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: EngineType{Name: "int8"}, NotNull: true, AutoIncrement: true}).
		Column(ColumnDef{Name: NewIdent("email"), Type: EngineType{Name: "varchar", Length: 255}, NotNull: true, Comment: "Unique login address"}).
		Column(ColumnDef{Name: NewIdent("name"), Type: EngineType{Name: "text"}}).
		Column(ColumnDef{Name: NewIdent("balance"), Type: EngineType{Name: "numeric", Precision: 10, Scale: 2}, NotNull: true}).
		Column(ColumnDef{Name: NewIdent("created_at"), Type: EngineType{Name: "timestamptz"}, NotNull: true, Default: Raw("now()")}).
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
	stmt := CreateTable(Table{Schema: NewIdent("public"), Name: NewIdent("users")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: EngineType{Name: "bigint"}, NotNull: true, AutoIncrement: true}).
		Column(ColumnDef{Name: NewIdent("email"), Type: EngineType{Name: "varchar", Length: 255}, NotNull: true, Comment: "Unique login address"}).
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
	stmt := CreateTable(Table{Name: NewIdent("items")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: RawType("bigserial")}).
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
	stmt := CreateTable(Table{Name: NewIdent("v1.users")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: RawType("bigint")})
	got := mustDDL(t, stmt, "postgres")
	if want := `CREATE TABLE "v1.users" (`; got[:len(want)] != want {
		t.Errorf("dotted name should be one quoted identifier:\n%s", got)
	}
}

func TestDropTable(t *testing.T) {
	got := mustDDL(t, DropTable(Table{Schema: NewIdent("public"), Name: NewIdent("users")}).IfExists(), "postgres")
	if want := `DROP TABLE IF EXISTS "public"."users";`; got != want {
		t.Errorf("DropTable() = %q, want %q", got, want)
	}
}

func TestDropTableMultiple(t *testing.T) {
	stmt := DropTable(
		Table{Schema: NewIdent("public"), Name: NewIdent("users")},
		Table{Schema: NewIdent("public"), Name: NewIdent("posts")},
	).IfExists()
	got := mustDDL(t, stmt, "postgres")
	if want := `DROP TABLE IF EXISTS "public"."users", "public"."posts";`; got != want {
		t.Errorf("DropTable(multiple) = %q, want %q", got, want)
	}
}

func TestCreateIndex(t *testing.T) {
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
	got := mustDDL(t, CreateIndex("uq_users_email", users, "email").Unique(), "postgres")
	if want := `CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");`; got != want {
		t.Errorf("CreateIndex() = %q, want %q", got, want)
	}
	got = mustDDL(t, CreateIndex("idx_users_name", users, "name"), "postgres")
	if want := `CREATE INDEX "idx_users_name" ON "public"."users" ("name");`; got != want {
		t.Errorf("CreateIndex() = %q, want %q", got, want)
	}
}

// MySQL has no IF NOT EXISTS for CREATE INDEX (any version); the compiler must
// reject it rather than emit SQL MySQL's parser refuses. PostgreSQL supports it.
func TestCreateIndexIfNotExistsRejectedOnMySQL(t *testing.T) {
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
	stmt := CreateIndex("idx_users_name", users, "name").IfNotExists()
	if _, _, err := stmt.ToSQL(mustDialect(t, "mysql")); err == nil ||
		!strings.Contains(err.Error(), "CREATE INDEX ... IF NOT EXISTS") {
		t.Fatalf("ToSQL(mysql) error = %v, want CREATE INDEX IF NOT EXISTS rejection", err)
	}
	if got, want := mustDDL(t, stmt, "postgres"),
		`CREATE INDEX IF NOT EXISTS "idx_users_name" ON "public"."users" ("name");`; got != want {
		t.Errorf("CreateIndex().IfNotExists(postgres) = %q, want %q", got, want)
	}
}

func TestDropIndex(t *testing.T) {
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
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
	stmt := AlterTable(Table{Schema: NewIdent("public"), Name: NewIdent("posts")}).
		AddForeignKey(ForeignKeyConstraint{
			Name: NewIdent("fk_posts_user_id"), Columns: Idents("user_id"),
			RefSchema: NewIdent("public"), RefTable: NewIdent("users"), RefColumns: Idents("id"),
		})
	got := mustDDL(t, stmt, "postgres")
	want := `ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id");`
	if got != want {
		t.Errorf("AlterTable() = %q, want %q", got, want)
	}
}

func TestDropForeignKey(t *testing.T) {
	posts := Table{Schema: NewIdent("public"), Name: NewIdent("posts")}
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
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
	cases := []struct {
		name string
		stmt Statement
		want string
	}{
		{"add column", AlterTable(users).AddColumn(ColumnDef{Name: NewIdent("age"), Type: EngineType{Name: "int4"}, NotNull: true}),
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
		{"multi action", AlterTable(users).AddColumn(ColumnDef{Name: NewIdent("a"), Type: RawType("int")}).DropColumn("b"),
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
	stmt := AlterTable(Table{Name: NewIdent("users")}).SetNotNull("name")
	if _, _, err := stmt.ToSQL(mustDialect(t, "mysql")); err == nil {
		t.Error("expected error for column modify on mysql")
	}
}

func TestCommentStatements(t *testing.T) {
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
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
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
	stmts := Statements{
		CreateTable(users).Column(ColumnDef{Name: NewIdent("id"), Type: RawType("bigserial")}).PrimaryKey("id"),
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

	// A statement that fails to compile surfaces its error from Render.
	if _, err := (Statements{RenameTable(Table{}, "x")}).Render(mustDialect(t, "mysql")); err == nil {
		t.Fatal("Render of an invalid statement: want error")
	}
}

func TestForeignKeyWithActions(t *testing.T) {
	stmt := AlterTable(Table{Schema: NewIdent("public"), Name: NewIdent("posts")}).
		AddForeignKey(ForeignKeyConstraint{
			Name: NewIdent("fk_posts_user_id"), Columns: Idents("user_id"),
			RefSchema: NewIdent("public"), RefTable: NewIdent("users"), RefColumns: Idents("id"),
			OnDelete: "CASCADE", OnUpdate: "RESTRICT",
		})
	got := mustDDL(t, stmt, "postgres")
	want := `ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE RESTRICT;`
	if got != want {
		t.Errorf("AddForeignKey() =\n%s\nwant\n%s", got, want)
	}
}

func TestRestateColumn(t *testing.T) {
	col := ColumnDef{Name: NewIdent("name"), Type: EngineType{Name: "text"}, Default: Raw("''")}
	stmt := AlterTable(Table{Name: NewIdent("users")}).RestateColumn(col)

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
	stmt := CreateTable(Table{Name: NewIdent("scratch")}).
		Column(ColumnDef{Name: NewIdent("id"), Type: RawType("bigint")}).
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
		From(Table{Schema: NewIdent("public"), Name: NewIdent("users")}).
		Where(Raw("active"))

	got := mustDDL(t, CreateView(Table{Schema: NewIdent("public"), Name: NewIdent("active_users")}, body), "postgres")
	want := "CREATE VIEW \"public\".\"active_users\" AS\n" +
		`SELECT "id", "email" FROM "public"."users" WHERE active;`
	if got != want {
		t.Errorf("CreateView() =\n%s\nwant\n%s", got, want)
	}

	// OR REPLACE and explicit column names.
	got = mustDDL(t, CreateView(Table{Name: NewIdent("v")}, body).OrReplace().Columns("a", "b"), "mysql")
	want = "CREATE OR REPLACE VIEW `v` (`a`, `b`) AS\n" +
		"SELECT `id`, `email` FROM `public`.`users` WHERE active;"
	if got != want {
		t.Errorf("CreateView().OrReplace().Columns() =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateViewRejectsBoundValues(t *testing.T) {
	// A view body that binds a value cannot be a DDL statement (no bind args);
	// the literal must be inlined with Raw.
	body := Select(Star()).From(Table{Name: NewIdent("t")}).Where(Eq(Col("x"), Int(5)))
	if _, _, err := CreateView(Table{Name: NewIdent("v")}, body).ToSQL(mustDialect(t, "postgres")); err == nil {
		t.Error("CreateView with a value-binding body: expected error")
	}
}

func TestDropView(t *testing.T) {
	v := Table{Schema: NewIdent("public"), Name: NewIdent("active_users")}
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
	add := AlterTable(Table{Name: NewIdent("t")}).AddPrimaryKey("a", "b")
	if got, want := mustDDL(t, add, "postgres"), `ALTER TABLE "t" ADD PRIMARY KEY ("a", "b");`; got != want {
		t.Errorf("AddPrimaryKey =\n%s\nwant\n%s", got, want)
	}
	drop := AlterTable(Table{Name: NewIdent("t")}).DropPrimaryKey()
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
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
	posts := Table{Schema: NewIdent("public"), Name: NewIdent("posts")}

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
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}
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
		{RenameTable(Table{Schema: NewIdent("app"), Name: NewIdent("users")}, "people"), `ALTER TABLE "app"."users" RENAME TO "people";`},
		{RenameColumn(Table{Name: NewIdent("users")}, "email", "addr"), `ALTER TABLE "users" RENAME COLUMN "email" TO "addr";`},
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
	if _, _, err := RenameColumn(Table{Name: NewIdent("t")}, "a", "").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("RENAME COLUMN without a new name succeeded, want error")
	}
}

func TestTruncate(t *testing.T) {
	users := Table{Schema: NewIdent("public"), Name: NewIdent("users")}

	if got, want := mustDDL(t, Truncate(users), "postgres"), `TRUNCATE TABLE "public"."users";`; got != want {
		t.Fatalf("TRUNCATE = %q, want %q", got, want)
	}

	if got, want := mustDDL(t, Truncate(users), "mysql"), "TRUNCATE TABLE `public`.`users`;"; got != want {
		t.Fatalf("TRUNCATE(mysql) = %q, want %q", got, want)
	}
}

func TestTruncateOptions(t *testing.T) {
	users := Table{Name: NewIdent("users")}
	logs := Table{Name: NewIdent("logs")}

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
	users := Table{Name: NewIdent("users")}
	logs := Table{Name: NewIdent("logs")}

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
	users := Table{Name: NewIdent("users")}
	orders := Table{Name: NewIdent("orders")}

	alter := AlterTable(users).DropColumn("x").RenameColumn("a", "b").Stmt()
	if alter.Table.Name.String() != "users" {
		t.Errorf("AlterTable Table = %q, want users", alter.Table.Name)
	}
	if len(alter.Alterations) != 2 {
		t.Fatalf("Alterations = %d, want 2", len(alter.Alterations))
	}
	if alter.Alterations[0].Kind != AlterDropColumn || alter.Alterations[0].Name.String() != "x" {
		t.Errorf("first alteration = %+v, want DROP COLUMN x", alter.Alterations[0])
	}
	if a := alter.Alterations[1]; a.Kind != AlterRenameColumn || a.Name.String() != "a" || a.NewName.String() != "b" {
		t.Errorf("second alteration = %+v, want RENAME COLUMN a -> b", a)
	}

	dropped := DropTable(users, orders).Stmt().Tables
	if len(dropped) != 2 || dropped[0].Name.String() != "users" || dropped[1].Name.String() != "orders" {
		t.Errorf("DropTable Tables = %v, want [users orders]", dropped)
	}

	created := CreateTable(users).
		Column(ColumnDef{Name: NewIdent("id"), Type: RawType("int")}).
		PrimaryKey("id").Stmt()
	if created.Table.Name.String() != "users" || len(created.Columns) != 1 || len(created.PrimaryKey) != 1 {
		t.Errorf("CreateTable parts = %+v, want one column and a primary key", created)
	}
}

// TestDDLBuilderPlumbing exercises the accessor "plumbing" every DDL builder and
// statement shares — Stmt(), buildStmt(), and setPos() — which the feature tests
// in ddl_test.go don't all reach. These are uniform across the DDL types, so a
// single table keeps them covered.

// ddlBuilder is the shared shape of every DDL fluent builder.
type ddlBuilder interface {
	buildStmt() Query
	setPos(int) Query
}

func TestDDLBuilderPlumbing(t *testing.T) {
	view := Select(Col("id")).From(orgs)

	// Stmt() returns a distinct concrete type per builder, so call each directly.
	_ = CreateSchema("s").Stmt()
	_ = DropSchema("s").Stmt()
	_ = CreateTable(users).Stmt()
	_ = DropTable(users).Stmt()
	_ = CreateView(users, view).Stmt()
	_ = DropView(users).Stmt()
	_ = CreateIndex("i", users, "id").Stmt()
	_ = AlterTable(users).Stmt()

	// buildStmt() and the builder's setPos() are uniform: drive them via the
	// shared interface.
	builders := []ddlBuilder{
		CreateSchema("s"),
		DropSchema("s"),
		CreateTable(users),
		DropTable(users),
		CreateView(users, view),
		DropView(users),
		CreateIndex("i", users, "id"),
		AlterTable(users),
	}
	for _, b := range builders {
		if q := b.buildStmt(); q == nil {
			t.Fatalf("%T.buildStmt() = nil", b)
		}
		if q := b.setPos(5); q == nil {
			t.Fatalf("%T.setPos() = nil", b)
		}
	}

	// Every DDL statement value (including the two that have no builder,
	// DropIndex/DropForeignKey) carries setPos via the Query plumbing.
	stmts := []Query{
		CreateSchema("s").Stmt(),
		DropSchema("s").Stmt(),
		CreateTable(users).Stmt(),
		DropTable(users).Stmt(),
		CreateView(users, view).Stmt(),
		DropView(users).Stmt(),
		CreateIndex("i", users, "id").Stmt(),
		AlterTable(users).Stmt(),
		DropIndex("i", users),
		DropForeignKey(users, "fk"),
	}
	for _, s := range stmts {
		sp, ok := s.(interface{ setPos(int) Query })
		if !ok {
			t.Fatalf("%T has no setPos", s)
		}
		if sp.setPos(7) == nil {
			t.Fatalf("%T.setPos() = nil", s)
		}
	}
}

// TestDDLCompileColumnAndAlterCoverage drives the column-definition and
// alteration compile branches (defaults, references, generated, identity,
// collate, auto-increment, ON UPDATE) across both dialects.
func TestDDLCompileColumnAndAlterCoverage(t *testing.T) {
	pg, my := PostgreSQLDialect{}, MySQLDialect{}
	mustCompile := func(d Dialect, q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(d); err != nil {
			t.Fatalf("%T on %s: %v", q, d.Name(), err)
		}
	}
	tbl := Table{Name: NewIdent("t")}

	// PostgreSQL CREATE TABLE exercising the column-definition branches.
	mustCompile(pg, CreateTable(tbl).IfNotExists().
		Column(ColumnDef{Name: NewIdent("id"), Type: EngineType{Name: "bigint"}, Identity: IdentityByDefault, NotNull: true}).
		Column(ColumnDef{Name: NewIdent("email"), Type: EngineType{Name: "text"}, NotNull: true, Unique: true, Collate: NewIdent("\"C\"")}).
		Column(ColumnDef{Name: NewIdent("author_id"), Type: EngineType{Name: "bigint"},
			References: &ColumnReference{Table: NewIdent("users"), Column: NewIdent("id"), OnDelete: "CASCADE", OnUpdate: "RESTRICT"}}).
		Column(ColumnDef{Name: NewIdent("status"), Type: EngineType{Name: "text"}, Default: Raw("'active'"), Comment: "the status"}).
		Column(ColumnDef{Name: NewIdent("search"), Type: EngineType{Name: "text"}, Generated: &GeneratedColumn{Expression: Raw("lower(email)"), Stored: true}}).
		PrimaryKey("id").
		Check("c_author", Raw("author_id > 0")).
		Unique("u_email", "email").
		ForeignKey(ForeignKeyConstraint{Name: NewIdent("fk"), Columns: []Ident{NewIdent("author_id")}, RefTable: NewIdent("users"), RefColumns: []Ident{NewIdent("id")}, OnDelete: "SET NULL"}).
		Comment("a table").
		Stmt())

	// Temporary table and a MySQL table with AUTO_INCREMENT, ON UPDATE, inline index.
	mustCompile(pg, CreateTable(tbl).Temporary().Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "int"}}).Stmt())
	mustCompile(my, CreateTable(tbl).
		Column(ColumnDef{Name: NewIdent("id"), Type: EngineType{Name: "int"}, AutoIncrement: true, NotNull: true}).
		Column(ColumnDef{Name: NewIdent("updated"), Type: EngineType{Name: "datetime"}, OnUpdate: Raw("CURRENT_TIMESTAMP")}).
		PrimaryKey("id").Index("i_updated", "updated").Stmt())

	// ALTER TABLE exercising every alteration kind.
	mustCompile(pg, AlterTable(tbl).
		AddColumn(ColumnDef{Name: NewIdent("n"), Type: EngineType{Name: "int"}, NotNull: true, Default: Raw("0")}).
		DropColumn("old").
		AddForeignKey(ForeignKeyConstraint{Name: NewIdent("fk2"), Columns: []Ident{NewIdent("n")}, RefTable: NewIdent("users"), RefColumns: []Ident{NewIdent("id")}, OnUpdate: "CASCADE"}).
		AddCheck("c2", Raw("n >= 0")).
		AddUnique("u2", "n").
		DropConstraint("c2").
		AlterColumnType("n", EngineType{Name: "bigint"}).
		SetNotNull("n").
		DropNotNull("n").
		SetDefault("n", Raw("1")).
		DropDefault("n").
		RestateColumn(ColumnDef{Name: NewIdent("n"), Type: EngineType{Name: "bigint"}, NotNull: true, Default: Raw("2")}).
		AddPrimaryKey("n").
		RenameColumn("n", "m").
		Stmt())
	mustCompile(pg, AlterTable(tbl).DropPrimaryKey().Stmt())
	mustCompile(pg, AlterTable(tbl).RenameTo("t2").Stmt())
}

// TestDDLMorePlumbing covers Truncate, CreateIndex IfNotExists, and the
// COMMENT ON table/column statements plus their setPos plumbing.
func TestDDLMorePlumbing(t *testing.T) {
	pg := PostgreSQLDialect{}
	mustOK := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	tbl := Table{Name: NewIdent("t")}
	mustOK(Truncate(tbl))
	mustOK(CreateIndex("i", tbl, "a").IfNotExists())
	mustOK(CommentOnTable(tbl, "hi"))
	mustOK(CommentOnColumn(tbl, "a", "hi"))

	_ = Truncate(tbl).Stmt()
	_ = Truncate(tbl).buildStmt()
	for _, q := range []Query{
		Truncate(tbl), Truncate(tbl).Stmt(),
		CreateIndex("i", tbl, "a"),
		CommentOnTable(tbl, "x"), CommentOnColumn(tbl, "a", "x"),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
}

// TestDDLCompileErrors covers the validation/error branches of the DDL compiler.
func TestDDLCompileErrors(t *testing.T) {
	pg, my := PostgreSQLDialect{}, MySQLDialect{}
	tbl := Table{Name: NewIdent("t")}
	intCol := func(n string) ColumnDef { return ColumnDef{Name: NewIdent(n), Type: EngineType{Name: "int"}} }
	errOn := func(name string, d Dialect, q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(d); err == nil {
			t.Errorf("%s: want error", name)
		}
	}

	errOn("unique no cols", pg, CreateTable(tbl).Column(intCol("a")).Unique("u"))
	errOn("index no cols", my, CreateTable(tbl).Column(intCol("a")).Index("i"))
	errOn("col no type", pg, CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a")}))
	errOn("default+generated", pg, CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"),
		Type: EngineType{Name: "int"}, Default: Raw("0"), Generated: &GeneratedColumn{Expression: Raw("1"), Stored: true}}))
	errOn("onupdate on pg", pg, CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"),
		Type: EngineType{Name: "timestamp"}, OnUpdate: Raw("CURRENT_TIMESTAMP")}))
	errOn("ref empty", pg, CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"),
		Type: EngineType{Name: "int"}, References: &ColumnReference{}}))
	errOn("bad default expr", pg, CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"),
		Type: EngineType{Name: "int"}, Default: UnaryExpression{Operand: Col("x")}}))
	errOn("drop no tables", pg, DropTable())
	errOn("view no query", pg, CreateView(tbl, nil))
	errOn("view binds values", pg, CreateView(tbl, Select(Col("a")).From(orgs).Where(Eq(Col("a"), Val(1)))))
	errOn("truncate no tables", pg, Truncate())
	errOn("truncate multi mysql", my, Truncate(tbl, Table{Name: NewIdent("u")}))
	errOn("truncate cascade mysql", my, Truncate(tbl).Cascade())
	errOn("alter empty", pg, AlterTable(tbl))
	errOn("add unique no cols", pg, AlterTable(tbl).AddUnique("u"))
	errOn("column modify on mysql", my, AlterTable(tbl).SetNotNull("a"))

	// Raw alterations for the no-definition / unsupported-kind guards.
	errOn("add column nil def", pg, AlterTableStmt{Table: tbl, Alterations: []Alteration{{Kind: AlterAddColumn}}})
	errOn("restate nil def", pg, AlterTableStmt{Table: tbl, Alterations: []Alteration{{Kind: AlterRestateColumn}}})
	errOn("add fk nil", pg, AlterTableStmt{Table: tbl, Alterations: []Alteration{{Kind: AlterAddForeignKey}}})
	errOn("unsupported kind", pg, AlterTableStmt{Table: tbl, Alterations: []Alteration{{Kind: AlterationKind(9999)}}})
}

// errColumnType is a ColumnType whose RenderType always fails, driving the
// type-render error branches of compileColumnDef, compileRestateColumn, and
// compileAlteration.
type errColumnType struct{}

func (errColumnType) RenderType(Dialect) (string, error) {
	return "", fmt.Errorf("render boom")
}

// TestDDLCompileRemainingBranches covers the remaining ddl_compile.go branches:
// the CREATE TABLE CHECK error, the column type-render / generated / on-update
// errors, every compileRestateColumn arm, the CREATE VIEW nil-query guard, and
// the compileAlteration ADD COLUMN / ADD CHECK / column-modify / type-render
// branches.
func TestDDLCompileRemainingBranches(t *testing.T) {
	pg, my := PostgreSQLDialect{}, MySQLDialect{}
	tbl := Table{Name: NewIdent("t")}
	bad := UnaryExpression{Operand: Col("x")}
	errOn := func(name string, d Dialect, q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(d); err == nil {
			t.Errorf("%s: want error", name)
		}
	}

	// CREATE TABLE: a CHECK with a bad expression, a column whose type fails to
	// render, a bad GENERATED expression, and a bad ON UPDATE (MySQL).
	errOn("check bad expr", pg, CreateTable(tbl).
		Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "int"}}).Check("c", bad))
	errOn("col render error", pg, CreateTable(tbl).
		Column(ColumnDef{Name: NewIdent("a"), Type: errColumnType{}}))
	errOn("generated bad expr", pg, CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"),
		Type: EngineType{Name: "int"}, Generated: &GeneratedColumn{Expression: bad, Stored: true}}))
	errOn("onupdate bad expr", my, CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"),
		Type: EngineType{Name: "timestamp"}, OnUpdate: bad}))

	// compileRestateColumn: nil type, a MySQL MODIFY whose column fails, a
	// PostgreSQL type-render error, and a bad DEFAULT.
	errOn("restate nil type", pg, AlterTable(tbl).RestateColumn(ColumnDef{Name: NewIdent("a")}))
	errOn("restate mysql bad", my, AlterTable(tbl).RestateColumn(ColumnDef{Name: NewIdent("a"), Type: errColumnType{}}))
	errOn("restate render error", pg, AlterTable(tbl).RestateColumn(ColumnDef{Name: NewIdent("a"), Type: errColumnType{}}))
	errOn("restate bad default", pg, AlterTable(tbl).RestateColumn(ColumnDef{
		Name: NewIdent("a"), Type: EngineType{Name: "int"}, Default: bad}))

	// A restate that keeps NOT NULL and no default drives the DROP DEFAULT arm.
	if _, _, err := AlterTable(tbl).RestateColumn(ColumnDef{
		Name: NewIdent("a"), Type: EngineType{Name: "int"}, NotNull: true}).ToSQL(pg); err != nil {
		t.Errorf("restate drop default: %v", err)
	}

	// CREATE VIEW without a query.
	errOn("view nil query", pg, CreateView(tbl, nil))

	// compileAlteration: ADD COLUMN render error, ADD CHECK bad expr, ALTER
	// COLUMN TYPE on MySQL (unsupported) and with a render error, and SET
	// DEFAULT / SET NOT NULL on MySQL (unsupported).
	errOn("add column render error", pg, AlterTable(tbl).AddColumn(ColumnDef{Name: NewIdent("a"), Type: errColumnType{}}))
	errOn("add check bad expr", pg, AlterTable(tbl).AddCheck("c", bad))
	errOn("alter type on mysql", my, AlterTable(tbl).AlterColumnType("a", EngineType{Name: "int"}))
	errOn("alter type render error", pg, AlterTable(tbl).AlterColumnType("a", errColumnType{}))
	errOn("set default on mysql", my, AlterTable(tbl).SetDefault("a", Raw("0")))
	errOn("set default bad expr", pg, AlterTable(tbl).SetDefault("a", bad))
	errOn("set not null on mysql", my, AlterTable(tbl).SetNotNull("a"))

	// CREATE VIEW whose body fails to compile.
	errOn("view bad body", pg, CreateView(tbl, Select(bad).From(orgs)))
}
