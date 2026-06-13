package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/inspector"
)

// TestInspectLiveSchema verifies the inspector reads the schema the ddl
// generator just applied, on both engines, and that codegen can turn the
// introspected model back into compiling bindings.
func TestInspectLiveSchema(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		model, err := inspector.Inspect(ctx, db.SQL(), eng.name, testSchema)
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
