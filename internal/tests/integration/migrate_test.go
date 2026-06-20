package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit/introspect"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/migrate"
)

// TestMigratorAgainstPostgres applies and rolls back versioned migrations
// against a real PostgreSQL instance, confirming the decl-authored operations
// and the schema_migrations history work end to end.
func TestMigratorAgainstPostgres(t *testing.T) {
	eng := engineNamed(t, "postgres")
	db := connect(t, eng)
	ctx := context.Background()

	// The migrator owns tables outside the shared catalog, so connect (not setup)
	// is used and the migrator's own tables — those the shared migration set
	// creates, plus the history table — are dropped here to isolate the test from
	// a prior run on the shared connection.
	owned := append(append([]string{}, testMigrationTables...), "schema_migrations")
	for _, name := range owned {
		ident := eng.dialect.QuoteIdent(testSchema + "." + name)
		if _, err := db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS "+ident); err != nil {
			t.Fatalf("drop %s: %v", name, err)
		}
	}

	m := migrate.New(db, testMigrations(), migrate.WithSchema(testSchema))

	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	model, err := introspect.Schema(ctx, db.SQL(), eng.name, testSchema)
	if err != nil {
		t.Fatalf("inspect after up: %v", err)
	}
	cols := tableColumns(model, "tags")
	if cols == nil {
		t.Fatal("tags table not created")
	}
	if !cols["name"] || !cols["color"] {
		t.Fatalf("tags columns = %v, want name and color", cols)
	}

	// Up is idempotent: a second run applies nothing and does not error.
	if err := m.Up(ctx); err != nil {
		t.Fatalf("second Up: %v", err)
	}

	// Roll the whole feature back.
	if err := m.DownTo(ctx, ""); err != nil {
		t.Fatalf("DownTo: %v", err)
	}
	model, err = introspect.Schema(ctx, db.SQL(), eng.name, testSchema)
	if err != nil {
		t.Fatalf("inspect after down: %v", err)
	}
	if tableColumns(model, "tags") != nil {
		t.Fatal("tags table should be dropped after rollback")
	}

	status, err := m.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, s := range status {
		if s.Applied {
			t.Fatalf("migration %s still applied after rollback", s.Revision)
		}
	}

	// The history table retains the full timeline: up, up, down, down.
	history, err := m.History(ctx)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("history has %d events, want 4 (2 up + 2 down)", len(history))
	}
}

// engineNamed returns the configured engine with the given name, failing the
// test if no such engine exists.
func engineNamed(t *testing.T, name string) engine {
	t.Helper()
	for _, eng := range engines {
		if eng.name == name {
			return eng
		}
	}
	t.Fatalf("no engine named %q configured", name)
	return engine{}
}

// tableColumns returns the set of column names of the named table, or nil if
// the table is absent.
func tableColumns(model meta.Metadata, name string) map[string]bool {
	for _, table := range model.Tables {
		if table.Name != name {
			continue
		}
		cols := map[string]bool{}
		for _, c := range table.Columns {
			cols[c.Name] = true
		}
		return cols
	}
	return nil
}
