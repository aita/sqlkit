package decl_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/meta"
	querysql "github.com/aita/sqlkit/sql"
)

// blogCatalog mirrors examples/generated: users and the posts that reference them.
func blogCatalog() *decl.Catalog {
	catalog := decl.NewCatalog()
	public := catalog.Schema("public")
	public.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.VarChar(255)),
		decl.Column("name", decl.Text, decl.Nullable()),
		decl.Column("created_at", decl.Timestamp, decl.Default(querysql.Raw("now()"))),
	)
	public.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt, decl.References("users", "id")),
		decl.Column("title", decl.Text),
		decl.Column("body", decl.Text, decl.Nullable()),
		decl.Column("published", decl.Bool, decl.Default(querysql.Raw("false"))),
	)
	return catalog
}

func buildCatalog(t *testing.T, c *decl.Catalog) meta.Metadata {
	t.Helper()
	md, err := c.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return md
}

func TestBuild(t *testing.T) {
	got := buildCatalog(t, blogCatalog())

	// Column types stay portable (the decl constructors are meta.Type values),
	// resolved to an engine name only by the generators.
	want := meta.Metadata{
		Tables: []meta.Table{
			{
				Schema: meta.Schema{Name: "public"},
				Name:   "users",
				Columns: []meta.Column{
					{Name: "id", Type: decl.BigInt, PrimaryKey: true, HasDefault: true},
					{Name: "email", Type: decl.VarChar(255)},
					{Name: "name", Type: decl.Text, Nullable: true},
					{Name: "created_at", Type: decl.Timestamp, HasDefault: true, DefaultExpr: querysql.Raw("now()")},
				},
			},
			{
				Schema: meta.Schema{Name: "public"},
				Name:   "posts",
				Columns: []meta.Column{
					{Name: "id", Type: decl.BigInt, PrimaryKey: true, HasDefault: true},
					{Name: "user_id", Type: decl.BigInt},
					{Name: "title", Type: decl.Text},
					{Name: "body", Type: decl.Text, Nullable: true},
					{Name: "published", Type: decl.Bool, HasDefault: true, DefaultExpr: querysql.Raw("false")},
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

func TestSchemaCreateOption(t *testing.T) {
	catalog := decl.NewCatalog()
	// A plain schema is assumed to exist; a Create schema is managed.
	catalog.Schema("public").Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
	)
	catalog.Schema("audit", decl.Create()).Table("log",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
	)
	md := buildCatalog(t, catalog)

	byName := map[string]meta.Schema{}
	for _, s := range md.Schemas() {
		byName[s.Name] = s
	}
	if byName["public"].Create {
		t.Errorf("public schema should not be marked Create")
	}
	if !byName["audit"].Create {
		t.Errorf("audit schema should be marked Create")
	}

	pg, err := querysql.DialectByName("postgres")
	if err != nil {
		t.Fatalf("DialectByName() error = %v", err)
	}
	got, err := md.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if !strings.Contains(got, `CREATE SCHEMA IF NOT EXISTS "audit";`) {
		t.Errorf("expected CREATE SCHEMA for audit:\n%s", got)
	}
	if strings.Contains(got, `CREATE SCHEMA IF NOT EXISTS "public";`) {
		t.Errorf("did not expect CREATE SCHEMA for public:\n%s", got)
	}
}

// TestSchemaCreateObjectOnly pins that a Create schema hosting only an enum (no
// table) is still created: the enum's CREATE TYPE is schema-qualified, so a
// missing CREATE SCHEMA would make the generated SQL reference a schema that does
// not exist.
func TestSchemaCreateObjectOnly(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Schema("audit", decl.Create()).Enum("mood", "happy", "sad")
	md := buildCatalog(t, catalog)

	pg, err := querysql.DialectByName("postgres")
	if err != nil {
		t.Fatalf("DialectByName() error = %v", err)
	}
	got, err := md.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	schemaAt := strings.Index(got, `CREATE SCHEMA IF NOT EXISTS "audit";`)
	if schemaAt < 0 {
		t.Fatalf("expected CREATE SCHEMA for the enum-only audit schema:\n%s", got)
	}
	typeAt := strings.Index(got, `CREATE TYPE "audit"."mood"`)
	if typeAt < 0 {
		t.Fatalf("expected the enum type in the audit schema:\n%s", got)
	}
	if schemaAt > typeAt {
		t.Errorf("CREATE SCHEMA must precede the CREATE TYPE that needs it:\n%s", got)
	}
}

func TestBuildPortableTypes(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Table("items",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("sku", decl.UUID),
		decl.Column("price", decl.Decimal(10, 2)),
		decl.Column("created_at", decl.Timestamp),
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
	catalog := decl.NewCatalog()
	public := catalog.Schema("public")
	// order_lines references orders, declared before it; a composite key names
	// the target table by name.
	public.Table("order_lines",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("order_region", decl.Text),
		decl.Column("order_number", decl.BigInt),
		decl.ForeignKey([]string{"order_region", "order_number"}, "orders", []string{"region", "number"}),
	)
	public.Table("orders",
		decl.Column("region", decl.Text, decl.PrimaryKey()),
		decl.Column("number", decl.BigInt, decl.PrimaryKey()),
	)
	// employees self-references by name, resolved at Build — no separate
	// post-declaration step is needed.
	public.Table("employees",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("manager_id", decl.BigInt, decl.Nullable(), decl.References("employees", "id")),
	)

	got := buildCatalog(t, catalog)
	wantLines := []meta.ForeignKey{{
		Name:       "fk_order_lines_order_region_order_number",
		Columns:    []string{"order_region", "order_number"},
		RefSchema:  meta.Schema{Name: "public"},
		RefTable:   "orders",
		RefColumns: []string{"region", "number"},
	}}
	if !reflect.DeepEqual(got.Tables[0].ForeignKeys, wantLines) {
		t.Errorf("composite ForeignKeys = %+v\nwant %+v", got.Tables[0].ForeignKeys, wantLines)
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
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	users := catalog.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey()))
	if users.Schema.Name != "public" {
		t.Errorf("default schema = %q, want public", users.Schema.Name)
	}
	got := buildCatalog(t, catalog)
	if got.Tables[0].Schema.Name != "public" {
		t.Errorf("built table schema = %q, want public", got.Tables[0].Schema.Name)
	}
}

// TestBuildColumnDropsTableLevelConstructs checks that BuildColumn (used by
// ALTER TABLE ADD COLUMN) accepts a column carrying References/Unique — those
// are table-level constructs and are dropped, not validated against a catalog
// that has no such table, which is the common "add the FK column, then
// CreateForeignKey" pattern.
func TestBuildColumnDropsTableLevelConstructs(t *testing.T) {
	col, err := decl.BuildColumn(
		decl.Column("user_id", decl.BigInt, decl.References("users", "id"), decl.Unique()),
	)
	if err != nil {
		t.Fatalf("BuildColumn with References/Unique: %v", err)
	}
	if col.Name != "user_id" {
		t.Errorf("column name = %q, want user_id", col.Name)
	}
}

// TestBuildTableSkipsEnumValidation covers BuildTable's use for a migration
// operation that adds one table/column in isolation (migrate/op.go's
// CreateTable/AddColumn): an EnumType column may legitimately reference an
// enum a separate, earlier migration already created, not one declared in
// this standalone build's own (nonexistent) catalog — so BuildTable must not
// apply Catalog.Build's dangling-enum-reference check, unlike a real,
// caller-constructed Catalog.
func TestBuildTableSkipsEnumValidation(t *testing.T) {
	tbl, err := decl.BuildTable("public", "users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("mood", decl.EnumType("mood")),
	)
	if err != nil {
		t.Fatalf("BuildTable with an EnumType column and no declared Enum: %v", err)
	}
	if tbl.Name != "users" {
		t.Errorf("table name = %q, want users", tbl.Name)
	}
}

func TestCrossSchemaReference(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Schema("audit").Table("events",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
	)
	catalog.Schema("public").Table("orders",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("event_id", decl.BigInt, decl.References("audit.events", "id")),
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
	catalog := decl.NewCatalog()
	catalog.Table("products",
		decl.TableComment("Sellable products"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("sku", decl.VarChar(64), decl.Unique(), decl.Comment("Stock keeping unit")),
		decl.Column("vendor", decl.Text),
		decl.Column("name", decl.Text),
		decl.Column("price", decl.Decimal(10, 2)),
		decl.Index("name"),
		decl.UniqueIndex("vendor", "name"),
		decl.Check(querysql.Raw("price > 0")),
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
		{Name: "ck_products_1", Expression: querysql.Raw("price > 0")},
	}
	if !reflect.DeepEqual(table.Checks, wantChecks) {
		t.Errorf("checks = %+v\nwant %+v", table.Checks, wantChecks)
	}
}

func TestSharedElementsReuse(t *testing.T) {
	audit := []decl.TableElement{
		decl.Column("created_at", decl.Timestamp, decl.Default(querysql.Raw("now()"))),
		decl.Column("updated_at", decl.Timestamp, decl.Nullable()),
	}
	catalog := decl.NewCatalog()
	catalog.Table("a", append([]decl.TableElement{
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
	}, audit...)...)
	catalog.Table("b", append([]decl.TableElement{
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
	}, audit...)...)

	got := buildCatalog(t, catalog)
	for _, table := range got.Tables {
		if len(table.Columns) != 3 || table.Columns[1].Name != "created_at" {
			t.Errorf("table %s columns = %+v", table.Name, table.Columns)
		}
	}
}

// TestBlankDefaultIsAbsent: a blank Default expression (an empty sql.Raw) is
// normalized to absent, so an integer primary key still renders as serial
// rather than emitting an invalid empty DEFAULT clause.
func TestBlankDefaultIsAbsent(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Table("items",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default(querysql.Raw(""))),
	)
	got := buildCatalog(t, catalog)
	col := got.Tables[0].Columns[0]
	if !col.HasDefault || col.DefaultExpr != nil {
		t.Fatalf("blank default: HasDefault=%v DefaultExpr=%v, want true and nil", col.HasDefault, col.DefaultExpr)
	}
	sql, err := got.CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if !strings.Contains(sql, `"id" bigserial`) {
		t.Errorf("blank default on integer PK should render serial:\n%s", sql)
	}
	if strings.Contains(sql, "DEFAULT ,") || strings.Contains(sql, "DEFAULT \n") {
		t.Errorf("blank default must not emit an empty DEFAULT clause:\n%s", sql)
	}
}

// TestSchemalessDatabase covers MySQL-style databases without a schema
// namespace: Catalog.Table declares into the unnamed default schema, so tables
// carry an empty schema and generate unqualified DDL.
func TestSchemalessDatabase(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Table("items",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.VarChar(100)),
	)
	got := buildCatalog(t, catalog)
	if got.Tables[0].Schema.Name != "" {
		t.Errorf("schemaless table schema = %q, want empty", got.Tables[0].Schema.Name)
	}
	sql, err := got.CreateSQL(querysql.MySQLDialect{})
	if err != nil {
		t.Fatalf("DDL() error = %v", err)
	}
	if !strings.Contains(sql, "CREATE TABLE `items` (") {
		t.Errorf("schemaless DDL should be unqualified:\n%s", sql)
	}
}

func TestGenerate(t *testing.T) {
	files, err := codegen.Generate(buildCatalog(t, blogCatalog()), codegen.Options{PackageName: "blogdb", EmitDTO: true})
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

func TestBindingName(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Table("tbl_person",
		decl.BindingName("Person"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("name", decl.Text),
	)
	catalog.Table("widgets",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
	)
	md := buildCatalog(t, catalog)

	// The override lives on the catalog (a codegen hint), not on the schema model.
	bindingName := catalog.BindingNames()
	if bindingName == nil {
		t.Fatal("BindingNames() should be non-nil when an override is declared")
	}
	if got := bindingName(md.Tables[0]); got != "Person" {
		t.Errorf("BindingNames() for tbl_person = %q, want Person", got)
	}
	if got := bindingName(md.Tables[1]); got != "" {
		t.Errorf("BindingNames() for widgets = %q, want empty (default)", got)
	}

	files, err := codegen.Generate(md, codegen.Options{PackageName: "appdb", BindingName: bindingName})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := string(files[0].Content)
	for _, want := range []string{
		"type PersonTable struct",
		"var Person = func() PersonTable",
		"type WidgetsTable struct", // no override -> default name
	} {
		if !strings.Contains(body, want) {
			t.Errorf("generated binding should contain %q:\n%s", want, body)
		}
	}
	// The SQL table name is unaffected — only the Go identifier changes.
	if !strings.Contains(body, `Name: "tbl_person"`) {
		t.Errorf("SQL table name should stay tbl_person:\n%s", body)
	}
}

func TestBindingNamesNilWithoutOverrides(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey()))
	if catalog.BindingNames() != nil {
		t.Error("BindingNames() should be nil when no table declares an override")
	}
}

func TestBindingNameRequiresName(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Table("items",
		decl.BindingName(""),
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
	)
	if _, err := catalog.Build(); err == nil {
		t.Fatal("empty BindingName should be a declaration error")
	}
}

func TestDDL(t *testing.T) {
	sql, err := buildCatalog(t, blogCatalog()).CreateSQL(querysql.PostgreSQLDialect{})
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
		catalog func() *decl.Catalog
		want    string
	}{
		{
			name:    "no objects",
			catalog: func() *decl.Catalog { return decl.NewCatalog() },
			want:    "declares no objects",
		},
		{
			name: "no columns",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users")
				return c
			},
			want: "declares no columns",
		},
		{
			name: "duplicate table",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt))
				c.Table("users", decl.Column("id", decl.BigInt))
				return c
			},
			want: `table "users" is declared twice`,
		},
		{
			name: "duplicate column",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users",
					decl.Column("id", decl.BigInt),
					decl.Column("id", decl.BigInt),
				)
				return c
			},
			want: `column "id" is declared twice`,
		},
		{
			name: "nullable primary key",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Nullable()))
				return c
			},
			want: "primary key column cannot be nullable",
		},
		{
			name: "empty reference",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("posts", decl.Column("user_id", decl.BigInt, decl.References("", "id")))
				return c
			},
			want: "references an unnamed table",
		},
		{
			name: "reference to undeclared table",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("posts", decl.Column("user_id", decl.BigInt, decl.References("users", "id")))
				return c
			},
			want: "not declared in this catalog",
		},
		{
			name: "enum type not declared",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt, decl.PrimaryKey()), decl.Column("mood", decl.EnumType("mood")))
				return c
			},
			want: `type references enum "mood", which is not declared`,
		},
		{
			name: "unknown referenced column",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt))
				c.Table("posts", decl.Column("user_id", decl.BigInt, decl.References("users", "uuid")))
				return c
			},
			want: `references unknown column "uuid"`,
		},
		{
			name: "foreign key column count mismatch",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt))
				c.Table("posts",
					decl.Column("user_id", decl.BigInt),
					decl.ForeignKey([]string{"user_id"}, "users", []string{"id", "email"}),
				)
				return c
			},
			want: "matching column lists",
		},
		{
			name: "duplicate references",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt))
				c.Table("posts",
					decl.Column("user_id", decl.BigInt, decl.References("users", "id"), decl.References("users", "id")),
				)
				return c
			},
			want: "References is declared twice",
		},
		{
			name: "index on unknown column",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt), decl.Index("email"))
				return c
			},
			want: `index references unknown column "email"`,
		},
		{
			name: "index without columns",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt), decl.Index())
				return c
			},
			want: "index declares no columns",
		},
		{
			name: "duplicate index",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users",
					decl.Column("email", decl.Text, decl.Unique()),
					decl.UniqueIndex("email"),
				)
				return c
			},
			want: `index "uq_users_email" is declared twice`,
		},
		{
			name: "empty check",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt), decl.Check(querysql.Raw("  ")))
				return c
			},
			want: "Check requires an expression",
		},
		{
			name: "default with two expressions",
			catalog: func() *decl.Catalog {
				c := decl.NewCatalog()
				c.Table("users", decl.Column("id", decl.BigInt, decl.Default(querysql.Raw("a"), querysql.Raw("b"))))
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
	c := decl.NewCatalog()
	c.Table("users", decl.Column("tags", decl.TypeFor(map[string]string{"postgres": "citext"})))
	md, err := c.Build()
	if err != nil {
		t.Fatalf("Build() should not resolve types: %v", err)
	}
	if _, err := md.CreateSQL(querysql.MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "no type on dialect mysql") {
		t.Errorf("DDL(mysql) error = %v, want unavailable-type error", err)
	}
}

// TestArrayColumns: Array(elem) declares a PostgreSQL array column. Its engine
// resolves to the element type flagged as an array on PostgreSQL (so DDL renders
// "elem[]"), is unavailable on MySQL, and codegen maps it to a Go slice.
func TestArrayColumns(t *testing.T) {
	tags := decl.Array(decl.Text)
	eng, ok := tags.Engine("postgres")
	if !ok || eng.Name != "text" || !eng.Array {
		t.Errorf("Array(Text).Engine(postgres) = %+v, ok=%v; want {Name:text, Array:true}", eng, ok)
	}
	if _, ok := tags.Engine("mysql"); ok {
		t.Error("Array(Text).Engine(mysql) should be unavailable")
	}

	c := decl.NewCatalog()
	c.Table("docs",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("tags", decl.Array(decl.Text)),
		decl.Column("scores", decl.Array(decl.Int), decl.Nullable()),
	)
	md, err := c.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	ddl, err := md.CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if !strings.Contains(ddl, `"tags" text[] NOT NULL`) {
		t.Errorf("DDL missing text[] column:\n%s", ddl)
	}
	if !strings.Contains(ddl, `"scores" integer[]`) {
		t.Errorf("DDL missing integer[] column:\n%s", ddl)
	}

	// Arrays are PostgreSQL-only: targeting MySQL reports the type as missing.
	if _, err := md.CreateSQL(querysql.MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "no type on dialect mysql") {
		t.Errorf("CreateSQL(mysql) error = %v, want unavailable-type error", err)
	}

	files, err := codegen.Generate(md, codegen.Options{PackageName: "docsdb"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := string(files[0].Content)
	if !strings.Contains(body, `sqlkit.NewColumn[[]string](info, "tags")`) {
		t.Errorf("generated tags column is not []string:\n%s", body)
	}
	// A nullable array stays a slice, not a pointer.
	if !strings.Contains(body, `sqlkit.NewColumn[[]int32](info, "scores")`) {
		t.Errorf("generated scores column is not []int32:\n%s", body)
	}
}

// TestArrayPinnedGoType: a Go type pinned on the element flows through codegen as
// a slice of the pinned type.
func TestArrayPinnedGoType(t *testing.T) {
	c := decl.NewCatalog()
	c.Table("things",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("ids", decl.Array(decl.UUID.Go(decl.GoTypeOf[string]()))),
	)
	md, err := c.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	files, err := codegen.Generate(md, codegen.Options{PackageName: "x"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if body := string(files[0].Content); !strings.Contains(body, `sqlkit.NewColumn[[]string](info, "ids")`) {
		t.Errorf("pinned element type not used for array:\n%s", body)
	}
}

func TestUnsupportedDialect(t *testing.T) {
	_, err := querysql.DialectByName("oracle")
	if err == nil || !strings.Contains(err.Error(), "unsupported dialect") {
		t.Errorf("DialectByName(oracle) error = %v, want unsupported dialect", err)
	}
}

// TestDataTypePinnedGoType: a DataType fixes the DB type and pins the Go type
// (DataType.Go), so the DB type still resolves per dialect for DDL while the
// pinned Go type flows through codegen — without a separate TypeMapper override.
func TestDataTypePinnedGoType(t *testing.T) {
	// Go implements meta.GoTyped.
	var _ meta.GoTyped = decl.Type("citext")

	citext := decl.Type("citext").Go(decl.GoTypeOf[string]())
	if gt, ok := citext.GoType("postgres"); !ok || gt.Name != "string" {
		t.Errorf("GoType(postgres) = %+v, %v; want string", gt, ok)
	}
	// The DB type is still fixed and resolves per dialect (verbatim for an
	// unregistered kind).
	if eng, _ := citext.Engine("postgres"); eng.Name != "citext" {
		t.Errorf("Engine(postgres) = %q, want citext", eng.Name)
	}

	c := decl.NewCatalog()
	c.Table("things",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("tag", citext),
		decl.Column("ref", decl.TypeFor(map[string]string{"postgres": "uuid", "mysql": "char"}).
			Go(decl.GoType(reflect.TypeOf(time.Time{})))),
	)
	md := buildCatalog(t, c)
	files, err := codegen.Generate(md, codegen.Options{PackageName: "x"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := string(files[0].Content)
	if !strings.Contains(body, `sqlkit.NewColumn[string](info, "tag")`) {
		t.Errorf("pinned Go type for citext not used:\n%s", body)
	}
	if !strings.Contains(body, `sqlkit.NewColumn[time.Time](info, "ref")`) {
		t.Errorf("reflect-derived pinned type not used:\n%s", body)
	}
}

// TestDataTypeGoForPerDialect: GoFor pins a distinct Go type per dialect.
func TestDataTypeGoForPerDialect(t *testing.T) {
	dt := decl.Type("json").
		GoFor("postgres", meta.GoType{ImportPath: "encoding/json", Name: "json.RawMessage"}).
		GoFor("mysql", meta.GoType{Name: "string"})
	if gt, ok := dt.GoType("postgres"); !ok || gt.Name != "json.RawMessage" {
		t.Errorf("GoType(postgres) = %+v, %v; want json.RawMessage", gt, ok)
	}
	if gt, ok := dt.GoType("mysql"); !ok || gt.Name != "string" {
		t.Errorf("GoType(mysql) = %+v, %v; want string", gt, ok)
	}
	// Pinning is copy-on-write: the base type is not mutated.
	if _, ok := decl.Type("json").GoType("postgres"); ok {
		t.Errorf("base Type(json) should have no pinned Go type")
	}
}

// TestRegisterTypeAndDialects: a custom kind registered for postgres/mysql
// resolves per dialect, and registering can extend a built-in kind with a new
// dialect.
func TestRegisterTypeAndDialects(t *testing.T) {
	decl.RegisterType("geometry", map[string]string{
		"postgres": "geometry", "mysql": "geometry",
	})
	geo := decl.Type("geometry")
	for dialect, want := range map[string]string{"postgres": "geometry", "mysql": "geometry"} {
		if eng, ok := geo.Engine(dialect); !ok || eng.Name != want {
			t.Errorf("geometry Engine(%s) = %q (ok=%v), want %q", dialect, eng.Name, ok, want)
		}
	}

	// Built-in BigInt resolves mysql from the default registration.
	if eng, ok := decl.BigInt.Engine("mysql"); !ok || eng.Name != "bigint" {
		t.Errorf("BigInt Engine(mysql) = %q (ok=%v), want bigint", eng.Name, ok)
	}
	// A dialect not registered for a kind is unavailable.
	if _, ok := geo.Engine("oracle"); ok {
		t.Errorf("geometry should be unavailable on an unregistered dialect")
	}
}

// TestSQLiteAndLibSQLBuiltinTypes covers the SQLite (and its libSQL fork)
// engine-type resolution of the built-in kinds: SQLite's affinity-based types
// collapse many kinds onto integer/real/text/blob, and libSQL resolves to the
// same names.
func TestSQLiteAndLibSQLBuiltinTypes(t *testing.T) {
	cases := []struct {
		typ  decl.DataType
		want string
	}{
		{decl.Bool, "boolean"},
		{decl.SmallInt, "integer"},
		{decl.Int, "integer"},
		{decl.BigInt, "integer"},
		{decl.Float, "real"},
		{decl.Double, "real"},
		{decl.Text, "text"},
		{decl.Bytes, "blob"},
		{decl.JSON, "text"},
		{decl.UUID, "text"},
		{decl.Date, "text"},
		{decl.Time, "text"},
		{decl.Timestamp, "datetime"},
		{decl.Decimal(10, 2), "numeric"},
	}
	for _, dialect := range []string{"sqlite", "libsql"} {
		for _, c := range cases {
			eng, ok := c.typ.Engine(dialect)
			if !ok || eng.Name != c.want {
				t.Errorf("Engine(%s) = %q (ok=%v), want %q", dialect, eng.Name, ok, c.want)
			}
		}
	}
}
