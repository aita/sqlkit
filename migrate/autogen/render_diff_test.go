package autogen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// TestRenderCheckConstraint covers add/drop/change of a named CHECK constraint
// on a retained table, plus a check rendered inside CreateTable.
func TestRenderCheckConstraint(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("products",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("price", decl.Int),
		decl.Check(sql.Raw("price > 0")),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("products",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("price", decl.Int),
		decl.Check(sql.Raw("price >= 0")),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	// The expression text changed, so the old check is dropped and the new added.
	if !strings.Contains(res.Up, `op.DropConstraint(`) {
		t.Errorf("Up missing DropConstraint for changed check:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `op.AddCheck(`) || !strings.Contains(res.Up, `price >= 0`) {
		t.Errorf("Up missing AddCheck for changed check:\n%s", res.Up)
	}
}

// TestRenderCheckInCreateTable confirms a newly created table renders its CHECK
// constraints inline via decl.Check and flags the sql import.
func TestRenderCheckInCreateTable(t *testing.T) {
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("products",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("price", decl.Int),
		decl.Check(sql.Raw("price > 0")),
	)
	res := render(t, meta.Metadata{}, build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	if !strings.Contains(res.Up, `decl.Check(sql.Raw("price > 0"))`) {
		t.Errorf("created table missing inline Check:\n%s", res.Up)
	}
	if !res.UsesSQL {
		t.Error("inline check references sql.Raw; expected UsesSQL")
	}
}

// TestRenderPrimaryKeyChange covers DropPrimaryKey/AddPrimaryKey when a table's
// primary-key membership changes between source and target.
func TestRenderPrimaryKeyChange(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("t",
		decl.Column("a", decl.BigInt, decl.PrimaryKey()),
		decl.Column("b", decl.BigInt),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("t",
		decl.Column("a", decl.BigInt, decl.PrimaryKey()),
		decl.Column("b", decl.BigInt, decl.PrimaryKey()),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	if !strings.Contains(res.Up, `op.DropPrimaryKey("t")`) {
		t.Errorf("Up missing DropPrimaryKey:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `op.AddPrimaryKey("t", "a", "b")`) {
		t.Errorf("Up missing AddPrimaryKey with both columns:\n%s", res.Up)
	}
}

// TestRenderTableCommentChange covers a table-comment change emitting a
// CommentOnTable on a retained table.
func TestRenderTableCommentChange(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.TableComment("old"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.TableComment("new"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	if !strings.Contains(res.Up, `op.Execute(sql.CommentOnTable(sql.Table{Schema: "public", Name: "users"}, "new"))`) {
		t.Errorf("Up missing CommentOnTable:\n%s", res.Up)
	}
	if !res.UsesSQL {
		t.Error("CommentOnTable references the sql package; expected UsesSQL")
	}
	// Down restores the old comment.
	if !strings.Contains(res.Down, `"old"`) {
		t.Errorf("Down missing restored old comment:\n%s", res.Down)
	}
}

// TestRenderCreateTableWithComment covers the TableComment branch of createTable
// (a table comment on a newly created table renders decl.TableComment inline).
func TestRenderCreateTableWithComment(t *testing.T) {
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.TableComment("the users"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	res := render(t, meta.Metadata{}, build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	if !strings.Contains(res.Up, `decl.TableComment("the users")`) {
		t.Errorf("created table missing inline TableComment:\n%s", res.Up)
	}
}

// TestRenderColumnDefaultExpr covers a column whose default carries an
// expression (rendered as decl.Default(sql.Raw(...))) and a default-only change
// producing a ModifyColumn.
func TestRenderColumnDefaultExpr(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("t",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("n", decl.Int, decl.Default(sql.Raw("0"))),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("t",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("n", decl.Int, decl.Default(sql.Raw("1"))),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	if !strings.Contains(res.Up, `op.ModifyColumn("t", decl.Column("n"`) {
		t.Errorf("Up missing ModifyColumn for default change:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `decl.Default(sql.Raw("1"))`) {
		t.Errorf("Up missing new default expr:\n%s", res.Up)
	}
}

// TestRenderTableRenameHeuristic covers the lone-drop/lone-add table rename hint
// comment (no rename passed, identical column sets).
func TestRenderTableRenameHeuristic(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("clients",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("customers",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, "possible rename: table") {
		t.Errorf("Up missing table-rename hint comment:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, "--rename-table clients:customers") {
		t.Errorf("rename hint missing the suggested flag:\n%s", res.Up)
	}
}

// TestRenderRenameTableWithColumnRename covers a column rename scoped to a
// table that is itself renamed: the column-rename hint targets the destination
// table name.
func TestRenderRenameTableWithColumnRename(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("clients",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("customers",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("full_name", decl.Text),
	)
	renames := Renames{
		Tables:  []TableRename{{From: "clients", To: "customers"}},
		Columns: []ColumnRename{{Table: "customers", From: "name", To: "full_name"}},
	}
	res := render(t, build(t, source), build(t, target), "postgres", renames)
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	if !strings.Contains(res.Up, `op.RenameTable("clients", "customers")`) {
		t.Errorf("Up missing RenameTable:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `op.RenameColumn("customers", "name", "full_name")`) {
		t.Errorf("Up missing RenameColumn on the renamed table:\n%s", res.Up)
	}
	// Down: the column rename is remapped onto the source table "clients".
	if !strings.Contains(res.Down, `op.RenameColumn("clients", "full_name", "name")`) {
		t.Errorf("Down should remap the column rename onto the source table:\n%s", res.Down)
	}
}

// TestRenderColumnRenameHeuristicHint covers the lone-drop/lone-add column
// rename hint (same type, no rename passed).
func TestRenderColumnRenameHeuristicHint(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("t",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("old", decl.Text),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("t",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("new", decl.Text),
	)
	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, "possible rename: column t.old -> new?") {
		t.Errorf("Up missing column-rename hint:\n%s", res.Up)
	}
}

// TestRenderMissingDialectError covers the nil-dialect guard in Render.
func TestRenderMissingDialectError(t *testing.T) {
	_, err := Render(meta.Metadata{}, meta.Metadata{}, Options{})
	if err == nil {
		t.Fatal("expected an error when no dialect is supplied")
	}
	if !strings.Contains(err.Error(), "dialect") {
		t.Errorf("error should mention the missing dialect, got: %v", err)
	}
}

// TestRenderForeignKeyOnUpdate covers fkCreate's OnUpdate action branch (the
// existing FK tests only exercise OnDelete).
func TestRenderForeignKeyOnUpdate(t *testing.T) {
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
	target.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt,
			decl.References("users", "id", decl.OnUpdate(decl.Cascade), decl.OnDelete(decl.SetNull))),
	)
	res := render(t, meta.Metadata{}, build(t, target), "postgres", Renames{})
	assertValidGo(t, res.Up)
	if !strings.Contains(res.Up, `migrate.OnUpdate(decl.Cascade)`) {
		t.Errorf("Up missing OnUpdate action:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `migrate.OnDelete(decl.SetNull)`) {
		t.Errorf("Up missing OnDelete action:\n%s", res.Up)
	}
	if !res.UsesDecl {
		t.Error("a FK with referential actions references decl.* constants; expected UsesDecl")
	}
}

// TestRenderModifyColumnNoTypeError covers the error path through
// columnPropsChanged when comparing a column that has no type on the dialect.
func TestRenderModifyColumnNoTypeError(t *testing.T) {
	tbl := func(c meta.Column) meta.Metadata {
		return meta.Metadata{Tables: []meta.Table{{
			Schema: meta.Schema{Name: "public"}, Name: "t",
			Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "bigint", 0, 0, 0)}, c},
		}}}
	}
	// Source column has a real type; target column has no type for postgres.
	source := tbl(meta.Column{Name: "x", Type: meta.Of("postgres", "text", 0, 0, 0)})
	target := tbl(meta.Column{Name: "x", Type: meta.Of("mysql", "text", 0, 0, 0)})
	_, err := Render(source, target, Options{Dialect: mustDialect(t, "postgres")})
	if err == nil {
		t.Fatal("expected an error comparing a column with no type on the dialect")
	}
}
