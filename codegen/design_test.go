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
		`var App = Schema("app", func() {`,
		`Column("id", BigInt, PrimaryKey(), Default())`,
		`Column("name", VarChar(100))`,
		`Column("price", Decimal(10, 2))`,
		`Column("attrs", JSON, Nullable())`,
		`Column("created_at", Timestamp)`,
		`Column("point", Type("geometry"))`,
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
		`var Orders = Schema("public", func() {`,
		`ForeignKey([]string{"order_region", "order_number"}, "orders", []string{"region", "number"})`,
		`foreign key fk_other_schema references audit.events in another schema`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated design missing %q\n%s", want, got)
		}
	}
}

func TestGenerateDesignIndexesChecksComments(t *testing.T) {
	model := schema.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []schema.Table{
			{
				Schema:  "public",
				Name:    "products",
				Comment: "Sellable products",
				Columns: []schema.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "sku", DataType: "varchar", Length: 64, Comment: "Stock keeping unit"},
					{Name: "vendor", DataType: "text"},
					{Name: "name", DataType: "text"},
					{Name: "price", DataType: "numeric", Precision: 10, Scale: 2},
				},
				Indexes: []schema.Index{
					{Name: "products_sku_key", Columns: []string{"sku"}, Unique: true},
					{Name: "idx_products_name", Columns: []string{"name"}},
					{Name: "uq_products_vendor_name", Columns: []string{"vendor", "name"}, Unique: true},
				},
				Checks: []schema.Check{
					{Name: "products_price_check", Expression: "price > 0"},
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
		`TableComment("Sellable products")`,
		`Column("sku", VarChar(64), Unique(), Comment("Stock keeping unit"))`,
		`Index("name")`,
		`UniqueIndex("vendor", "name")`,
		`Check("price > 0")`,
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
