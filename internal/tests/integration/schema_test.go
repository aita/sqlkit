package integration

import (
	"context"
	"reflect"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/introspection"
	"github.com/aita/sqlkit/meta"
)

// TestInspectLiveSchema verifies the introspection reads the schema the ddl
// generator just applied, on both engines, and that codegen can turn the
// introspected model back into compiling bindings.
func TestInspectLiveSchema(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		model, err := introspection.Schema(ctx, db.SQL(), eng.name, testSchema)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}

		tables := map[string]bool{}
		for _, table := range model.Tables {
			tables[table.Name] = true
		}
		if !tables["users"] || !tables["posts"] {
			t.Fatalf("inspected tables = %v, want users and posts", tables)
		}

		var idPK, idDefault, emailNullable, sawCreatedAt bool
		for _, table := range model.Tables {
			if table.Name != "users" {
				continue
			}
			for _, c := range table.Columns {
				switch c.Name {
				case "id":
					idPK = c.PrimaryKey
					idDefault = c.HasDefault // bigserial / auto_increment -> default
				case "email":
					emailNullable = c.Nullable
				case "created_at":
					sawCreatedAt = true
				}
			}
		}
		if !idPK || !idDefault {
			t.Fatalf("users.id metadata: pk=%v default=%v, want both true", idPK, idDefault)
		}
		if emailNullable {
			t.Fatal("users.email should be NOT NULL")
		}
		if !sawCreatedAt {
			t.Fatal("users.created_at missing from inspected schema")
		}

		// The foreign key from posts.user_id to users must be discovered.
		var sawFK bool
		for _, table := range model.Tables {
			if table.Name != "posts" {
				continue
			}
			for _, fk := range table.ForeignKeys {
				if fk.RefTable == "users" {
					sawFK = true
				}
			}
		}
		if !sawFK {
			t.Fatal("posts.user_id foreign key to users not inspected")
		}

		// Generated code for the live schema must compile (gofmt + parse). The
		// introspected model carries types known only on the engine it was read
		// from, so codegen must resolve them on that dialect.
		if _, err := codegen.Generate(model, codegen.Options{PackageName: "blogdb", Dialect: eng.name}); err != nil {
			t.Fatalf("codegen against live %s schema: %v", eng.name, err)
		}
	})
}

// TestInspectCompositeForeignKey is a regression test for the PostgreSQL
// composite (multi-column) foreign-key introspection bug: pairing referencing
// and referenced columns through information_schema.constraint_column_usage
// joined on constraint name only produced an N×N cartesian product, so a
// 2-column FK (pa,pb) -> parent(a,b) came back as Columns=[pa,pa,pb,pb] /
// RefColumns=[a,b,a,b] and re-emitted DDL PostgreSQL rejects ("must not contain
// duplicates"). The fix pairs columns by ordinal via pg_constraint, so the test
// asserts the FK round-trips as Columns=[pa,pb] / RefColumns=[a,b]. The same
// test runs on every engine to guard the MySQL path against regression.
func TestInspectCompositeForeignKey(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		quote := eng.dialect.QuoteIdent

		// Names unique to this test so they never collide with the shared catalog.
		composeParent := quote(testSchema + ".fk_compose_parent")
		composeChild := quote(testSchema + ".fk_compose_child")
		singleParent := quote(testSchema + ".fk_single_parent")
		singleChild := quote(testSchema + ".fk_single_child")

		drop := []string{composeChild, singleChild, composeParent, singleParent}
		dropAll := func() {
			for _, ident := range drop {
				// Best-effort cleanup; order drops children before parents.
				_, _ = db.SQL().ExecContext(ctx, "DROP TABLE IF EXISTS "+ident)
			}
		}
		dropAll()
		t.Cleanup(dropAll)

		stmts := []string{
			"CREATE TABLE " + composeParent + " (a int NOT NULL, b int NOT NULL, PRIMARY KEY (a, b))",
			"CREATE TABLE " + composeChild + " (id int NOT NULL PRIMARY KEY, pa int NOT NULL, pb int NOT NULL, " +
				"CONSTRAINT fk_compose FOREIGN KEY (pa, pb) REFERENCES " + composeParent +
				" (a, b) ON DELETE CASCADE ON UPDATE RESTRICT)",
			"CREATE TABLE " + singleParent + " (id int NOT NULL PRIMARY KEY)",
			"CREATE TABLE " + singleChild + " (id int NOT NULL PRIMARY KEY, sp int, " +
				"CONSTRAINT fk_single FOREIGN KEY (sp) REFERENCES " + singleParent +
				" (id) ON DELETE SET NULL)",
		}
		for _, stmt := range stmts {
			if _, err := db.SQL().ExecContext(ctx, stmt); err != nil {
				t.Fatalf("create schema: %v\n%s", err, stmt)
			}
		}

		model, err := introspection.Schema(ctx, db.SQL(), eng.name, testSchema)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}

		composeFK := foreignKey(t, model, "fk_compose_child")
		if got, want := composeFK.Columns, []string{"pa", "pb"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composite FK Columns = %v, want %v (no cartesian duplicates)", got, want)
		}
		if got, want := composeFK.RefColumns, []string{"a", "b"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("composite FK RefColumns = %v, want %v (paired by ordinal)", got, want)
		}
		if composeFK.RefTable != "fk_compose_parent" {
			t.Fatalf("composite FK RefTable = %q, want fk_compose_parent", composeFK.RefTable)
		}
		if composeFK.OnDelete != meta.Cascade {
			t.Fatalf("composite FK OnDelete = %q, want CASCADE", composeFK.OnDelete)
		}
		if composeFK.OnUpdate != meta.Restrict {
			t.Fatalf("composite FK OnUpdate = %q, want RESTRICT", composeFK.OnUpdate)
		}

		singleFK := foreignKey(t, model, "fk_single_child")
		if got, want := singleFK.Columns, []string{"sp"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("single FK Columns = %v, want %v", got, want)
		}
		if got, want := singleFK.RefColumns, []string{"id"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("single FK RefColumns = %v, want %v", got, want)
		}
		if singleFK.OnDelete != meta.SetNull {
			t.Fatalf("single FK OnDelete = %q, want SET NULL", singleFK.OnDelete)
		}
	})
}

// foreignKey returns the sole foreign key of the named table, failing if the
// table or its foreign key is absent.
func foreignKey(t *testing.T, model meta.Metadata, table string) meta.ForeignKey {
	t.Helper()
	for _, tbl := range model.Tables {
		if tbl.Name != table {
			continue
		}
		if len(tbl.ForeignKeys) != 1 {
			t.Fatalf("table %q has %d foreign keys, want 1", table, len(tbl.ForeignKeys))
		}
		return tbl.ForeignKeys[0]
	}
	t.Fatalf("table %q not found in inspected model", table)
	return meta.ForeignKey{}
}
