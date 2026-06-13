package schema_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/schema"
)

// blogCatalog mirrors examples/blog: users and the posts that reference them.
func blogCatalog() *schema.Catalog {
	catalog := schema.NewCatalog()
	public := catalog.Schema("public")
	users := public.Table("users",
		schema.Column("id", schema.BigInt, schema.PrimaryKey(), schema.Default()),
		schema.Column("email", schema.VarChar(255)),
		schema.Column("name", schema.Text, schema.Nullable()),
		schema.Column("created_at", schema.Timestamp, schema.Default("now()")),
	)
	public.Table("posts",
		schema.Column("id", schema.BigInt, schema.PrimaryKey(), schema.Default()),
		schema.Column("user_id", schema.BigInt, schema.References(users, "id")),
		schema.Column("title", schema.Text),
		schema.Column("body", schema.Text, schema.Nullable()),
		schema.Column("published", schema.Bool, schema.Default("false")),
	)
	return catalog
}

func buildOne(t *testing.T, catalog *schema.Catalog, dialect string) meta.Schema {
	t.Helper()
	models, err := catalog.Build(dialect)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("Build() produced %d schemas, want 1", len(models))
	}
	return models[0]
}

func TestBuildPostgres(t *testing.T) {
	got := buildOne(t, blogCatalog(), "postgres")

	want := meta.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []meta.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []meta.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "email", DataType: "varchar", Length: 255},
					{Name: "name", DataType: "text", Nullable: true},
					{Name: "created_at", DataType: "timestamptz", HasDefault: true, DefaultExpr: "now()"},
				},
			},
			{
				Schema: "public",
				Name:   "posts",
				Columns: []meta.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "user_id", DataType: "int8"},
					{Name: "title", DataType: "text"},
					{Name: "body", DataType: "text", Nullable: true},
					{Name: "published", DataType: "bool", HasDefault: true, DefaultExpr: "false"},
				},
				ForeignKeys: []meta.ForeignKey{
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
	catalog := schema.NewCatalog()
	catalog.Table("items",
		schema.Column("id", schema.BigInt, schema.PrimaryKey(), schema.Default()),
		schema.Column("sku", schema.UUID),
		schema.Column("price", schema.Decimal(10, 2)),
		schema.Column("created_at", schema.Timestamp),
	)
	got := buildOne(t, catalog, "mysql")
	want := []meta.Column{
		{Name: "id", DataType: "bigint", PrimaryKey: true, HasDefault: true},
		{Name: "sku", DataType: "char", Length: 36},
		{Name: "price", DataType: "decimal", Precision: 10, Scale: 2},
		{Name: "created_at", DataType: "datetime"},
	}
	if !reflect.DeepEqual(got.Tables[0].Columns, want) {
		t.Errorf("Build() columns = %+v\nwant %+v", got.Tables[0].Columns, want)
	}
	if got.Dialect != "mysql" || got.Name != "" {
		t.Errorf("Build() schema = %q dialect = %q, want unnamed mysql", got.Name, got.Dialect)
	}
}

func TestBuildDefaultsToPostgres(t *testing.T) {
	got := buildOne(t, blogCatalog(), "")
	if got.Dialect != "postgres" {
		t.Errorf("Build(\"\") dialect = %q, want postgres", got.Dialect)
	}
}

func TestCompositeAndSelfForeignKeys(t *testing.T) {
	catalog := schema.NewCatalog()
	public := catalog.Schema("public")
	orders := public.Table("orders",
		schema.Column("region", schema.Text, schema.PrimaryKey()),
		schema.Column("number", schema.BigInt, schema.PrimaryKey()),
	)
	public.Table("order_lines",
		schema.Column("id", schema.BigInt, schema.PrimaryKey()),
		schema.Column("order_region", schema.Text),
		schema.Column("order_number", schema.BigInt),
		schema.ForeignKey([]string{"order_region", "order_number"}, orders, []string{"region", "number"}),
	)
	employees := public.Table("employees",
		schema.Column("id", schema.BigInt, schema.PrimaryKey()),
		schema.Column("manager_id", schema.BigInt, schema.Nullable()),
	)
	employees.ForeignKey([]string{"manager_id"}, employees, []string{"id"})

	got := buildOne(t, catalog, "postgres")
	wantLines := []meta.ForeignKey{{
		Name:       "fk_order_lines_order_region_order_number",
		Columns:    []string{"order_region", "order_number"},
		RefSchema:  "public",
		RefTable:   "orders",
		RefColumns: []string{"region", "number"},
	}}
	if !reflect.DeepEqual(got.Tables[1].ForeignKeys, wantLines) {
		t.Errorf("composite ForeignKeys = %+v\nwant %+v", got.Tables[1].ForeignKeys, wantLines)
	}
	wantSelf := []meta.ForeignKey{{
		Name:       "fk_employees_manager_id",
		Columns:    []string{"manager_id"},
		RefSchema:  "public",
		RefTable:   "employees",
		RefColumns: []string{"id"},
	}}
	if !reflect.DeepEqual(got.Tables[2].ForeignKeys, wantSelf) {
		t.Errorf("self ForeignKeys = %+v\nwant %+v", got.Tables[2].ForeignKeys, wantSelf)
	}
}

func TestCrossSchemaReference(t *testing.T) {
	catalog := schema.NewCatalog()
	audit := catalog.Schema("audit").Table("events",
		schema.Column("id", schema.BigInt, schema.PrimaryKey()),
	)
	catalog.Schema("public").Table("orders",
		schema.Column("id", schema.BigInt, schema.PrimaryKey()),
		schema.Column("event_id", schema.BigInt, schema.References(audit, "id")),
	)
	models, err := catalog.Build("postgres")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("Build() produced %d schemas, want 2", len(models))
	}
	fk := models[1].Tables[0].ForeignKeys[0]
	if fk.RefSchema != "audit" || fk.RefTable != "events" {
		t.Errorf("cross-schema fk = %+v", fk)
	}
}

func TestIndexesChecksComments(t *testing.T) {
	catalog := schema.NewCatalog()
	catalog.Table("products",
		schema.TableComment("Sellable products"),
		schema.Column("id", schema.BigInt, schema.PrimaryKey(), schema.Default()),
		schema.Column("sku", schema.VarChar(64), schema.Unique(), schema.Comment("Stock keeping unit")),
		schema.Column("vendor", schema.Text),
		schema.Column("name", schema.Text),
		schema.Column("price", schema.Decimal(10, 2)),
		schema.Index("name"),
		schema.UniqueIndex("vendor", "name"),
		schema.Check("price > 0"),
	)
	got := buildOne(t, catalog, "postgres")
	table := got.Tables[0]
	if table.Comment != "Sellable products" {
		t.Errorf("table comment = %q", table.Comment)
	}
	if table.Columns[1].Comment != "Stock keeping unit" {
		t.Errorf("column comment = %q", table.Columns[1].Comment)
	}
	wantIndexes := []meta.Index{
		{Name: "uq_products_sku", Columns: []string{"sku"}, Unique: true},
		{Name: "idx_products_name", Columns: []string{"name"}},
		{Name: "uq_products_vendor_name", Columns: []string{"vendor", "name"}, Unique: true},
	}
	if !reflect.DeepEqual(table.Indexes, wantIndexes) {
		t.Errorf("indexes = %+v\nwant %+v", table.Indexes, wantIndexes)
	}
	wantChecks := []meta.Check{
		{Name: "ck_products_1", Expression: "price > 0"},
	}
	if !reflect.DeepEqual(table.Checks, wantChecks) {
		t.Errorf("checks = %+v\nwant %+v", table.Checks, wantChecks)
	}
}

func TestSharedElementsReuse(t *testing.T) {
	audit := []schema.TableElement{
		schema.Column("created_at", schema.Timestamp, schema.Default("now()")),
		schema.Column("updated_at", schema.Timestamp, schema.Nullable()),
	}
	catalog := schema.NewCatalog()
	catalog.Table("a", append([]schema.TableElement{
		schema.Column("id", schema.BigInt, schema.PrimaryKey()),
	}, audit...)...)
	catalog.Table("b", append([]schema.TableElement{
		schema.Column("id", schema.BigInt, schema.PrimaryKey()),
	}, audit...)...)

	got := buildOne(t, catalog, "postgres")
	for _, table := range got.Tables {
		if len(table.Columns) != 3 || table.Columns[1].Name != "created_at" {
			t.Errorf("table %s columns = %+v", table.Name, table.Columns)
		}
	}
}

func TestGenerate(t *testing.T) {
	files, err := blogCatalog().Generate(codegen.Options{PackageName: "blogdb", EmitDTO: true})
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

func TestDDL(t *testing.T) {
	sql, err := blogCatalog().DDL("postgres")
	if err != nil {
		t.Fatalf("DDL() error = %v", err)
	}
	for _, want := range []string{
		`CREATE TABLE "public"."users" (`,
		`"id" bigserial,`,
		`"created_at" timestamptz NOT NULL DEFAULT now(),`,
		`PRIMARY KEY ("id")`,
		`ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id");`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("DDL missing %q\n%s", want, sql)
		}
	}
}

func TestBuildErrors(t *testing.T) {
	tests := []struct {
		name    string
		catalog func() *schema.Catalog
		dialect string
		want    string
	}{
		{
			name:    "no tables",
			catalog: schema.NewCatalog,
			want:    "declares no tables",
		},
		{
			name: "no columns",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users")
				return c
			},
			want: "declares no columns",
		},
		{
			name: "duplicate table",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users", schema.Column("id", schema.BigInt))
				c.Table("users", schema.Column("id", schema.BigInt))
				return c
			},
			want: `table "users" is declared twice`,
		},
		{
			name: "duplicate column",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users",
					schema.Column("id", schema.BigInt),
					schema.Column("id", schema.BigInt),
				)
				return c
			},
			want: `column "id" is declared twice`,
		},
		{
			name: "nullable primary key",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users", schema.Column("id", schema.BigInt, schema.PrimaryKey(), schema.Nullable()))
				return c
			},
			want: "primary key column cannot be nullable",
		},
		{
			name: "nil reference",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("posts", schema.Column("user_id", schema.BigInt, schema.References(nil, "id")))
				return c
			},
			want: "references a nil table",
		},
		{
			name: "reference into another catalog",
			catalog: func() *schema.Catalog {
				other := schema.NewCatalog()
				users := other.Table("users", schema.Column("id", schema.BigInt))
				c := schema.NewCatalog()
				c.Table("posts", schema.Column("user_id", schema.BigInt, schema.References(users, "id")))
				return c
			},
			want: "from a different catalog",
		},
		{
			name: "unknown referenced column",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				users := c.Table("users", schema.Column("id", schema.BigInt))
				c.Table("posts", schema.Column("user_id", schema.BigInt, schema.References(users, "uuid")))
				return c
			},
			want: `references unknown column "uuid"`,
		},
		{
			name: "foreign key column count mismatch",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				users := c.Table("users", schema.Column("id", schema.BigInt))
				c.Table("posts",
					schema.Column("user_id", schema.BigInt),
					schema.ForeignKey([]string{"user_id"}, users, []string{"id", "email"}),
				)
				return c
			},
			want: "matching column lists",
		},
		{
			name: "duplicate references",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				users := c.Table("users", schema.Column("id", schema.BigInt))
				c.Table("posts",
					schema.Column("user_id", schema.BigInt, schema.References(users, "id"), schema.References(users, "id")),
				)
				return c
			},
			want: "References is declared twice",
		},
		{
			name: "index on unknown column",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users", schema.Column("id", schema.BigInt), schema.Index("email"))
				return c
			},
			want: `index references unknown column "email"`,
		},
		{
			name: "index without columns",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users", schema.Column("id", schema.BigInt), schema.Index())
				return c
			},
			want: "index declares no columns",
		},
		{
			name: "duplicate index",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users",
					schema.Column("email", schema.Text, schema.Unique()),
					schema.UniqueIndex("email"),
				)
				return c
			},
			want: `index "uq_users_email" is declared twice`,
		},
		{
			name: "empty check",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users", schema.Column("id", schema.BigInt), schema.Check("  "))
				return c
			},
			want: "Check requires an expression",
		},
		{
			name: "default with two expressions",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users", schema.Column("id", schema.BigInt, schema.Default("a", "b")))
				return c
			},
			want: "Default takes at most one expression",
		},
		{
			name: "type unavailable on dialect",
			catalog: func() *schema.Catalog {
				c := schema.NewCatalog()
				c.Table("users", schema.Column("tags", schema.TypeFor("citext", "")))
				return c
			},
			dialect: "mysql",
			want:    "not available on dialect",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialect := tt.dialect
			if dialect == "" {
				dialect = "postgres"
			}
			_, err := tt.catalog().Build(dialect)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Build() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestBuildUnsupportedDialect(t *testing.T) {
	_, err := blogCatalog().Build("oracle")
	if err == nil || !strings.Contains(err.Error(), "unsupported dialect") {
		t.Errorf("Build(oracle) error = %v, want unsupported dialect", err)
	}
}
