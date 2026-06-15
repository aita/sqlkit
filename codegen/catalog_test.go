package codegen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

func TestGenerateCatalog(t *testing.T) {
	file, err := GenerateCatalog(meta.Metadata{Tables: fixtureTables()}, CatalogOptions{PackageName: "schema"})
	if err != nil {
		t.Fatalf("GenerateCatalog() error = %v", err)
	}
	if file.Name != "schema.go" {
		t.Errorf("file name = %q, want schema.go", file.Name)
	}
	checkGolden(t, "schema.go.golden", file.Content)
}

func TestGenerateCatalogMySQL(t *testing.T) {
	tables := []meta.Table{
		{
			Schema: meta.Schema{Name: "app"},
			Name:   "items",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("mysql", "bigint", 0, 0, 0), PrimaryKey: true, HasDefault: true},
				{Name: "name", Type: meta.Of("mysql", "varchar", 100, 0, 0)},
				{Name: "price", Type: meta.Of("mysql", "decimal", 0, 10, 2)},
				{Name: "attrs", Type: meta.Of("mysql", "json", 0, 0, 0), Nullable: true},
				{Name: "created_at", Type: meta.Of("mysql", "datetime", 0, 0, 0), HasDefault: true, DefaultExpr: sql.Raw("now()")},
				{Name: "point", Type: meta.Of("mysql", "geometry", 0, 0, 0)},
			},
		},
	}
	file, err := GenerateCatalog(meta.Metadata{Tables: tables}, CatalogOptions{PackageName: "schema", Dialect: "mysql"})
	if err != nil {
		t.Fatalf("GenerateCatalog() error = %v", err)
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
		`sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(sql.Raw("now()"))),`,
		`sqldef.Column("point", sqldef.Type("geometry")),`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated catalog missing %q\n%s", want, got)
		}
	}
}

func TestGenerateCatalogArray(t *testing.T) {
	tables := []meta.Table{{
		Schema: meta.Schema{Name: "public"},
		Name:   "docs",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true, HasDefault: true},
			{Name: "tags", Type: meta.OfArray("postgres", "text", 0, 0, 0)},
			{Name: "scores", Type: meta.OfArray("postgres", "int4", 0, 0, 0), Nullable: true},
		},
	}}
	file, err := GenerateCatalog(meta.Metadata{Tables: tables}, CatalogOptions{PackageName: "schema"})
	if err != nil {
		t.Fatalf("GenerateCatalog() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`sqldef.Column("tags", sqldef.Array(sqldef.Text)),`,
		`sqldef.Column("scores", sqldef.Array(sqldef.Int), sqldef.Nullable()),`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated catalog missing %q\n%s", want, got)
		}
	}
}

func TestGenerateCatalogForeignKeys(t *testing.T) {
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
	file, err := GenerateCatalog(meta.Metadata{Tables: tables}, CatalogOptions{PackageName: "schema"})
	if err != nil {
		t.Fatalf("GenerateCatalog() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`var Public = Catalog.Schema("public")`,
		`sqldef.ForeignKey([]string{"order_region", "order_number"}, "orders", []string{"region", "number"}),`,
		`sqldef.References("order_lines", "id")`, // self-reference is an ordinary named reference
		`foreign key fk_other_schema references audit.events outside this catalog`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated catalog missing %q\n%s", want, got)
		}
	}
}

func TestGenerateCatalogIndexesChecksComments(t *testing.T) {
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
				{Name: "products_price_check", Expression: sql.Raw("price > 0")},
			},
		},
	}
	file, err := GenerateCatalog(meta.Metadata{Tables: tables}, CatalogOptions{PackageName: "schema"})
	if err != nil {
		t.Fatalf("GenerateCatalog() error = %v", err)
	}
	got := string(file.Content)
	for _, want := range []string{
		`sqldef.TableComment("Sellable products"),`,
		`sqldef.Column("sku", sqldef.VarChar(64), sqldef.Unique(), sqldef.Comment("Stock keeping unit")),`,
		`sqldef.Index("name"),`,
		`sqldef.UniqueIndex("vendor", "name"),`,
		`sqldef.Check(sql.Raw("price > 0")),`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated catalog missing %q\n%s", want, got)
		}
	}
}

// TestGenerateCatalogSameNameDifferentSchemas: the same table name in two
// schemas must get distinct variables and correctly-scoped foreign keys.
func TestGenerateCatalogSameNameDifferentSchemas(t *testing.T) {
	tables := []meta.Table{
		{
			Schema:  meta.Schema{Name: "public"},
			Name:    "users",
			Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}},
		},
		{
			Schema: meta.Schema{Name: "audit"},
			Name:   "users",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
				{Name: "public_user_id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
			},
			ForeignKeys: []meta.ForeignKey{
				{Name: "fk", Columns: []string{"public_user_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}
	got := string(mustGenerateCatalog(t, tables))
	for _, want := range []string{
		"var Users = Public.Table(\"users\",",         // public.users
		"var UsersTable = Audit.Table(\"users\",",     // audit.users gets a distinct var
		"sqldef.References(\"public.users\", \"id\")", // audit.users FK targets public.users
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated catalog missing %q\n%s", want, got)
		}
	}
	if strings.Count(got, "var Users ") != 1 || strings.Count(got, "var UsersTable ") != 1 {
		t.Errorf("expected one Users and one UsersTable declaration:\n%s", got)
	}
}

func mustGenerateCatalog(t *testing.T, tables []meta.Table) []byte {
	t.Helper()
	file, err := GenerateCatalog(meta.Metadata{Tables: tables}, CatalogOptions{PackageName: "schema"})
	if err != nil {
		t.Fatalf("GenerateCatalog() error = %v", err)
	}
	return file.Content
}

func TestGenerateCatalogErrors(t *testing.T) {
	if _, err := GenerateCatalog(meta.Metadata{Tables: fixtureTables()}, CatalogOptions{}); err == nil {
		t.Error("GenerateCatalog() without package name: expected error")
	}
	if _, err := GenerateCatalog(meta.Metadata{}, CatalogOptions{PackageName: "schema"}); err == nil {
		t.Error("GenerateCatalog() without tables: expected error")
	}
}
