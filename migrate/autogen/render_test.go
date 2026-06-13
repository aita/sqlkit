package autogen

import (
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqldef"
)

// assertValidGo wraps a rendered body in a migration scaffold and gofmts it, so
// a malformed op.* call is caught as a syntax error.
func assertValidGo(t *testing.T, body string) {
	t.Helper()
	src := fmt.Sprintf(`package migrations

import (
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqldef"
)

var _ = func(op *migrate.Operator) {
%s
}

var (
	_ = sql.Raw
	_ = sqldef.BigInt
)
`, body)
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("rendered body is not valid Go: %v\n--- src ---\n%s", err, src)
	}
}

func mustDialect(t *testing.T, name string) sql.Dialect {
	t.Helper()
	d, err := sql.DialectByName(name)
	if err != nil {
		t.Fatalf("dialect %q: %v", name, err)
	}
	return d
}

// build assembles a catalog into meta.Metadata, failing the test on error.
func build(t *testing.T, c *sqldef.Catalog) meta.Metadata {
	t.Helper()
	md, err := c.Build()
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	return md
}

func render(t *testing.T, source, target meta.Metadata, dialect string, renames Renames) Result {
	t.Helper()
	res, err := Render(source, target, Options{Dialect: mustDialect(t, dialect), Renames: renames})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return res
}

func TestRenderAddTable(t *testing.T) {
	source := func() *sqldef.Catalog {
		c := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
		c.Table("users",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		)
		return c
	}
	target := func() *sqldef.Catalog {
		c := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
		users := c.Table("users",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		)
		c.Table("posts",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id", sqldef.OnDelete(sqldef.Cascade))),
		)
		return c
	}

	res := render(t, build(t, source()), build(t, target()), "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	wantUp := []string{
		`op.CreateTable("posts",`,
		`sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default())`,
		`op.CreateForeignKey("fk_posts_user_id", "posts", []string{"user_id"}, "users", []string{"id"}, migrate.OnDelete(sqldef.Cascade))`,
	}
	for _, w := range wantUp {
		if !strings.Contains(res.Up, w) {
			t.Errorf("Up missing %q\n--- Up ---\n%s", w, res.Up)
		}
	}
	for _, w := range []string{`op.DropForeignKey("fk_posts_user_id", "posts")`, `op.DropTable("posts")`} {
		if !strings.Contains(res.Down, w) {
			t.Errorf("Down missing %q\n--- Down ---\n%s", w, res.Down)
		}
	}
	if !res.UsesSQL { // Default(sql.Raw) is not used here, but Default() is bare; FK has no sql.Raw
		// id has bare Default(), so sql import may not be needed; don't assert UsesSQL true.
	}
}

func TestRenderAddDropColumn(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("old", sqldef.Text, sqldef.Nullable()),
	)
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	target.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("email", sqldef.VarChar(255)),
	)

	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.AddColumn("users", sqldef.Column("email", sqldef.VarChar(255)))`) {
		t.Errorf("Up missing AddColumn:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `op.DropColumn("users", "old")`) {
		t.Errorf("Up missing DropColumn:\n%s", res.Up)
	}
	// Down reverses: adds back "old", drops "email".
	if !strings.Contains(res.Down, `op.AddColumn("users", sqldef.Column("old", sqldef.Text, sqldef.Nullable()))`) {
		t.Errorf("Down missing AddColumn(old):\n%s", res.Down)
	}
	if !strings.Contains(res.Down, `op.DropColumn("users", "email")`) {
		t.Errorf("Down missing DropColumn(email):\n%s", res.Down)
	}
}

func TestRenderModifyColumn(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("name", sqldef.VarChar(100)),
	)
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	target.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("name", sqldef.Text, sqldef.Nullable()),
	)

	for _, dialect := range []string{"postgres", "mysql"} {
		res := render(t, build(t, source), build(t, target), dialect, Renames{})
		want := `op.ModifyColumn("users", sqldef.Column("name", sqldef.Text, sqldef.Nullable()))`
		if !strings.Contains(res.Up, want) {
			t.Errorf("[%s] Up missing %q\n%s", dialect, want, res.Up)
		}
	}
}

func TestRenderColumnRename(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("name", sqldef.Text),
	)
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	target.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("full_name", sqldef.Text),
	)

	renames := Renames{Columns: []ColumnRename{{Table: "users", From: "name", To: "full_name"}}}
	res := render(t, build(t, source), build(t, target), "postgres", renames)
	if !strings.Contains(res.Up, `op.RenameColumn("users", "name", "full_name")`) {
		t.Errorf("Up missing RenameColumn:\n%s", res.Up)
	}
	if strings.Contains(res.Up, `op.DropColumn`) || strings.Contains(res.Up, `op.AddColumn`) {
		t.Errorf("Up should not drop/add a renamed column:\n%s", res.Up)
	}
	// Down renames back.
	if !strings.Contains(res.Down, `op.RenameColumn("users", "full_name", "name")`) {
		t.Errorf("Down missing reverse RenameColumn:\n%s", res.Down)
	}
}

func TestRenderRenameHeuristic(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("name", sqldef.Text),
	)
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	target.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("full_name", sqldef.Text),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, "possible rename: column users.name -> full_name?") {
		t.Errorf("Up missing rename hint comment:\n%s", res.Up)
	}
}

func TestRenderFKActionChange(t *testing.T) {
	mk := func(action meta.ReferentialAction) meta.Metadata {
		c := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
		users := c.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
		c.Table("posts",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id", sqldef.OnDelete(action))),
		)
		return build(t, c)
	}
	res := render(t, mk(meta.Restrict), mk(meta.Cascade), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.DropForeignKey("fk_posts_user_id", "posts")`) {
		t.Errorf("Up missing DropForeignKey for changed action:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `migrate.OnDelete(sqldef.Cascade)`) {
		t.Errorf("Up missing re-created FK with new action:\n%s", res.Up)
	}
}

func TestRenderNoChange(t *testing.T) {
	mk := func() meta.Metadata {
		c := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
		c.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
		return build(t, c)
	}
	res := render(t, mk(), mk(), "postgres", Renames{})
	if !res.Empty() {
		t.Errorf("expected empty result, got Up=%q Down=%q", res.Up, res.Down)
	}
}

func TestRenderIndexesAsVariadic(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	target.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("email", sqldef.Text),
		sqldef.UniqueIndex("email"),
	)
	// New table with an inline index, to exercise sqldef.Index rendering too.
	target.Table("posts",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("author", sqldef.BigInt),
		sqldef.Index("author"),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	if !strings.Contains(res.Up, `op.CreateUniqueIndex("uq_users_email", "users", "email")`) {
		t.Errorf("index columns must be variadic args, not a slice:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `sqldef.Index("author")`) {
		t.Errorf("created-table index must be variadic args:\n%s", res.Up)
	}
	if strings.Contains(res.Up, `[]string{"email"}`) || strings.Contains(res.Up, `[]string{"author"}`) {
		t.Errorf("index columns must not render as []string literals:\n%s", res.Up)
	}
}

func TestRenderPlainForeignKeyNoSqldefImport(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
	source.Table("posts",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("user_id", sqldef.BigInt),
	)
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	users := target.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
	target.Table("posts",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id")), // no action
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.CreateForeignKey(`) {
		t.Fatalf("expected CreateForeignKey:\n%s", res.Up)
	}
	if res.UsesSqldef {
		t.Errorf("a plain foreign key must not flag the sqldef import (unused import); Up=%s", res.Up)
	}
}

func TestRenderAddedColumnComment(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	target.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("email", sqldef.Text, sqldef.Comment("login")),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.Execute(sql.CommentOnColumn(sql.Table{Name: "users"}, "email", "login"))`) {
		t.Errorf("added-column comment must be emitted as a standalone statement:\n%s", res.Up)
	}
}

func TestRenderRenameTableBeforeDrops(t *testing.T) {
	source := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	source.Table("clients",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("note", sqldef.Text),
		sqldef.Index("note"),
	)
	target := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	target.Table("customers",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
	)
	renames := Renames{Tables: []TableRename{{From: "clients", To: "customers"}}}
	res := render(t, build(t, source), build(t, target), "postgres", renames)
	assertValidGo(t, res.Up)
	rename := strings.Index(res.Up, `op.RenameTable("clients", "customers")`)
	drop := strings.Index(res.Up, `op.DropIndex(`)
	if rename < 0 || drop < 0 {
		t.Fatalf("expected rename and drop in Up:\n%s", res.Up)
	}
	if rename > drop {
		t.Errorf("RenameTable must precede drops on the renamed table:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `op.DropIndex("idx_clients_note", "customers")`) {
		t.Errorf("post-rename drop should target the new table name:\n%s", res.Up)
	}
}

func TestRenderMultiSchemaSameName(t *testing.T) {
	mk := func(auditCols ...sqldef.TableElement) meta.Metadata {
		c := sqldef.NewCatalog()
		pub := c.Schema("public")
		aud := c.Schema("audit")
		pub.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()))
		aud.Table("users", append([]sqldef.TableElement{
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		}, auditCols...)...)
		return build(t, c)
	}
	// audit.users gains a column; public.users is unchanged. The same-named
	// public table must not mask the audit one.
	res := render(t, mk(), mk(sqldef.Column("at", sqldef.Timestamp)), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.AddColumn("users", sqldef.Column("at", sqldef.Timestamp))`) {
		t.Errorf("multi-schema change on audit.users was lost:\n%s", res.Up)
	}
}
