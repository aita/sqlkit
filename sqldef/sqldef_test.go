package sqldef_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sqldef"
)

// blogCatalog mirrors examples/blog: users and the posts that reference them.
func blogCatalog() *sqldef.Catalog {
	catalog := sqldef.NewCatalog()
	public := catalog.Schema("public")
	users := public.Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("email", sqldef.VarChar(255)),
		sqldef.Column("name", sqldef.Text, sqldef.Nullable()),
		sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default("now()")),
	)
	public.Table("posts",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id")),
		sqldef.Column("title", sqldef.Text),
		sqldef.Column("body", sqldef.Text, sqldef.Nullable()),
		sqldef.Column("published", sqldef.Bool, sqldef.Default("false")),
	)
	return catalog
}

func buildCatalog(t *testing.T, c *sqldef.Catalog) meta.Meta {
	t.Helper()
	catalog, err := c.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return catalog
}

func TestBuild(t *testing.T) {
	got := buildCatalog(t, blogCatalog())

	// Column types stay portable (the sqldef constructors are meta.Type values),
	// resolved to an engine name only by the generators.
	want := meta.Meta{
		Tables: []meta.Table{
			{
				Schema: meta.Schema{Name: "public"},
				Name:   "users",
				Columns: []meta.Column{
					{Name: "id", Type: sqldef.BigInt, PrimaryKey: true, HasDefault: true},
					{Name: "email", Type: sqldef.VarChar(255)},
					{Name: "name", Type: sqldef.Text, Nullable: true},
					{Name: "created_at", Type: sqldef.Timestamp, HasDefault: true, DefaultExpr: "now()"},
				},
			},
			{
				Schema: meta.Schema{Name: "public"},
				Name:   "posts",
				Columns: []meta.Column{
					{Name: "id", Type: sqldef.BigInt, PrimaryKey: true, HasDefault: true},
					{Name: "user_id", Type: sqldef.BigInt},
					{Name: "title", Type: sqldef.Text},
					{Name: "body", Type: sqldef.Text, Nullable: true},
					{Name: "published", Type: sqldef.Bool, HasDefault: true, DefaultExpr: "false"},
				},
				ForeignKeys: []meta.ForeignKey{
					{Name: "fk_posts_user_id", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Build() = %+v\nwant %+v", got, want)
	}
}

func TestBuildPortableTypes(t *testing.T) {
	catalog := sqldef.NewCatalog()
	catalog.Table("items",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("sku", sqldef.UUID),
		sqldef.Column("price", sqldef.Decimal(10, 2)),
		sqldef.Column("created_at", sqldef.Timestamp),
	)
	got := buildCatalog(t, catalog)
	cols := got.Tables[0].Columns
	// UUID is postgres uuid but mysql char(36): one portable type resolves both.
	if eng, _ := cols[1].Type.Engine("postgres"); eng.Name != "uuid" {
		t.Errorf("sku postgres type = %q, want uuid", eng.Name)
	}
	if eng, _ := cols[1].Type.Engine("mysql"); eng.Name != "char" || eng.Length != 36 {
		t.Errorf("sku mysql type = %q(%d), want char(36)", eng.Name, eng.Length)
	}
	// id resolves per dialect from one portable declaration.
	if eng, _ := cols[0].Type.Engine("postgres"); eng.Name != "int8" {
		t.Errorf("id postgres type = %q, want int8", eng.Name)
	}
	if eng, _ := cols[0].Type.Engine("mysql"); eng.Name != "bigint" {
		t.Errorf("id mysql type = %q, want bigint", eng.Name)
	}
	if got.Tables[0].Schema.Name != "" {
		t.Errorf("Build() table schema = %q, want unnamed default", got.Tables[0].Schema.Name)
	}
}

func TestCompositeAndSelfForeignKeys(t *testing.T) {
	catalog := sqldef.NewCatalog()
	public := catalog.Schema("public")
	orders := public.Table("orders",
		sqldef.Column("region", sqldef.Text, sqldef.PrimaryKey()),
		sqldef.Column("number", sqldef.BigInt, sqldef.PrimaryKey()),
	)
	public.Table("order_lines",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
		sqldef.Column("order_region", sqldef.Text),
		sqldef.Column("order_number", sqldef.BigInt),
		sqldef.ForeignKey([]string{"order_region", "order_number"}, orders, []string{"region", "number"}),
	)
	employees := public.Table("employees",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
		sqldef.Column("manager_id", sqldef.BigInt, sqldef.Nullable()),
	)
	catalog.AddForeignKey(employees, []string{"manager_id"}, employees, []string{"id"})

	got := buildCatalog(t, catalog)
	wantLines := []meta.ForeignKey{{
		Name:       "fk_order_lines_order_region_order_number",
		Columns:    []string{"order_region", "order_number"},
		RefSchema:  meta.Schema{Name: "public"},
		RefTable:   "orders",
		RefColumns: []string{"region", "number"},
	}}
	if !reflect.DeepEqual(got.Tables[1].ForeignKeys, wantLines) {
		t.Errorf("composite ForeignKeys = %+v\nwant %+v", got.Tables[1].ForeignKeys, wantLines)
	}
	wantSelf := []meta.ForeignKey{{
		Name:       "fk_employees_manager_id",
		Columns:    []string{"manager_id"},
		RefSchema:  meta.Schema{Name: "public"},
		RefTable:   "employees",
		RefColumns: []string{"id"},
	}}
	if !reflect.DeepEqual(got.Tables[2].ForeignKeys, wantSelf) {
		t.Errorf("self ForeignKeys = %+v\nwant %+v", got.Tables[2].ForeignKeys, wantSelf)
	}
}

func TestDefaultSchema(t *testing.T) {
	catalog := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	users := catalog.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()))
	if users.Schema.Name != "public" {
		t.Errorf("default schema = %q, want public", users.Schema.Name)
	}
	got := buildCatalog(t, catalog)
	if got.Tables[0].Schema.Name != "public" {
		t.Errorf("built table schema = %q, want public", got.Tables[0].Schema.Name)
	}
}

func TestCrossSchemaReference(t *testing.T) {
	catalog := sqldef.NewCatalog()
	audit := catalog.Schema("audit").Table("events",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
	)
	catalog.Schema("public").Table("orders",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
		sqldef.Column("event_id", sqldef.BigInt, sqldef.References(audit, "id")),
	)
	got, err := catalog.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if schemas := got.Schemas(); len(schemas) != 2 {
		t.Fatalf("Build() spans %d schemas, want 2 (audit, public)", len(schemas))
	}
	// orders is the second table (audit.events is first).
	fk := got.Tables[1].ForeignKeys[0]
	if fk.RefSchema.Name != "audit" || fk.RefTable != "events" {
		t.Errorf("cross-schema fk = %+v", fk)
	}
}

func TestIndexesChecksComments(t *testing.T) {
	catalog := sqldef.NewCatalog()
	catalog.Table("products",
		sqldef.TableComment("Sellable products"),
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("sku", sqldef.VarChar(64), sqldef.Unique(), sqldef.Comment("Stock keeping unit")),
		sqldef.Column("vendor", sqldef.Text),
		sqldef.Column("name", sqldef.Text),
		sqldef.Column("price", sqldef.Decimal(10, 2)),
		sqldef.Index("name"),
		sqldef.UniqueIndex("vendor", "name"),
		sqldef.Check("price > 0"),
	)
	got := buildCatalog(t, catalog)
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
	audit := []sqldef.TableElement{
		sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default("now()")),
		sqldef.Column("updated_at", sqldef.Timestamp, sqldef.Nullable()),
	}
	catalog := sqldef.NewCatalog()
	catalog.Table("a", append([]sqldef.TableElement{
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
	}, audit...)...)
	catalog.Table("b", append([]sqldef.TableElement{
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
	}, audit...)...)

	got := buildCatalog(t, catalog)
	for _, table := range got.Tables {
		if len(table.Columns) != 3 || table.Columns[1].Name != "created_at" {
			t.Errorf("table %s columns = %+v", table.Name, table.Columns)
		}
	}
}

// TestSchemalessDatabase covers MySQL/SQLite-style databases without a schema
// namespace: Catalog.Table declares into the unnamed default schema, so tables
// carry an empty schema and generate unqualified DDL.
func TestSchemalessDatabase(t *testing.T) {
	catalog := sqldef.NewCatalog()
	catalog.Table("items",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("name", sqldef.VarChar(100)),
	)
	got := buildCatalog(t, catalog)
	if got.Tables[0].Schema.Name != "" {
		t.Errorf("schemaless table schema = %q, want empty", got.Tables[0].Schema.Name)
	}
	sql, err := catalog.DDL("mysql")
	if err != nil {
		t.Fatalf("DDL() error = %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE `items` (") {
		t.Errorf("schemaless DDL should be unqualified:\n%s", sql)
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
		catalog func() *sqldef.Catalog
		want    string
	}{
		{
			name:    "no tables",
			catalog: func() *sqldef.Catalog { return sqldef.NewCatalog() },
			want:    "declares no tables",
		},
		{
			name: "no columns",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users")
				return c
			},
			want: "declares no columns",
		},
		{
			name: "duplicate table",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users", sqldef.Column("id", sqldef.BigInt))
				c.Table("users", sqldef.Column("id", sqldef.BigInt))
				return c
			},
			want: `table "users" is declared twice`,
		},
		{
			name: "duplicate column",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users",
					sqldef.Column("id", sqldef.BigInt),
					sqldef.Column("id", sqldef.BigInt),
				)
				return c
			},
			want: `column "id" is declared twice`,
		},
		{
			name: "nullable primary key",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Nullable()))
				return c
			},
			want: "primary key column cannot be nullable",
		},
		{
			name: "zero-value reference",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("posts", sqldef.Column("user_id", sqldef.BigInt, sqldef.References(meta.Table{}, "id")))
				return c
			},
			want: "references an unnamed table",
		},
		{
			name: "reference into another catalog",
			catalog: func() *sqldef.Catalog {
				other := sqldef.NewCatalog()
				users := other.Table("users", sqldef.Column("id", sqldef.BigInt))
				c := sqldef.NewCatalog()
				c.Table("posts", sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id")))
				return c
			},
			want: "not declared in this catalog",
		},
		{
			name: "unknown referenced column",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				users := c.Table("users", sqldef.Column("id", sqldef.BigInt))
				c.Table("posts", sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "uuid")))
				return c
			},
			want: `references unknown column "uuid"`,
		},
		{
			name: "foreign key column count mismatch",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				users := c.Table("users", sqldef.Column("id", sqldef.BigInt))
				c.Table("posts",
					sqldef.Column("user_id", sqldef.BigInt),
					sqldef.ForeignKey([]string{"user_id"}, users, []string{"id", "email"}),
				)
				return c
			},
			want: "matching column lists",
		},
		{
			name: "duplicate references",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				users := c.Table("users", sqldef.Column("id", sqldef.BigInt))
				c.Table("posts",
					sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id"), sqldef.References(users, "id")),
				)
				return c
			},
			want: "References is declared twice",
		},
		{
			name: "index on unknown column",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users", sqldef.Column("id", sqldef.BigInt), sqldef.Index("email"))
				return c
			},
			want: `index references unknown column "email"`,
		},
		{
			name: "index without columns",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users", sqldef.Column("id", sqldef.BigInt), sqldef.Index())
				return c
			},
			want: "index declares no columns",
		},
		{
			name: "duplicate index",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users",
					sqldef.Column("email", sqldef.Text, sqldef.Unique()),
					sqldef.UniqueIndex("email"),
				)
				return c
			},
			want: `index "uq_users_email" is declared twice`,
		},
		{
			name: "empty check",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users", sqldef.Column("id", sqldef.BigInt), sqldef.Check("  "))
				return c
			},
			want: "Check requires an expression",
		},
		{
			name: "default with two expressions",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.Default("a", "b")))
				return c
			},
			want: "Default takes at most one expression",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.catalog().Build()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Build() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestTypeUnavailableOnDialect: a postgres-only type builds fine (types are
// portable, resolved late) but errors when a generator targets the dialect it
// has no engine name for.
func TestTypeUnavailableOnDialect(t *testing.T) {
	c := sqldef.NewCatalog()
	c.Table("users", sqldef.Column("tags", sqldef.TypeFor("citext", "")))
	if _, err := c.Build(); err != nil {
		t.Fatalf("Build() should not resolve types: %v", err)
	}
	if _, err := c.DDL("mysql"); err == nil || !strings.Contains(err.Error(), "no type on dialect mysql") {
		t.Errorf("DDL(mysql) error = %v, want unavailable-type error", err)
	}
}

func TestUnsupportedDialect(t *testing.T) {
	_, err := blogCatalog().DDL("oracle")
	if err == nil || !strings.Contains(err.Error(), "unsupported dialect") {
		t.Errorf("DDL(oracle) error = %v, want unsupported dialect", err)
	}
}
