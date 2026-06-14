package sql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func mustDialect(t *testing.T, name string) sql.Dialect {
	t.Helper()
	d, err := sql.DialectByName(name)
	if err != nil {
		t.Fatalf("DialectByName(%q) error = %v", name, err)
	}
	return d
}

func mustDDL(t *testing.T, s sql.Statement, dialect string) string {
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
	if got, want := mustDDL(t, sql.CreateSchema("auth"), "postgres"), `CREATE SCHEMA "auth";`; got != want {
		t.Errorf("CreateSchema(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.CreateSchema("auth").IfNotExists(), "postgres"), `CREATE SCHEMA IF NOT EXISTS "auth";`; got != want {
		t.Errorf("CreateSchema().IfNotExists(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.CreateSchema("auth").IfNotExists(), "mysql"), "CREATE SCHEMA IF NOT EXISTS `auth`;"; got != want {
		t.Errorf("CreateSchema().IfNotExists(mysql) = %q, want %q", got, want)
	}
}

func TestDropSchema(t *testing.T) {
	if got, want := mustDDL(t, sql.DropSchema("auth").IfExists(), "postgres"), `DROP SCHEMA IF EXISTS "auth";`; got != want {
		t.Errorf("DropSchema().IfExists(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.DropSchema("auth").IfExists().Cascade(), "postgres"), `DROP SCHEMA IF EXISTS "auth" CASCADE;`; got != want {
		t.Errorf("DropSchema().Cascade(postgres) = %q, want %q", got, want)
	}
	// MySQL has no CASCADE clause; the flag is ignored.
	if got, want := mustDDL(t, sql.DropSchema("auth").IfExists().Cascade(), "mysql"), "DROP SCHEMA IF EXISTS `auth`;"; got != want {
		t.Errorf("DropSchema().Cascade(mysql) = %q, want %q", got, want)
	}
}

func TestSchemaStmtRequiresName(t *testing.T) {
	if _, _, err := sql.CreateSchema("").ToSQL(mustDialect(t, "postgres")); err == nil {
		t.Error("CreateSchema(\"\"): expected error")
	}
	if _, _, err := sql.DropSchema("").ToSQL(mustDialect(t, "postgres")); err == nil {
		t.Error("DropSchema(\"\"): expected error")
	}
}

func TestCreateTablePostgres(t *testing.T) {
	stmt := sql.CreateTable(sql.Table{Schema: "public", Name: "users"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.EngineType{Name: "int8"}, NotNull: true, AutoIncrement: true}).
		Column(sql.ColumnDef{Name: "email", Type: sql.EngineType{Name: "varchar", Length: 255}, NotNull: true, Comment: "Unique login address"}).
		Column(sql.ColumnDef{Name: "name", Type: sql.EngineType{Name: "text"}}).
		Column(sql.ColumnDef{Name: "balance", Type: sql.EngineType{Name: "numeric", Precision: 10, Scale: 2}, NotNull: true}).
		Column(sql.ColumnDef{Name: "created_at", Type: sql.EngineType{Name: "timestamptz"}, NotNull: true, Default: sql.Raw("now()")}).
		PrimaryKey("id").
		Check("ck_users_1", sql.Raw("balance >= 0")).
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
	stmt := sql.CreateTable(sql.Table{Schema: "public", Name: "users"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.EngineType{Name: "bigint"}, NotNull: true, AutoIncrement: true}).
		Column(sql.ColumnDef{Name: "email", Type: sql.EngineType{Name: "varchar", Length: 255}, NotNull: true, Comment: "Unique login address"}).
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
	stmt := sql.CreateTable(sql.Table{Name: "items"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.RawType("bigserial")}).
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
	stmt := sql.CreateTable(sql.Table{Name: "v1.users"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.RawType("bigint")})
	got := mustDDL(t, stmt, "postgres")
	if want := `CREATE TABLE "v1.users" (`; got[:len(want)] != want {
		t.Errorf("dotted name should be one quoted identifier:\n%s", got)
	}
}

func TestDropTable(t *testing.T) {
	got := mustDDL(t, sql.DropTable(sql.Table{Schema: "public", Name: "users"}).IfExists(), "postgres")
	if want := `DROP TABLE IF EXISTS "public"."users";`; got != want {
		t.Errorf("DropTable() = %q, want %q", got, want)
	}
}

func TestCreateIndex(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	got := mustDDL(t, sql.CreateIndex("uq_users_email", users, "email").Unique(), "postgres")
	if want := `CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");`; got != want {
		t.Errorf("CreateIndex() = %q, want %q", got, want)
	}
	got = mustDDL(t, sql.CreateIndex("idx_users_name", users, "name"), "postgres")
	if want := `CREATE INDEX "idx_users_name" ON "public"."users" ("name");`; got != want {
		t.Errorf("CreateIndex() = %q, want %q", got, want)
	}
}

func TestDropIndex(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	if got, want := mustDDL(t, sql.DropIndex("uq_users_email", users), "postgres"),
		`DROP INDEX IF EXISTS "public"."uq_users_email";`; got != want {
		t.Errorf("DropIndex(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.DropIndex("uq_users_email", users), "mysql"),
		"DROP INDEX `uq_users_email` ON `public`.`users`;"; got != want {
		t.Errorf("DropIndex(mysql) = %q, want %q", got, want)
	}
}

func TestAlterTableAddForeignKey(t *testing.T) {
	stmt := sql.AlterTable(sql.Table{Schema: "public", Name: "posts"}).
		AddForeignKey(sql.ForeignKeyConstraint{
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
	posts := sql.Table{Schema: "public", Name: "posts"}
	if got, want := mustDDL(t, sql.DropForeignKey(posts, "fk_posts_user_id"), "postgres"),
		`ALTER TABLE "public"."posts" DROP CONSTRAINT IF EXISTS "fk_posts_user_id";`; got != want {
		t.Errorf("DropForeignKey(postgres) = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.DropForeignKey(posts, "fk_posts_user_id"), "mysql"),
		"ALTER TABLE `public`.`posts` DROP FOREIGN KEY `fk_posts_user_id`;"; got != want {
		t.Errorf("DropForeignKey(mysql) = %q, want %q", got, want)
	}
}

func TestAlterTableColumnOps(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	cases := []struct {
		name string
		stmt sql.Statement
		want string
	}{
		{"add column", sql.AlterTable(users).AddColumn(sql.ColumnDef{Name: "age", Type: sql.EngineType{Name: "int4"}, NotNull: true}),
			`ALTER TABLE "public"."users" ADD COLUMN "age" integer NOT NULL;`},
		{"drop column", sql.AlterTable(users).DropColumn("age"),
			`ALTER TABLE "public"."users" DROP COLUMN "age";`},
		{"alter type", sql.AlterTable(users).AlterColumnType("age", sql.EngineType{Name: "int8"}),
			`ALTER TABLE "public"."users" ALTER COLUMN "age" TYPE bigint;`},
		{"set not null", sql.AlterTable(users).SetNotNull("name"),
			`ALTER TABLE "public"."users" ALTER COLUMN "name" SET NOT NULL;`},
		{"drop not null", sql.AlterTable(users).DropNotNull("name"),
			`ALTER TABLE "public"."users" ALTER COLUMN "name" DROP NOT NULL;`},
		{"set default", sql.AlterTable(users).SetDefault("created_at", sql.Raw("now()")),
			`ALTER TABLE "public"."users" ALTER COLUMN "created_at" SET DEFAULT now();`},
		{"drop default", sql.AlterTable(users).DropDefault("created_at"),
			`ALTER TABLE "public"."users" ALTER COLUMN "created_at" DROP DEFAULT;`},
		{"rename column", sql.AlterTable(users).RenameColumn("name", "full_name"),
			`ALTER TABLE "public"."users" RENAME COLUMN "name" TO "full_name";`},
		{"rename table", sql.AlterTable(users).RenameTo("accounts"),
			`ALTER TABLE "public"."users" RENAME TO "accounts";`},
		{"multi action", sql.AlterTable(users).AddColumn(sql.ColumnDef{Name: "a", Type: sql.RawType("int")}).DropColumn("b"),
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
	stmt := sql.AlterTable(sql.Table{Name: "users"}).SetNotNull("name")
	if _, _, err := stmt.ToSQL(mustDialect(t, "mysql")); err == nil {
		t.Error("expected error for column modify on mysql")
	}
}

func TestCommentStatements(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	if got, want := mustDDL(t, sql.CommentOnTable(users, "Registered authors"), "postgres"),
		`COMMENT ON TABLE "public"."users" IS 'Registered authors';`; got != want {
		t.Errorf("CommentOnTable = %q, want %q", got, want)
	}
	if got, want := mustDDL(t, sql.CommentOnColumn(users, "email", "Login address"), "postgres"),
		`COMMENT ON COLUMN "public"."users"."email" IS 'Login address';`; got != want {
		t.Errorf("CommentOnColumn = %q, want %q", got, want)
	}
	// MySQL comments inline, so the standalone form renders empty.
	if got := mustDDL(t, sql.CommentOnTable(users, "x"), "mysql"); got != "" {
		t.Errorf("CommentOnTable(mysql) = %q, want empty", got)
	}
}

func TestStatementsRender(t *testing.T) {
	users := sql.Table{Schema: "public", Name: "users"}
	stmts := sql.Statements{
		sql.CreateTable(users).Column(sql.ColumnDef{Name: "id", Type: sql.RawType("bigserial")}).PrimaryKey("id"),
		sql.CreateIndex("idx_users_id", users, "id"),
		sql.CommentOnTable(users, "x"), // empty on mysql, skipped
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
	stmt := sql.AlterTable(sql.Table{Schema: "public", Name: "posts"}).
		AddForeignKey(sql.ForeignKeyConstraint{
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
	col := sql.ColumnDef{Name: "name", Type: sql.EngineType{Name: "text"}, Default: sql.Raw("''")}
	stmt := sql.AlterTable(sql.Table{Name: "users"}).RestateColumn(col)

	if got, want := mustDDL(t, stmt, "mysql"), "ALTER TABLE `users` MODIFY COLUMN `name` text DEFAULT '';"; got != want {
		t.Errorf("RestateColumn(mysql) =\n%s\nwant\n%s", got, want)
	}
	got := mustDDL(t, stmt, "postgres")
	want := `ALTER TABLE "users" ALTER COLUMN "name" TYPE text, ALTER COLUMN "name" DROP NOT NULL, ALTER COLUMN "name" SET DEFAULT '';`
	if got != want {
		t.Errorf("RestateColumn(postgres) =\n%s\nwant\n%s", got, want)
	}
}

func TestPrimaryKeyAlterations(t *testing.T) {
	add := sql.AlterTable(sql.Table{Name: "t"}).AddPrimaryKey("a", "b")
	if got, want := mustDDL(t, add, "postgres"), `ALTER TABLE "t" ADD PRIMARY KEY ("a", "b");`; got != want {
		t.Errorf("AddPrimaryKey =\n%s\nwant\n%s", got, want)
	}
	drop := sql.AlterTable(sql.Table{Name: "t"}).DropPrimaryKey()
	if got, want := mustDDL(t, drop, "mysql"), "ALTER TABLE `t` DROP PRIMARY KEY;"; got != want {
		t.Errorf("DropPrimaryKey(mysql) =\n%s\nwant\n%s", got, want)
	}
	if got, want := mustDDL(t, drop, "postgres"), `ALTER TABLE "t" DROP CONSTRAINT "t_pkey";`; got != want {
		t.Errorf("DropPrimaryKey(postgres) =\n%s\nwant\n%s", got, want)
	}
}
