package codegen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

func TestGenerateDesign(t *testing.T) {
	file, err := GenerateDesign(meta.Meta{Tables: fixtureTables()}, DesignOptions{PackageName: "design"})
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
				{Name: "id", Type: meta.Of("mysql", "bigint", 0, 0, 0), PrimaryKey: true, HasDefault: true},
				{Name: "name", Type: meta.Of("mysql", "varchar", 100, 0, 0)},
				{Name: "price", Type: meta.Of("mysql", "decimal", 0, 10, 2)},
				{Name: "attrs", Type: meta.Of("mysql", "json", 0, 0, 0), Nullable: true},
				{Name: "created_at", Type: meta.Of("mysql", "datetime", 0, 0, 0), HasDefault: true, DefaultExpr: "now()"},
				{Name: "point", Type: meta.Of("mysql", "geometry", 0, 0, 0)},
			},
		},
	}
	file, err := GenerateDesign(meta.Meta{Tables: tables}, DesignOptions{PackageName: "design", Dialect: "mysql"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`var Catalog = sqldef.NewCatalog()`,
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
				{Name: "region", Type: meta.Of("postgres", "text", 0, 0, 0), PrimaryKey: true},
				{Name: "number", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
			},
		},
		{
			Schema: meta.Schema{Name: "public"},
			Name:   "order_lines",
			Columns: []meta.Column{
				{Name: "order_region", Type: meta.Of("postgres", "text", 0, 0, 0)},
				{Name: "order_number", Type: meta.Of("postgres", "int8", 0, 0, 0)},
				{Name: "parent_id", Type: meta.Of("postgres", "int8", 0, 0, 0), Nullable: true},
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
				{Name: "audit_id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
			},
			ForeignKeys: []meta.ForeignKey{
				{Name: "fk_composite", Columns: []string{"order_region", "order_number"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "orders", RefColumns: []string{"region", "number"}},
				{Name: "fk_self", Columns: []string{"parent_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "order_lines", RefColumns: []string{"id"}},
				{Name: "fk_other_schema", Columns: []string{"audit_id"}, RefSchema: meta.Schema{Name: "audit"}, RefTable: "events", RefColumns: []string{"id"}},
			},
		},
	}
	file, err := GenerateDesign(meta.Meta{Tables: tables}, DesignOptions{PackageName: "design"})
	if err != nil {
		t.Fatalf("GenerateDesign() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`var Public = Catalog.Schema("public")`,
		`sqldef.ForeignKey([]string{"order_region", "order_number"}, Orders, []string{"region", "number"}),`,
		`var _ = Catalog.AddForeignKey(OrderLines, []string{"parent_id"}, OrderLines, []string{"id"})`,
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
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true, HasDefault: true},
				{Name: "sku", Type: meta.Of("postgres", "varchar", 64, 0, 0), Comment: "Stock keeping unit"},
				{Name: "vendor", Type: meta.Of("postgres", "text", 0, 0, 0)},
				{Name: "name", Type: meta.Of("postgres", "text", 0, 0, 0)},
				{Name: "price", Type: meta.Of("postgres", "numeric", 0, 10, 2)},
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
	file, err := GenerateDesign(meta.Meta{Tables: tables}, DesignOptions{PackageName: "design"})
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
	if _, err := GenerateDesign(meta.Meta{Tables: fixtureTables()}, DesignOptions{}); err == nil {
		t.Error("GenerateDesign() without package name: expected error")
	}
	if _, err := GenerateDesign(meta.Meta{}, DesignOptions{PackageName: "design"}); err == nil {
		t.Error("GenerateDesign() without tables: expected error")
	}
}
