package migrate_test

import (
	"sort"
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

// TestDiffBareDefaultToggleDetected covers columnAlterations' defaultChanged:
// a bare decl.Default() (HasDefault true, DefaultExpr nil — PK auto-increment)
// appearing or disappearing must not be silently dropped from the diff just
// because both sides render an empty default text. On PostgreSQL, ALTER
// COLUMN cannot express the auto-increment direction (no ALTER equivalent of
// bigserial's backing sequence), so Diff must error rather than emit nothing
// or wrongly DROP DEFAULT.
func TestDiffBareDefaultToggleDetected(t *testing.T) {
	plain := col("id", "int8")
	plain.PrimaryKey = true
	bareDefault := plain
	bareDefault.HasDefault = true

	old := md(tbl("t", plain))
	next := md(tbl("t", bareDefault))

	if _, err := migrate.Diff(old, next, pg(t)); err == nil {
		t.Fatal("Diff turning on a bare default succeeded on postgres, want error")
	} else if !strings.Contains(err.Error(), "auto-increment") {
		t.Fatalf("Diff error = %v, want mentioning auto-increment", err)
	}

	// The reverse direction (losing a bare default) has a real ALTER COLUMN
	// DROP DEFAULT equivalent and must not error.
	if got, want := diff(t, next, old), []string{`ALTER TABLE "public"."t" ALTER COLUMN "id" DROP DEFAULT;`}; !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDiffBareDefaultToggleMySQL covers the same bare-default toggle on MySQL,
// where MODIFY COLUMN natively supports AUTO_INCREMENT and the change must
// actually be emitted (not silently dropped as it was before HasDefault was
// folded into defaultChanged).
func TestDiffBareDefaultToggleMySQL(t *testing.T) {
	d, err := sql.DialectByName("mysql")
	if err != nil {
		t.Fatal(err)
	}
	plain := meta.Column{Name: "id", Type: meta.Of("mysql", "bigint", 0, 0, 0), PrimaryKey: true}
	bareDefault := plain
	bareDefault.HasDefault = true
	old := meta.Metadata{Tables: []meta.Table{{Schema: meta.Schema{Name: "app"}, Name: "users", Columns: []meta.Column{plain}}}}
	next := meta.Metadata{Tables: []meta.Table{{Schema: meta.Schema{Name: "app"}, Name: "users", Columns: []meta.Column{bareDefault}}}}

	stmts, err := migrate.Diff(old, next, d)
	if err != nil {
		t.Fatalf("Diff error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("want 1 statement (the toggle must not be silently dropped), got %d", len(stmts))
	}
	text, _, _ := stmts[0].ToSQL(d)
	want := "ALTER TABLE `app`.`users` MODIFY COLUMN `id` bigint NOT NULL AUTO_INCREMENT;"
	if text != want {
		t.Errorf("MySQL bare-default toggle =\n%s\nwant\n%s", text, want)
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

// TestDiffDeterministicDropOrder pins that dropping several indexes and foreign
// keys in one diff emits them in a stable, name-sorted order. Before the fix
// diffTable ranged the by-name index and FK maps directly, so Go's randomized
// map iteration produced a different statement order on different runs.
func TestDiffDeterministicDropOrder(t *testing.T) {
	users := tbl("users", col("id", "int8"))
	ref := meta.Schema{Name: "public"}

	posts := func(withObjects bool) meta.Table {
		p := tbl("posts", col("id", "int8"), col("user_id", "int8"), col("email", "text"))
		if withObjects {
			p.Indexes = []meta.Index{
				{Name: "idx_e", Columns: []string{"id"}},
				{Name: "idx_d", Columns: []string{"id"}},
				{Name: "idx_c", Columns: []string{"id"}},
				{Name: "idx_b", Columns: []string{"id"}},
				{Name: "idx_a", Columns: []string{"email"}},
			}
			p.ForeignKeys = []meta.ForeignKey{
				{Name: "fk_d", Columns: []string{"user_id"}, RefSchema: ref, RefTable: "users", RefColumns: []string{"id"}},
				{Name: "fk_c", Columns: []string{"user_id"}, RefSchema: ref, RefTable: "users", RefColumns: []string{"id"}},
				{Name: "fk_b", Columns: []string{"user_id"}, RefSchema: ref, RefTable: "users", RefColumns: []string{"id"}},
				{Name: "fk_a", Columns: []string{"user_id"}, RefSchema: ref, RefTable: "users", RefColumns: []string{"id"}},
			}
		}
		return p
	}

	old := md(users, posts(true))
	next := md(users, posts(false))

	// The diff must be byte-identical across many runs despite randomized map
	// iteration; a single run can coincidentally come out ordered.
	first := diff(t, old, next)
	for i := 0; i < 200; i++ {
		if got := diff(t, old, next); !equalStrings(got, first) {
			t.Fatalf("non-deterministic diff on run %d:\n first=%v\n got  =%v", i, first, got)
		}
	}

	// Dropped objects of each kind come out in ascending name order.
	for _, kind := range []string{"DROP INDEX", "DROP CONSTRAINT"} {
		var lines []string
		for _, stmt := range first {
			if strings.Contains(stmt, kind) {
				lines = append(lines, stmt)
			}
		}
		if len(lines) < 2 {
			t.Fatalf("%s: expected several statements, got %v", kind, lines)
		}
		if !sort.StringsAreSorted(lines) {
			t.Errorf("%s statements not in sorted order: %v", kind, lines)
		}
	}
}

// A migration that introduces (or removes) a managed schema with objects in it
// must emit CREATE SCHEMA before the objects and DROP SCHEMA after them — without
// it the apply fails ("schema does not exist") or leaves an orphaned schema. The
// diff engine omitted schema statements entirely (the #157 fix landed only in
// meta.CreateSQL/DropSQL).
func TestDiffManagedSchemaCreateAndDrop(t *testing.T) {
	withSchema := meta.Metadata{
		ManagedSchemas: []meta.Schema{{Name: "audit", Create: true}},
		Tables:         []meta.Table{tbl("log", col("id", "int8"))},
	}
	withSchema.Tables[0].Schema = meta.Schema{Name: "audit"}

	indexOf := func(rows []string, prefix string) int {
		for i, r := range rows {
			if strings.HasPrefix(r, prefix) {
				return i
			}
		}
		return -1
	}

	create := diff(t, md(), withSchema)
	cs, ct := indexOf(create, `CREATE SCHEMA IF NOT EXISTS "audit"`), indexOf(create, `CREATE TABLE "audit"."log"`)
	if cs < 0 {
		t.Fatalf("create diff omitted CREATE SCHEMA: %v", create)
	}
	if ct < 0 || cs > ct {
		t.Fatalf("CREATE SCHEMA must precede CREATE TABLE: %v", create)
	}

	drop := diff(t, withSchema, md())
	ds, dt := indexOf(drop, `DROP SCHEMA IF EXISTS "audit"`), indexOf(drop, `DROP TABLE`)
	if ds < 0 {
		t.Fatalf("drop diff omitted DROP SCHEMA: %v", drop)
	}
	if dt < 0 || ds < dt {
		t.Fatalf("DROP SCHEMA must follow DROP TABLE: %v", drop)
	}

	// An unchanged managed schema emits no schema statement.
	if got := diff(t, withSchema, withSchema); len(got) != 0 {
		t.Fatalf("no-op diff emitted statements: %v", got)
	}
}

func TestDiffFKEqualIgnoresSchemaCreateFlag(t *testing.T) {
	c := col("user_id", "int8")
	mkTable := func(create bool) meta.Table {
		tb := tbl("posts", col("id", "int8"), c)
		tb.ForeignKeys = []meta.ForeignKey{{
			Name: "fk_posts_user", Columns: []string{"user_id"},
			RefSchema: meta.Schema{Name: "public", Create: create}, RefTable: "users", RefColumns: []string{"id"},
		}}
		return tb
	}
	users := tbl("users", col("id", "int8"))
	old := md(users, mkTable(false))
	next := md(users, mkTable(true))
	got := diff(t, old, next)
	if len(got) != 0 {
		t.Fatalf("changing only Schema.Create should not alter FK; got %v", got)
	}
}
