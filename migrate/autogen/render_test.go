package autogen

import (
	"errors"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// assertValidGo wraps a rendered body in a migration scaffold and gofmts it, so
// a malformed op.* call is caught as a syntax error.
func assertValidGo(t *testing.T, body string) {
	t.Helper()
	src := fmt.Sprintf(`package migrations

import (
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/decl"
)

var _ = func(op *migrate.Operator) {
%s
}

var (
	_ = sql.Raw
	_ = decl.BigInt
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
func build(t *testing.T, c *decl.Catalog) meta.Metadata {
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
	source := func() *decl.Catalog {
		c := decl.NewCatalog(decl.DefaultSchema("public"))
		c.Table("users",
			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		)
		return c
	}
	target := func() *decl.Catalog {
		c := decl.NewCatalog(decl.DefaultSchema("public"))
		c.Table("users",
			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		)
		c.Table("posts",
			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
			decl.Column("user_id", decl.BigInt, decl.References("users", "id", decl.OnDelete(decl.Cascade))),
		)
		return c
	}

	res := render(t, build(t, source()), build(t, target()), "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	wantUp := []string{
		`op.CreateTable("posts",`,
		`decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default())`,
		`op.CreateForeignKey("fk_posts_user_id", "posts", []string{"user_id"}, "users", []string{"id"}, migrate.OnDelete(decl.Cascade))`,
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
	// UsesSQL is intentionally not asserted here: id has a bare Default() (no
	// sql.Raw) and the FK carries no sql.Raw either, so the sql import may or may
	// not be needed depending on rendering details.
}

func TestRenderAddDropColumn(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("old", decl.Text, decl.Nullable()),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.VarChar(255)),
	)

	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.AddColumn("users", decl.Column("email", decl.VarChar(255)))`) {
		t.Errorf("Up missing AddColumn:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `op.DropColumn("users", "old")`) {
		t.Errorf("Up missing DropColumn:\n%s", res.Up)
	}
	// Down reverses: adds back "old", drops "email".
	if !strings.Contains(res.Down, `op.AddColumn("users", decl.Column("old", decl.Text, decl.Nullable()))`) {
		t.Errorf("Down missing AddColumn(old):\n%s", res.Down)
	}
	if !strings.Contains(res.Down, `op.DropColumn("users", "email")`) {
		t.Errorf("Down missing DropColumn(email):\n%s", res.Down)
	}
}

func TestRenderModifyColumn(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.VarChar(100)),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text, decl.Nullable()),
	)

	for _, dialect := range []string{"postgres", "mysql"} {
		res := render(t, build(t, source), build(t, target), dialect, Renames{})
		want := `op.ModifyColumn("users", decl.Column("name", decl.Text, decl.Nullable()))`
		if !strings.Contains(res.Up, want) {
			t.Errorf("[%s] Up missing %q\n%s", dialect, want, res.Up)
		}
	}
}

func TestRenderColumnRename(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("full_name", decl.Text),
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

// TestRenderMultipleRenamesDeterministicOrder guards against the post-rename
// column-change statements following Go's randomized map iteration: two columns
// renamed AND retyped must emit their ModifyColumn statements in rename-
// declaration order, the same on every run.
func TestRenderMultipleRenamesDeterministicOrder(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("a", decl.Text),
		decl.Column("b", decl.Text),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("a2", decl.VarChar(50)),
		decl.Column("b2", decl.BigInt),
	)
	renames := Renames{Columns: []ColumnRename{
		{Table: "users", From: "a", To: "a2"},
		{Table: "users", From: "b", To: "b2"},
	}}
	res := render(t, build(t, source), build(t, target), "postgres", renames)
	ia := strings.Index(res.Up, `op.ModifyColumn("users", decl.Column("a2"`)
	ib := strings.Index(res.Up, `op.ModifyColumn("users", decl.Column("b2"`)
	if ia < 0 || ib < 0 {
		t.Fatalf("Up missing a ModifyColumn for a renamed+retyped column:\n%s", res.Up)
	}
	if ia > ib {
		t.Errorf("renamed-column changes out of declaration order (a2 after b2):\n%s", res.Up)
	}
}

func TestRenderRenameHeuristic(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("full_name", decl.Text),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, "possible rename: column users.name -> full_name?") {
		t.Errorf("Up missing rename hint comment:\n%s", res.Up)
	}
}

func TestRenderFKActionChange(t *testing.T) {
	mk := func(action meta.ReferentialAction) meta.Metadata {
		c := decl.NewCatalog(decl.DefaultSchema("public"))
		c.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
		c.Table("posts",
			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
			decl.Column("user_id", decl.BigInt, decl.References("users", "id", decl.OnDelete(action))),
		)
		return build(t, c)
	}
	res := render(t, mk(meta.Restrict), mk(meta.Cascade), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.DropForeignKey("fk_posts_user_id", "posts")`) {
		t.Errorf("Up missing DropForeignKey for changed action:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `migrate.OnDelete(decl.Cascade)`) {
		t.Errorf("Up missing re-created FK with new action:\n%s", res.Up)
	}
}

func TestRenderNoChange(t *testing.T) {
	mk := func() meta.Metadata {
		c := decl.NewCatalog(decl.DefaultSchema("public"))
		c.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
		return build(t, c)
	}
	res := render(t, mk(), mk(), "postgres", Renames{})
	if !res.Empty() {
		t.Errorf("expected empty result, got Up=%q Down=%q", res.Up, res.Down)
	}
}

// TestRenderGrantsDistinctGranteesOnSameObject mirrors
// migrate.TestDiffGrantsDistinctGranteesOnSameObject for the autogen renderer's
// own copy of grantKey: two distinct grants on the same object (different
// grantees, different privileges) must not collide in the diff map, or adding
// one silently drops the op.Revoke for the other with no matching op.Grant.
func TestRenderGrantsDistinctGranteesOnSameObject(t *testing.T) {
	aliceSelect := meta.Grant{
		Privileges: []string{"SELECT"}, ObjectType: "TABLE",
		Objects: []string{"public.posts"}, Grantees: []string{"alice"},
	}
	source := meta.Metadata{Grants: []meta.Grant{aliceSelect}}
	target := meta.Metadata{Grants: []meta.Grant{
		aliceSelect, // unchanged
		{
			Privileges: []string{"INSERT"}, ObjectType: "TABLE",
			Objects: []string{"public.posts"}, Grantees: []string{"bob"},
		},
	}}
	res := render(t, source, target, "postgres", Renames{})
	assertValidGo(t, res.Up)

	if strings.Contains(res.Up, "op.Revoke") {
		t.Fatalf("alice's unchanged grant must not be revoked:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `sql.Grant("INSERT").OnType("TABLE", "public.posts").To("bob")`) {
		t.Fatalf("missing bob's new grant:\n%s", res.Up)
	}
}

func TestRenderIndexesAsVariadic(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.Text),
		decl.UniqueIndex("email"),
	)
	// New table with an inline index, to exercise decl.Index rendering too.
	target.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("author", decl.BigInt),
		decl.Index("author"),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	if !strings.Contains(res.Up, `op.CreateUniqueIndex("uq_users_email", "users", "email")`) {
		t.Errorf("index columns must be variadic args, not a slice:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `decl.Index("author")`) {
		t.Errorf("created-table index must be variadic args:\n%s", res.Up)
	}
	if strings.Contains(res.Up, `[]string{"email"}`) || strings.Contains(res.Up, `[]string{"author"}`) {
		t.Errorf("index columns must not render as []string literals:\n%s", res.Up)
	}
}

func TestRenderPlainForeignKeyNoDeclImport(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
	source.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
	target.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt, decl.References("users", "id")), // no action
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.CreateForeignKey(`) {
		t.Fatalf("expected CreateForeignKey:\n%s", res.Up)
	}
	if res.UsesDecl {
		t.Errorf("a plain foreign key must not flag the decl import (unused import); Up=%s", res.Up)
	}
}

// erroringQuery is a sql.Query whose ToSQL always fails, used to exercise the
// materialized-view create path when the view's query cannot compile for the
// target dialect.
type erroringQuery struct{}

func (erroringQuery) ToSQL(sql.Dialect) (string, []any, error) {
	return "", nil, errors.New("erroringQuery: cannot compile")
}

// TestRenderMaterializedViewCompileErrorPropagates covers matviewCreate: a
// materialized view whose query fails to compile for the target dialect must
// fail Render with a descriptive error, not silently emit
// op.CreateMaterializedView with an empty sql.RawSQL("") body that only fails
// later, at apply time, against the real engine.
func TestRenderMaterializedViewCompileErrorPropagates(t *testing.T) {
	target := meta.Metadata{MaterializedViews: []meta.MaterializedView{
		{Schema: "public", Name: "totals", Query: erroringQuery{}},
	}}
	_, err := Render(meta.Metadata{}, target, Options{Dialect: mustDialect(t, "postgres")})
	if err == nil {
		t.Fatal("expected Render to error on an uncompilable materialized view query, got nil")
	}
	if !strings.Contains(err.Error(), "totals") {
		t.Fatalf("error should name the offending view: %v", err)
	}
}

func TestRenderAddedColumnComment(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.Text, decl.Comment("login")),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.Execute(sql.CommentOnColumn(sql.Table{Name: "users"}, "email", "login"))`) {
		t.Errorf("added-column comment must be emitted as a standalone statement:\n%s", res.Up)
	}
}

func TestRenderRenameTableBeforeDrops(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("clients",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("note", decl.Text),
		decl.Index("note"),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("customers",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
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
	mk := func(auditCols ...decl.TableElement) meta.Metadata {
		c := decl.NewCatalog()
		pub := c.Schema("public")
		aud := c.Schema("audit")
		pub.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
		aud.Table("users", append([]decl.TableElement{
			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		}, auditCols...)...)
		return build(t, c)
	}
	// audit.users gains a column; public.users is unchanged. The same-named
	// public table must not mask the audit one.
	res := render(t, mk(), mk(decl.Column("at", decl.Timestamp)), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.AddColumn("users", decl.Column("at", decl.Timestamp))`) {
		t.Errorf("multi-schema change on audit.users was lost:\n%s", res.Up)
	}
}
