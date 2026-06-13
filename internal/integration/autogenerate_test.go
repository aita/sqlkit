package integration

// These tests exercise the autogenerate pipeline end to end against real
// databases: a source schema read from a live database by inspector.Inspect is
// diffed against a target sqldef catalog by autogen.Render, the same wiring the
// `sqlkit migrate autogenerate --from-db` command uses. The autogen unit tests
// (migrate/autogen/render_test.go) fabricate both sides as in-memory catalogs and
// never go through introspection, so the inspector -> Render seam — and the
// dialect-specific readback quirks it carries — is only covered here.
//
// IMPORTANT: a full round-trip (inspect a catalog, then Render the inspected
// schema against that same catalog and expect Empty) is NOT reliable, because
// render.go compares raw type names, default text, and index/FK/check names, and
// the inspector reads several of these back in a normalized form that no longer
// matches the catalog:
//
//   - MySQL: Bool -> tinyint; Default(false) -> "0"; Default(now()) ->
//     "CURRENT_TIMESTAMP"; CHECK `title <> ''` -> "`title` <> _utf8mb4''"; and a
//     TEXT column reports CHARACTER_MAXIMUM_LENGTH 65535 while the portable
//     sqldef.Text type carries length 0, so even a plain Text column diffs.
//   - Postgres: CHECK `title <> ''` -> "title <> ''::text"; now() default
//     normalization; unique/secondary index and FK names become engine-generated
//     and no longer equal the names the catalog's DDL emitted (render matches
//     these by name).
//
// So TestAutogenerateRoundTripIdempotent deliberately uses a reduced "safe"
// catalog containing only round-trip-stable elements (serial/auto_increment PK,
// fixed-length VarChar columns, both NOT NULL and nullable; no Bool, no Text, no
// default expressions, no indexes/unique/FK/checks). Do not "helpfully" expand
// that catalog — it will reintroduce the spurious diffs above and make the test
// flaky. The forward-diff test only asserts the presence of specific op.* calls
// in the rendered body, so it is unaffected by these quirks.

import (
	"context"
	"fmt"
	"go/format"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/inspector"
	"github.com/aita/sqlkit/migrate/autogen"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqldef"
)

// TestAutogenerateForwardDiff applies an initial schema to a live database,
// inspects it as the source, and renders a migration that brings it up to a
// fuller target catalog (a new table, a new column, a new foreign key). It
// asserts the rendered Up/Down carry the expected op.* calls and are valid Go.
//
// It avoids Bool columns, default expressions, checks, and named indexes so the
// assertions hold identically on both engines despite their readback quirks.
func TestAutogenerateForwardDiff(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		// Initial database state: just widgets(id, name).
		oldCatalog := sqldef.NewCatalog(sqldef.DefaultSchema(testSchema))
		oldCatalog.Table("widgets",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("name", sqldef.Text),
		)
		applyCatalog(t, db, eng, oldCatalog)
		t.Cleanup(func() { dropTables(t, db, eng, "gadgets", "widgets") })

		// Desired state: widgets gains a column, and a gadgets table referencing it.
		newCatalog := sqldef.NewCatalog(sqldef.DefaultSchema(testSchema))
		widgets := newCatalog.Table("widgets",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("name", sqldef.Text),
			sqldef.Column("sku", sqldef.VarChar(64), sqldef.Nullable()),
		)
		newCatalog.Table("gadgets",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("widget_id", sqldef.BigInt, sqldef.References(widgets, "id")),
		)

		source, err := inspector.Inspect(ctx, db.SQL(), eng.name, testSchema)
		if err != nil {
			t.Fatalf("inspect source: %v", err)
		}
		target, err := newCatalog.Build()
		if err != nil {
			t.Fatalf("build target catalog: %v", err)
		}

		res, err := autogen.Render(source, target, autogen.Options{Dialect: dialectFor(t, eng)})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if res.Empty() {
			t.Fatal("expected a non-empty diff, got none")
		}

		assertValidGo(t, res.Up)
		assertValidGo(t, res.Down)

		for _, want := range []string{
			`op.CreateTable("gadgets"`,
			`op.AddColumn("widgets", sqldef.Column("sku"`,
		} {
			if !strings.Contains(res.Up, want) {
				t.Errorf("Up missing %q\n--- Up ---\n%s", want, res.Up)
			}
		}
		// The new foreign key must be created and must reference both tables; its
		// name is engine-generated, so match on the operation and the table names
		// rather than a fixed constraint name.
		if !strings.Contains(res.Up, "op.CreateForeignKey(") ||
			!strings.Contains(res.Up, `"gadgets"`) || !strings.Contains(res.Up, `"widgets"`) {
			t.Errorf("Up missing CreateForeignKey gadgets -> widgets\n--- Up ---\n%s", res.Up)
		}

		for _, want := range []string{
			`op.DropTable("gadgets")`,
			`op.DropColumn("widgets", "sku")`,
		} {
			if !strings.Contains(res.Down, want) {
				t.Errorf("Down missing %q\n--- Down ---\n%s", want, res.Down)
			}
		}
	})
}

// TestAutogenerateRoundTripIdempotent applies a catalog to a live database,
// inspects it back, and renders the inspected schema against the very same
// catalog: a faithful introspection plus a correct diff must produce no
// operations. The catalog is restricted to round-trip-stable elements (see the
// package-level note) so the assertion holds on both engines.
func TestAutogenerateRoundTripIdempotent(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		safeCatalog := sqldef.NewCatalog(sqldef.DefaultSchema(testSchema))
		safeCatalog.Table("accounts",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("label", sqldef.VarChar(120)),
			sqldef.Column("note", sqldef.VarChar(255), sqldef.Nullable()),
		)
		applyCatalog(t, db, eng, safeCatalog)
		t.Cleanup(func() { dropTables(t, db, eng, "accounts") })

		source, err := inspector.Inspect(ctx, db.SQL(), eng.name, testSchema)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}
		target, err := safeCatalog.Build()
		if err != nil {
			t.Fatalf("build catalog: %v", err)
		}

		res, err := autogen.Render(source, target, autogen.Options{Dialect: dialectFor(t, eng)})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if !res.Empty() {
			t.Fatalf("inspecting an applied catalog and diffing it against itself should yield no changes, got:\n--- Up ---\n%s\n--- Down ---\n%s", res.Up, res.Down)
		}
	})
}

// dialectFor resolves the SQL dialect autogen.Render needs from the engine name,
// matching how the autogen unit tests obtain a dialect.
func dialectFor(t *testing.T, eng engine) sql.Dialect {
	t.Helper()
	d, err := sql.DialectByName(eng.name)
	if err != nil {
		t.Fatalf("dialect %q: %v", eng.name, err)
	}
	return d
}

// applyCatalog makes the live schema hold exactly the given catalog: it wipes
// every existing table (resetSchema) so the inspected source reflects the
// catalog alone — not the shared testCatalog that eachEngine's setup applied, nor
// tables another test left behind — then executes the rendered CREATE statements.
func applyCatalog(t *testing.T, db *sqlkit.Database, eng engine, cat *sqldef.Catalog) {
	t.Helper()
	ctx := context.Background()
	resetSchema(t, db, eng)
	model, err := cat.Build()
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	createText, err := model.CreateSQL(eng.dialect)
	if err != nil {
		t.Fatalf("generate %s schema: %v", eng.name, err)
	}
	for _, stmt := range splitStatements(createText) {
		if _, err := db.SQL().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("apply schema statement %q: %v", stmt, err)
		}
	}
}

// resetSchema drops every table currently in testSchema, giving a clean slate
// regardless of what earlier tests applied. It discovers the tables with the
// inspector, then drops them on a single connection so the engine's foreign-key
// dependencies do not block the drops: Postgres uses CASCADE, MySQL disables
// foreign-key checks for the duration (a session setting, hence one connection).
func resetSchema(t *testing.T, db *sqlkit.Database, eng engine) {
	t.Helper()
	ctx := context.Background()
	model, err := inspector.Inspect(ctx, db.SQL(), eng.name, testSchema)
	if err != nil {
		t.Fatalf("inspect for reset: %v", err)
	}
	if len(model.Tables) == 0 {
		return
	}
	conn, err := db.SQL().Conn(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Close()
	if eng.name == "mysql" {
		if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
			t.Fatalf("disable foreign key checks: %v", err)
		}
		defer conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1")
	}
	for _, tbl := range model.Tables {
		stmt := "DROP TABLE IF EXISTS " + eng.dialect.QuoteIdent(qualifiedName(tbl))
		if eng.name == "postgres" {
			stmt += " CASCADE"
		}
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("drop %s: %v", tbl.Name, err)
		}
	}
}

// dropTables drops the named tables (in the given order) in the test schema,
// leaving the shared per-engine connection clean for later subtests.
func dropTables(t *testing.T, db *sqlkit.Database, eng engine, names ...string) {
	t.Helper()
	ctx := context.Background()
	for _, name := range names {
		ident := eng.dialect.QuoteIdent(testSchema + "." + name)
		if _, err := db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS "+ident); err != nil {
			t.Fatalf("drop %s: %v", name, err)
		}
	}
}

// assertValidGo wraps a rendered migration body in a scaffold and gofmts it, so
// a malformed op.* call surfaces as a syntax error. It mirrors the helper in
// migrate/autogen/render_test.go, which cannot be imported across packages.
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
