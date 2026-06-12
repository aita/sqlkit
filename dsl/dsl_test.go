package dsl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/dsl"
	"github.com/aita/sqlkit/schema"
)

// blogDesign mirrors examples/blog/schema.sql.
func blogDesign() *dsl.SchemaExpr {
	return dsl.Schema("public", func() {
		dsl.Table("users", func() {
			dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Default())
			dsl.Column("email", dsl.VarChar(255))
			dsl.Column("name", dsl.Text, dsl.Nullable())
			dsl.Column("created_at", dsl.Timestamp, dsl.Default())
		})
		dsl.Table("posts", func() {
			dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Default())
			dsl.Column("user_id", dsl.BigInt, dsl.References("users", "id"))
			dsl.Column("title", dsl.Text)
			dsl.Column("body", dsl.Text, dsl.Nullable())
			dsl.Column("published", dsl.Bool, dsl.Default())
		})
	})
}

func TestBuildPostgres(t *testing.T) {
	got, err := blogDesign().Build("postgres")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	want := schema.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []schema.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []schema.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "email", DataType: "varchar", Length: 255},
					{Name: "name", DataType: "text", Nullable: true},
					{Name: "created_at", DataType: "timestamptz", HasDefault: true},
				},
			},
			{
				Schema: "public",
				Name:   "posts",
				Columns: []schema.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "user_id", DataType: "int8"},
					{Name: "title", DataType: "text"},
					{Name: "body", DataType: "text", Nullable: true},
					{Name: "published", DataType: "bool", HasDefault: true},
				},
				ForeignKeys: []schema.ForeignKey{
					{Name: "fk_posts_user_id", Columns: []string{"user_id"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Build() = %+v\nwant %+v", got, want)
	}
}

func TestBuildMySQL(t *testing.T) {
	design := dsl.Schema("", func() {
		dsl.Table("items", func() {
			dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Default())
			dsl.Column("sku", dsl.UUID)
			dsl.Column("price", dsl.Decimal(10, 2))
			dsl.Column("created_at", dsl.Timestamp)
		})
	})
	got, err := design.Build("mysql")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []schema.Column{
		{Name: "id", DataType: "bigint", PrimaryKey: true, HasDefault: true},
		{Name: "sku", DataType: "char", Length: 36},
		{Name: "price", DataType: "decimal", Precision: 10, Scale: 2},
		{Name: "created_at", DataType: "datetime"},
	}
	if !reflect.DeepEqual(got.Tables[0].Columns, want) {
		t.Errorf("Build() columns = %+v\nwant %+v", got.Tables[0].Columns, want)
	}
	if got.Dialect != "mysql" {
		t.Errorf("Build() dialect = %q, want mysql", got.Dialect)
	}
}

func TestBuildDefaultsToPostgres(t *testing.T) {
	got, err := blogDesign().Build("")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got.Dialect != "postgres" {
		t.Errorf("Build(\"\") dialect = %q, want postgres", got.Dialect)
	}
}

func TestCompositeForeignKey(t *testing.T) {
	design := dsl.Schema("public", func() {
		dsl.Table("orders", func() {
			dsl.Column("region", dsl.Text, dsl.PrimaryKey())
			dsl.Column("number", dsl.BigInt, dsl.PrimaryKey())
		})
		dsl.Table("order_lines", func() {
			dsl.Column("id", dsl.BigInt, dsl.PrimaryKey())
			dsl.Column("order_region", dsl.Text)
			dsl.Column("order_number", dsl.BigInt)
			dsl.ForeignKey([]string{"order_region", "order_number"}, "orders", []string{"region", "number"})
		})
	})
	got, err := design.Build("postgres")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := schema.ForeignKey{
		Name:       "fk_order_lines_order_region_order_number",
		Columns:    []string{"order_region", "order_number"},
		RefSchema:  "public",
		RefTable:   "orders",
		RefColumns: []string{"region", "number"},
	}
	if !reflect.DeepEqual(got.Tables[1].ForeignKeys, []schema.ForeignKey{want}) {
		t.Errorf("ForeignKeys = %+v\nwant %+v", got.Tables[1].ForeignKeys, want)
	}
}

func TestIndexesChecksComments(t *testing.T) {
	design := dsl.Schema("public", func() {
		dsl.Table("products", func() {
			dsl.TableComment("Sellable products")
			dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Default())
			dsl.Column("sku", dsl.VarChar(64), dsl.Unique(), dsl.Comment("Stock keeping unit"))
			dsl.Column("vendor", dsl.Text)
			dsl.Column("name", dsl.Text)
			dsl.Column("price", dsl.Decimal(10, 2))
			dsl.Index("name")
			dsl.UniqueIndex("vendor", "name")
			dsl.Check("price > 0")
		})
	})
	got, err := design.Build("postgres")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	table := got.Tables[0]
	if table.Comment != "Sellable products" {
		t.Errorf("table comment = %q", table.Comment)
	}
	if table.Columns[1].Comment != "Stock keeping unit" {
		t.Errorf("column comment = %q", table.Columns[1].Comment)
	}
	wantIndexes := []schema.Index{
		{Name: "uq_products_sku", Columns: []string{"sku"}, Unique: true},
		{Name: "idx_products_name", Columns: []string{"name"}},
		{Name: "uq_products_vendor_name", Columns: []string{"vendor", "name"}, Unique: true},
	}
	if !reflect.DeepEqual(table.Indexes, wantIndexes) {
		t.Errorf("indexes = %+v\nwant %+v", table.Indexes, wantIndexes)
	}
	wantChecks := []schema.Check{
		{Name: "ck_products_1", Expression: "price > 0"},
	}
	if !reflect.DeepEqual(table.Checks, wantChecks) {
		t.Errorf("checks = %+v\nwant %+v", table.Checks, wantChecks)
	}
}

func TestGenerate(t *testing.T) {
	files, err := blogDesign().Generate(codegen.Options{PackageName: "blogdb", EmitDTO: true})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("Generate() produced %d files, want 1", len(files))
	}
	got := string(files[0].Content)
	for _, want := range []string{
		"package blogdb",
		"var Users = func() UsersTable {",
		"ID        sqlkit.Column[int64]",
		"Name      sqlkit.Column[*string]", // Nullable -> pointer
		"CreatedAt sqlkit.Column[time.Time]",
		"type User struct {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated code missing %q\n%s", want, got)
		}
	}
}

func TestBuildErrors(t *testing.T) {
	tests := []struct {
		name   string
		design func() *dsl.SchemaExpr
		want   string
	}{
		{
			name:   "no tables",
			design: func() *dsl.SchemaExpr { return dsl.Schema("public", func() {}) },
			want:   "declares no tables",
		},
		{
			name: "no columns",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() { dsl.Table("users", func() {}) })
			},
			want: "declares no columns",
		},
		{
			name: "duplicate table",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() { dsl.Column("id", dsl.BigInt) })
					dsl.Table("users", func() { dsl.Column("id", dsl.BigInt) })
				})
			},
			want: `table "users" is declared twice`,
		},
		{
			name: "duplicate column",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() {
						dsl.Column("id", dsl.BigInt)
						dsl.Column("id", dsl.BigInt)
					})
				})
			},
			want: `column "id" is declared twice`,
		},
		{
			name: "nullable primary key",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() {
						dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Nullable())
					})
				})
			},
			want: "primary key column cannot be nullable",
		},
		{
			name: "unknown referenced table",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("posts", func() {
						dsl.Column("user_id", dsl.BigInt, dsl.References("users", "id"))
					})
				})
			},
			want: `references unknown table "users"`,
		},
		{
			name: "unknown referenced column",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() { dsl.Column("id", dsl.BigInt) })
					dsl.Table("posts", func() {
						dsl.Column("user_id", dsl.BigInt, dsl.References("users", "uuid"))
					})
				})
			},
			want: `references unknown column "uuid"`,
		},
		{
			name: "foreign key column count mismatch",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() { dsl.Column("id", dsl.BigInt) })
					dsl.Table("posts", func() {
						dsl.Column("user_id", dsl.BigInt)
						dsl.ForeignKey([]string{"user_id"}, "users", []string{"id", "email"})
					})
				})
			},
			want: "matching column lists",
		},
		{
			name: "duplicate references",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() { dsl.Column("id", dsl.BigInt) })
					dsl.Table("posts", func() {
						dsl.Column("user_id", dsl.BigInt, dsl.References("users", "id"), dsl.References("users", "id"))
					})
				})
			},
			want: "References is declared twice",
		},
		{
			name: "index on unknown column",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() {
						dsl.Column("id", dsl.BigInt)
						dsl.Index("email")
					})
				})
			},
			want: `index references unknown column "email"`,
		},
		{
			name: "index without columns",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() {
						dsl.Column("id", dsl.BigInt)
						dsl.Index()
					})
				})
			},
			want: "index declares no columns",
		},
		{
			name: "duplicate index",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() {
						dsl.Column("email", dsl.Text, dsl.Unique())
						dsl.UniqueIndex("email")
					})
				})
			},
			want: `index "uq_users_email" is declared twice`,
		},
		{
			name: "empty check",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() {
						dsl.Column("id", dsl.BigInt)
						dsl.Check("  ")
					})
				})
			},
			want: "Check requires an expression",
		},
		{
			name: "type unavailable on dialect",
			design: func() *dsl.SchemaExpr {
				return dsl.Schema("public", func() {
					dsl.Table("users", func() {
						dsl.Column("tags", dsl.TypeFor("citext", ""))
					})
				})
			},
			want: "not available on dialect",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialect := "postgres"
			if tt.name == "type unavailable on dialect" {
				dialect = "mysql"
			}
			_, err := tt.design().Build(dialect)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Build() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestBuildUnsupportedDialect(t *testing.T) {
	_, err := blogDesign().Build("oracle")
	if err == nil || !strings.Contains(err.Error(), "unsupported dialect") {
		t.Errorf("Build(oracle) error = %v, want unsupported dialect", err)
	}
}

func TestMisplacedCallsPanic(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected panic", name)
			}
		}()
		fn()
	}
	mustPanic("Table outside Schema", func() { dsl.Table("users", nil) })
	mustPanic("Column outside Table", func() { dsl.Column("id", dsl.BigInt) })
	mustPanic("ForeignKey outside Table", func() { dsl.ForeignKey(nil, "users", nil) })
	mustPanic("Index outside Table", func() { dsl.Index("id") })
	mustPanic("UniqueIndex outside Table", func() { dsl.UniqueIndex("id") })
	mustPanic("Check outside Table", func() { dsl.Check("id > 0") })
	mustPanic("TableComment outside Table", func() { dsl.TableComment("x") })
	mustPanic("nested Schema", func() {
		dsl.Schema("a", func() { dsl.Schema("b", nil) })
	})
	mustPanic("nested Table", func() {
		dsl.Schema("a", func() {
			dsl.Table("x", func() { dsl.Table("y", nil) })
		})
	})

	// Evaluation state is reset after a panic, so a fresh design still works.
	if _, err := blogDesign().Build("postgres"); err != nil {
		t.Errorf("Build() after panics error = %v", err)
	}
}
