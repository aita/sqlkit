package codegen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/schema"
)

func TestGenerateDesign(t *testing.T) {
	file, err := GenerateDesign(fixtureSchema(), DesignOptions{PackageName: "design"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	if file.Name != "design.go" {
		t.Errorf("file name = %q, want design.go", file.Name)
	}
	checkGolden(t, "design.go.golden", file.Content)
}

func TestGenerateDesignMySQL(t *testing.T) {
	model := schema.Schema{
		Name:    "app",
		Dialect: "mysql",
		Tables: []schema.Table{
			{
				Schema: "app",
				Name:   "items",
				Columns: []schema.Column{
					{Name: "id", DataType: "bigint", PrimaryKey: true, HasDefault: true},
					{Name: "name", DataType: "varchar", Length: 100},
					{Name: "price", DataType: "decimal", Precision: 10, Scale: 2},
					{Name: "attrs", DataType: "json", Nullable: true},
					{Name: "created_at", DataType: "datetime"},
					{Name: "point", DataType: "geometry"},
				},
			},
		},
	}
	file, err := GenerateDesign(model, DesignOptions{PackageName: "design"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`var App = dsl.Schema("app", func() {`,
		`dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Default())`,
		`dsl.Column("name", dsl.VarChar(100))`,
		`dsl.Column("price", dsl.Decimal(10, 2))`,
		`dsl.Column("attrs", dsl.JSON, dsl.Nullable())`,
		`dsl.Column("created_at", dsl.Timestamp)`,
		`dsl.Column("point", dsl.Type("geometry"))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated design missing %q\n%s", want, got)
		}
	}
}

func TestGenerateDesignForeignKeys(t *testing.T) {
	model := schema.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []schema.Table{
			{
				Schema: "public",
				Name:   "order_lines",
				Columns: []schema.Column{
					{Name: "order_region", DataType: "text"},
					{Name: "order_number", DataType: "int8"},
					{Name: "audit_id", DataType: "int8"},
				},
				ForeignKeys: []schema.ForeignKey{
					{Name: "fk_composite", Columns: []string{"order_region", "order_number"}, RefSchema: "public", RefTable: "orders", RefColumns: []string{"region", "number"}},
					{Name: "fk_other_schema", Columns: []string{"audit_id"}, RefSchema: "audit", RefTable: "events", RefColumns: []string{"id"}},
				},
			},
		},
	}
	file, err := GenerateDesign(model, DesignOptions{PackageName: "design", VarName: "Orders"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`var Orders = dsl.Schema("public", func() {`,
		`dsl.ForeignKey([]string{"order_region", "order_number"}, "orders", []string{"region", "number"})`,
		`foreign key fk_other_schema references audit.events in another schema`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated design missing %q\n%s", want, got)
		}
	}
}

func TestGenerateDesignErrors(t *testing.T) {
	if _, err := GenerateDesign(fixtureSchema(), DesignOptions{}); err == nil {
		t.Error("GenerateDesign() without package name: expected error")
	}
	if _, err := GenerateDesign(schema.Schema{Name: "public"}, DesignOptions{PackageName: "design"}); err == nil {
		t.Error("GenerateDesign() without tables: expected error")
	}
}
