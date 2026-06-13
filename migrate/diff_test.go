package migrate_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
)

func pg(t *testing.T) sql.Dialect {
	t.Helper()
	d, err := sql.DialectByName("postgres")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func renderAll(t *testing.T, stmts []sql.Statement) []string {
	t.Helper()
	d := pg(t)
	out := make([]string, len(stmts))
	for i, s := range stmts {
		text, _, err := s.ToSQL(d)
		if err != nil {
			t.Fatalf("ToSQL error = %v", err)
		}
		out[i] = text
	}
	return out
}

func tbl(name string, cols ...meta.Column) meta.Table {
	return meta.Table{Schema: meta.Schema{Name: "public"}, Name: name, Columns: cols}
}

func col(name, engine string) meta.Column {
	return meta.Column{Name: name, Type: meta.Of("postgres", engine, 0, 0, 0)}
}

func md(tables ...meta.Table) meta.Metadata { return meta.Metadata{Tables: tables} }

func diff(t *testing.T, old, next meta.Metadata) []string {
	t.Helper()
	stmts, err := migrate.Diff(old, next, pg(t))
	if err != nil {
		t.Fatalf("Diff error = %v", err)
	}
	return renderAll(t, stmts)
}

func TestDiffAddTable(t *testing.T) {
	old := md()
	next := md(tbl("users", col("id", "int8"), col("name", "text")))
	got := diff(t, old, next)
	if len(got) == 0 || !strings.HasPrefix(got[0], `CREATE TABLE "public"."users"`) {
		t.Fatalf("expected CREATE TABLE, got %v", got)
	}
}

func TestDiffDropTableDropsForeignKeysFirst(t *testing.T) {
	users := tbl("users", col("id", "int8"))
	posts := tbl("posts", col("id", "int8"), col("user_id", "int8"))
	posts.ForeignKeys = []meta.ForeignKey{{
		Name: "fk_posts_user_id", Columns: []string{"user_id"},
		RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"},
	}}
	old := md(users, posts)
	next := md(users)
	got := diff(t, old, next)
	want := []string{
		`ALTER TABLE "public"."posts" DROP CONSTRAINT IF EXISTS "fk_posts_user_id";`,
		`DROP TABLE IF EXISTS "public"."posts";`,
	}
	if !equalStrings(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestDiffAddColumn(t *testing.T) {
	old := md(tbl("users", col("id", "int8")))
	next := md(tbl("users", col("id", "int8"), col("age", "int4")))
	got := diff(t, old, next)
	want := []string{`ALTER TABLE "public"."users" ADD COLUMN "age" integer NOT NULL;`}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffDropColumn(t *testing.T) {
	old := md(tbl("users", col("id", "int8"), col("age", "int4")))
	next := md(tbl("users", col("id", "int8")))
	got := diff(t, old, next)
	want := []string{`ALTER TABLE "public"."users" DROP COLUMN "age";`}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffChangeColumnType(t *testing.T) {
	old := md(tbl("users", col("id", "int4")))
	next := md(tbl("users", col("id", "int8")))
	got := diff(t, old, next)
	want := []string{`ALTER TABLE "public"."users" ALTER COLUMN "id" TYPE bigint;`}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffChangeNullability(t *testing.T) {
	notNull := col("name", "text")
	nullable := col("name", "text")
	nullable.Nullable = true
	old := md(tbl("users", notNull))
	next := md(tbl("users", nullable))
	if got, want := diff(t, old, next), []string{`ALTER TABLE "public"."users" ALTER COLUMN "name" DROP NOT NULL;`}; !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := diff(t, next, old), []string{`ALTER TABLE "public"."users" ALTER COLUMN "name" SET NOT NULL;`}; !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffChangeDefault(t *testing.T) {
	plain := col("created_at", "timestamptz")
	withDefault := col("created_at", "timestamptz")
	withDefault.HasDefault = true
	withDefault.DefaultExpr = sql.Raw("now()")
	old := md(tbl("t", plain))
	next := md(tbl("t", withDefault))
	if got, want := diff(t, old, next), []string{`ALTER TABLE "public"."t" ALTER COLUMN "created_at" SET DEFAULT now();`}; !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := diff(t, next, old), []string{`ALTER TABLE "public"."t" ALTER COLUMN "created_at" DROP DEFAULT;`}; !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffIndexAddDrop(t *testing.T) {
	base := tbl("users", col("id", "int8"), col("email", "text"))
	withIndex := base
	withIndex.Indexes = []meta.Index{{Name: "uq_users_email", Columns: []string{"email"}, Unique: true}}
	if got, want := diff(t, md(base), md(withIndex)),
		[]string{`CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");`}; !equalStrings(got, want) {
		t.Errorf("add index: got %v, want %v", got, want)
	}
	if got, want := diff(t, md(withIndex), md(base)),
		[]string{`DROP INDEX IF EXISTS "public"."uq_users_email";`}; !equalStrings(got, want) {
		t.Errorf("drop index: got %v, want %v", got, want)
	}
}

func TestDiffAddForeignKeyOrderedLast(t *testing.T) {
	users := tbl("users", col("id", "int8"))
	postsOld := tbl("posts", col("id", "int8"), col("user_id", "int8"))
	postsNew := postsOld
	postsNew.ForeignKeys = []meta.ForeignKey{{
		Name: "fk_posts_user_id", Columns: []string{"user_id"},
		RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"},
	}}
	got := diff(t, md(users, postsOld), md(users, postsNew))
	want := []string{`ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id");`}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffChangedCheckDropsBeforeAdd(t *testing.T) {
	oldT := tbl("accounts", col("balance", "int8"))
	oldT.Checks = []meta.Check{{Name: "ck_balance", Expression: sql.Raw("balance >= 0")}}
	newT := tbl("accounts", col("balance", "int8"))
	newT.Checks = []meta.Check{{Name: "ck_balance", Expression: sql.Raw("balance > 0")}}

	got := diff(t, md(oldT), md(newT))
	if len(got) != 1 {
		t.Fatalf("expected one ALTER statement, got %v", got)
	}
	drop := strings.Index(got[0], `DROP CONSTRAINT "ck_balance"`)
	add := strings.Index(got[0], `ADD CONSTRAINT "ck_balance"`)
	if drop < 0 || add < 0 {
		t.Fatalf("expected both drop and add of ck_balance, got %q", got[0])
	}
	if drop > add {
		t.Errorf("DROP must come before ADD for a changed check, got %q", got[0])
	}
}

func TestDiffNoChange(t *testing.T) {
	schema := md(tbl("users", col("id", "int8"), col("name", "text")))
	if got := diff(t, schema, schema); len(got) != 0 {
		t.Errorf("expected no statements, got %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDiffColumnChangeMySQL(t *testing.T) {
	d, err := sql.DialectByName("mysql")
	if err != nil {
		t.Fatal(err)
	}
	old := meta.Metadata{Tables: []meta.Table{{Schema: meta.Schema{Name: "app"}, Name: "users", Columns: []meta.Column{
		{Name: "name", Type: meta.Of("mysql", "varchar", 100, 0, 0)},
	}}}}
	next := meta.Metadata{Tables: []meta.Table{{Schema: meta.Schema{Name: "app"}, Name: "users", Columns: []meta.Column{
		{Name: "name", Type: meta.Of("mysql", "text", 0, 0, 0), Nullable: true},
	}}}}
	stmts, err := migrate.Diff(old, next, d)
	if err != nil {
		t.Fatalf("Diff error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("want 1 statement, got %d", len(stmts))
	}
	text, _, _ := stmts[0].ToSQL(d)
	want := "ALTER TABLE `app`.`users` MODIFY COLUMN `name` text;"
	if text != want {
		t.Errorf("MySQL column change =\n%s\nwant\n%s", text, want)
	}
}

func TestDiffForeignKeyActionChange(t *testing.T) {
	mk := func(action meta.ReferentialAction) meta.Metadata {
		return md(meta.Table{Schema: meta.Schema{Name: "public"}, Name: "posts",
			Columns:     []meta.Column{col("user_id", "int8")},
			ForeignKeys: []meta.ForeignKey{{Name: "fk", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: action}},
		})
	}
	got := diff(t, mk(meta.Restrict), mk(meta.Cascade))
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `DROP CONSTRAINT IF EXISTS "fk"`) {
		t.Errorf("expected FK drop for action change, got:\n%s", joined)
	}
	if !strings.Contains(joined, "ON DELETE CASCADE") {
		t.Errorf("expected FK re-added with new action, got:\n%s", joined)
	}
}

func TestDiffPrimaryKeyChange(t *testing.T) {
	old := md(meta.Table{Schema: meta.Schema{Name: "public"}, Name: "t", Columns: []meta.Column{
		{Name: "a", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
		{Name: "b", Type: meta.Of("postgres", "int8", 0, 0, 0)},
	}})
	next := md(meta.Table{Schema: meta.Schema{Name: "public"}, Name: "t", Columns: []meta.Column{
		{Name: "a", Type: meta.Of("postgres", "int8", 0, 0, 0)},
		{Name: "b", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
	}})
	joined := strings.Join(diff(t, old, next), "\n")
	if !strings.Contains(joined, `DROP CONSTRAINT "t_pkey"`) || !strings.Contains(joined, `ADD PRIMARY KEY ("b")`) {
		t.Errorf("expected PK drop+add, got:\n%s", joined)
	}
}
