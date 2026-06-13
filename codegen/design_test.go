package codegen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

func TestGenerateDesign(t *testing.T) {
	file, err := GenerateDesign(meta.Catalog{Tables: fixtureTables()}, DesignOptions{PackageName: "design"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	if file.Name != "design.go" {
		t.Errorf("file name = %q, want design.go", file.Name)
	}
	checkGolden(t, "design.go.golden", file.Content)
}

func TestGenerateDesignMySQL(t *testing.T) {
	tables := []meta.Table{
		{
			Schema: meta.Schema{Name: "app"},
			Name:   "items",
			Columns: []meta.Column{
				{Name: "id", DataType: "bigint", PrimaryKey: true, HasDefault: true},
				{Name: "name", DataType: "varchar", Length: 100},
				{Name: "price", DataType: "decimal", Precision: 10, Scale: 2},
				{Name: "attrs", DataType: "json", Nullable: true},
				{Name: "created_at", DataType: "datetime", HasDefault: true, DefaultExpr: "now()"},
				{Name: "point", DataType: "geometry"},
			},
		},
	}
	file, err := GenerateDesign(meta.Catalog{Tables: tables}, DesignOptions{PackageName: "design", Dialect: "mysql"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`var Catalog = sqldef.Catalog()`,
		`var App = Catalog.Schema("app")`,
		"var Items = App.Table(\"items\",",
		`sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),`,
		`sqldef.Column("name", sqldef.VarChar(100)),`,
		`sqldef.Column("price", sqldef.Decimal(10, 2)),`,
		`sqldef.Column("attrs", sqldef.JSON, sqldef.Nullable()),`,
		`sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default("now()")),`,
		`sqldef.Column("point", sqldef.Type("geometry")),`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated design missing %q\n%s", want, got)
		}
	}
}

func TestGenerateDesignForeignKeys(t *testing.T) {
	tables := []meta.Table{
		{
			Schema: meta.Schema{Name: "public"},
			Name:   "orders",
			Columns: []meta.Column{
				{Name: "region", DataType: "text", PrimaryKey: true},
				{Name: "number", DataType: "int8", PrimaryKey: true},
			},
		},
		{
			Schema: meta.Schema{Name: "public"},
			Name:   "order_lines",
			Columns: []meta.Column{
				{Name: "order_region", DataType: "text"},
				{Name: "order_number", DataType: "int8"},
				{Name: "parent_id", DataType: "int8", Nullable: true},
				{Name: "id", DataType: "int8", PrimaryKey: true},
				{Name: "audit_id", DataType: "int8"},
			},
			ForeignKeys: []meta.ForeignKey{
				{Name: "fk_composite", Columns: []string{"order_region", "order_number"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "orders", RefColumns: []string{"region", "number"}},
				{Name: "fk_self", Columns: []string{"parent_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "order_lines", RefColumns: []string{"id"}},
				{Name: "fk_other_schema", Columns: []string{"audit_id"}, RefSchema: meta.Schema{Name: "audit"}, RefTable: "events", RefColumns: []string{"id"}},
			},
		},
	}
	file, err := GenerateDesign(meta.Catalog{Tables: tables}, DesignOptions{PackageName: "design"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`var Public = Catalog.Schema("public")`,
		`sqldef.ForeignKey([]string{"order_region", "order_number"}, Orders, []string{"region", "number"}),`,
		`var _ = OrderLines.ForeignKey([]string{"parent_id"}, OrderLines, []string{"id"})`,
		`foreign key fk_other_schema references audit.events outside this design`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated design missing %q\n%s", want, got)
		}
	}
}

func TestGenerateDesignIndexesChecksComments(t *testing.T) {
	tables := []meta.Table{
		{
			Schema:  meta.Schema{Name: "public"},
			Name:    "products",
			Comment: "Sellable products",
			Columns: []meta.Column{
				{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
				{Name: "sku", DataType: "varchar", Length: 64, Comment: "Stock keeping unit"},
				{Name: "vendor", DataType: "text"},
				{Name: "name", DataType: "text"},
				{Name: "price", DataType: "numeric", Precision: 10, Scale: 2},
			},
			Indexes: []meta.Index{
				{Name: "products_sku_key", Columns: []string{"sku"}, Unique: true},
				{Name: "idx_products_name", Columns: []string{"name"}},
				{Name: "uq_products_vendor_name", Columns: []string{"vendor", "name"}, Unique: true},
			},
			Checks: []meta.Check{
				{Name: "products_price_check", Expression: "price > 0"},
			},
		},
	}
	file, err := GenerateDesign(meta.Catalog{Tables: tables}, DesignOptions{PackageName: "design"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`sqldef.TableComment("Sellable products"),`,
		`sqldef.Column("sku", sqldef.VarChar(64), sqldef.Unique(), sqldef.Comment("Stock keeping unit")),`,
		`sqldef.Index("name"),`,
		`sqldef.UniqueIndex("vendor", "name"),`,
		`sqldef.Check("price > 0"),`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated design missing %q\n%s", want, got)
		}
	}
}

func TestGenerateDesignErrors(t *testing.T) {
	if _, err := GenerateDesign(meta.Catalog{Tables: fixtureTables()}, DesignOptions{}); err == nil {
		t.Error("GenerateDesign() without package name: expected error")
	}
	if _, err := GenerateDesign(meta.Catalog{}, DesignOptions{PackageName: "design"}); err == nil {
		t.Error("GenerateDesign() without tables: expected error")
	}
}
