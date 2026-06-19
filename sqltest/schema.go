package sqltest

import (
	"context"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/meta"
)

// SetupSchema gives a test a clean schema and tears it down automatically. It
// calls ApplySchema to drop and (re)create every table in md now, then
// registers a t.Cleanup that drops the tables again when the test finishes, so
// the database is left empty. Call it once at the top of a test:
//
//	md, _ := catalog.Build()
//	sqltest.SetupSchema(t, db, md)
//
// md is the meta.Metadata a schema catalog produces (decl.Catalog.Build).
func SetupSchema(t *testing.T, db *sqlkit.Database, md meta.Metadata) {
	t.Helper()
	ApplySchema(t, db, md)
	t.Cleanup(func() {
		dropTables(t, db, md)
	})
}

// ApplySchema resets md's tables on db: it drops them (children before parents,
// so foreign keys never block a drop and an empty database is a no-op), then
// executes the CREATE SQL the meta model renders for db's dialect. Running it
// before a test gives that test a clean schema. SetupSchema wraps it with an
// automatic teardown.
func ApplySchema(t *testing.T, db *sqlkit.Database, md meta.Metadata) {
	t.Helper()
	dropTables(t, db, md)
	createText, err := md.CreateSQL(db.Dialect())
	if err != nil {
		t.Fatalf("sqltest: render schema: %v", err)
	}
	ctx := context.Background()
	for _, stmt := range splitStatements(createText) {
		if _, err := db.SQL().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("sqltest: apply schema statement %q: %v", stmt, err)
		}
	}
}

// Truncate deletes every row from the given tables, children before parents.
// It is a faster between-tests reset than ApplySchema when the tables already
// exist and only their data needs clearing.
func Truncate(t *testing.T, db *sqlkit.Database, tables ...sqlkit.Table) {
	t.Helper()
	ctx := context.Background()
	for i := len(tables) - 1; i >= 0; i-- {
		ident := db.Dialect().QuoteIdent(tables[i].Info().QualifiedName())
		if _, err := db.SQL().ExecContext(ctx, "DELETE FROM "+ident); err != nil {
			t.Fatalf("sqltest: truncate %s: %v", tables[i].Info().QualifiedName(), err)
		}
	}
}

// dropTables drops model's tables in reverse declaration order with DROP TABLE
// IF EXISTS. The drops are issued directly rather than via Metadata.DropSQL
// because that drops foreign-key constraints with ALTER TABLE first, which
// fails on the not-yet-created tables of a fresh database.
func dropTables(t *testing.T, db *sqlkit.Database, md meta.Metadata) {
	t.Helper()
	ctx := context.Background()
	for i := len(md.Tables) - 1; i >= 0; i-- {
		ident := db.Dialect().QuoteIdent(qualifiedName(md.Tables[i]))
		if _, err := db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS "+ident); err != nil {
			t.Fatalf("sqltest: drop %s: %v", md.Tables[i].Name, err)
		}
	}
}

func qualifiedName(tbl meta.Table) string {
	if tbl.Schema.Name == "" {
		return tbl.Name
	}
	return tbl.Schema.Name + "." + tbl.Name
}

// splitStatements breaks rendered DDL into individual statements. The generated
// DDL never contains a semicolon inside a string literal or identifier, so a
// plain split on ";" is sufficient.
func splitStatements(ddlText string) []string {
	var out []string
	for _, part := range strings.Split(ddlText, ";") {
		if stmt := strings.TrimSpace(part); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
