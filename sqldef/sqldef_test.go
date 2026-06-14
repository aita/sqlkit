package sqldef_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/meta"
	querysql "github.com/aita/sqlkit/sql"
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
		sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(querysql.Raw("now()"))),
	)
	public.Table("posts",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id")),
		sqldef.Column("title", sqldef.Text),
		sqldef.Column("body", sqldef.Text, sqldef.Nullable()),
		sqldef.Column("published", sqldef.Bool, sqldef.Default(querysql.Raw("false"))),
	)
	return catalog
}

func buildCatalog(t *testing.T, c *sqldef.Catalog) meta.Metadata {
	t.Helper()
	md, err := c.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return md
}

func TestBuild(t *testing.T) {
	got := buildCatalog(t, blogCatalog())

	// Column types stay portable (the sqldef constructors are meta.Type values),
	// resolved to an engine name only by the generators.
	want := meta.Metadata{
		Tables: []meta.Table{
			{
				Schema: meta.Schema{Name: "public"},
				Name:   "users",
				Columns: []meta.Column{
					{Name: "id", Type: sqldef.BigInt, PrimaryKey: true, HasDefault: true},
					{Name: "email", Type: sqldef.VarChar(255)},
					{Name: "name", Type: sqldef.Text, Nullable: true},
					{Name: "created_at", Type: sqldef.Timestamp, HasDefault: true, DefaultExpr: querysql.Raw("now()")},
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
					{Name: "published", Type: sqldef.Bool, HasDefault: true, DefaultExpr: querysql.Raw("false")},
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
	catalog := sqldef.NewCatalog()
	// A plain schema is assumed to exist; a Create schema is managed.
	catalog.Schema("public").Table("users",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
	)
	catalog.Schema("audit", sqldef.Create()).Table("log",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
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

// TestBuildColumnDropsTableLevelConstructs checks that BuildColumn (used by
// ALTER TABLE ADD COLUMN) accepts a column carrying References/Unique — those
// are table-level constructs and are dropped, not validated against a catalog
// that has no such table, which is the common "add the FK column, then
// CreateForeignKey" pattern.
func TestBuildColumnDropsTableLevelConstructs(t *testing.T) {
	users := meta.Table{Name: "users", Columns: []meta.Column{{Name: "id"}}}
	col, err := sqldef.BuildColumn(
		sqldef.Column("user_id", sqldef.BigInt, sqldef.References(users, "id"), sqldef.Unique()),
	)
	if err != nil {
		t.Fatalf("BuildColumn with References/Unique: %v", err)
	}
	if col.Name != "user_id" {
		t.Errorf("column name = %q, want user_id", col.Name)
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
		sqldef.Check(querysql.Raw("price > 0")),
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
	audit := []sqldef.TableElement{
		sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(querysql.Raw("now()"))),
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

// TestBlankDefaultIsAbsent: a blank Default expression (an empty sql.Raw) is
// normalized to absent, so an integer primary key still renders as serial
// rather than emitting an invalid empty DEFAULT clause.
func TestBlankDefaultIsAbsent(t *testing.T) {
	catalog := sqldef.NewCatalog()
	catalog.Table("items",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default(querysql.Raw(""))),
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
				c.Table("users", sqldef.Column("id", sqldef.BigInt), sqldef.Check(querysql.Raw("  ")))
				return c
			},
			want: "Check requires an expression",
		},
		{
			name: "default with two expressions",
			catalog: func() *sqldef.Catalog {
				c := sqldef.NewCatalog()
				c.Table("users", sqldef.Column("id", sqldef.BigInt, sqldef.Default(querysql.Raw("a"), querysql.Raw("b"))))
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
	c.Table("users", sqldef.Column("tags", sqldef.TypeFor(map[string]string{"postgres": "citext"})))
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
// "elem[]"), is unavailable on MySQL/SQLite, and codegen maps it to a Go slice.
func TestArrayColumns(t *testing.T) {
	tags := sqldef.Array(sqldef.Text)
	eng, ok := tags.Engine("postgres")
	if !ok || eng.Name != "text" || !eng.Array {
		t.Errorf("Array(Text).Engine(postgres) = %+v, ok=%v; want {Name:text, Array:true}", eng, ok)
	}
	if _, ok := tags.Engine("mysql"); ok {
		t.Error("Array(Text).Engine(mysql) should be unavailable")
	}
	if _, ok := tags.Engine("sqlite"); ok {
		t.Error("Array(Text).Engine(sqlite) should be unavailable")
	}

	c := sqldef.NewCatalog()
	c.Table("docs",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("tags", sqldef.Array(sqldef.Text)),
		sqldef.Column("scores", sqldef.Array(sqldef.Int), sqldef.Nullable()),
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
	c := sqldef.NewCatalog()
	c.Table("things",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("ids", sqldef.Array(sqldef.UUID.Go(sqldef.GoTypeOf[string]()))),
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
	var _ meta.GoTyped = sqldef.Type("citext")

	citext := sqldef.Type("citext").Go(sqldef.GoTypeOf[string]())
	if gt, ok := citext.GoType("postgres"); !ok || gt.Name != "string" {
		t.Errorf("GoType(postgres) = %+v, %v; want string", gt, ok)
	}
	// The DB type is still fixed and resolves per dialect (verbatim for an
	// unregistered kind).
	if eng, _ := citext.Engine("postgres"); eng.Name != "citext" {
		t.Errorf("Engine(postgres) = %q, want citext", eng.Name)
	}

	c := sqldef.NewCatalog()
	c.Table("things",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("tag", citext),
		sqldef.Column("ref", sqldef.TypeFor(map[string]string{"postgres": "uuid", "mysql": "char"}).
			Go(sqldef.GoType(reflect.TypeOf(time.Time{})))),
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
	dt := sqldef.Type("json").
		GoFor("postgres", meta.GoType{ImportPath: "encoding/json", Name: "json.RawMessage"}).
		GoFor("mysql", meta.GoType{Name: "string"})
	if gt, ok := dt.GoType("postgres"); !ok || gt.Name != "json.RawMessage" {
		t.Errorf("GoType(postgres) = %+v, %v; want json.RawMessage", gt, ok)
	}
	if gt, ok := dt.GoType("mysql"); !ok || gt.Name != "string" {
		t.Errorf("GoType(mysql) = %+v, %v; want string", gt, ok)
	}
	// Pinning is copy-on-write: the base type is not mutated.
	if _, ok := sqldef.Type("json").GoType("postgres"); ok {
		t.Errorf("base Type(json) should have no pinned Go type")
	}
}

// TestRegisterTypeAndDialects: a custom kind registered for postgres/mysql/sqlite
// resolves per dialect, and registering can extend a built-in kind with a new
// dialect.
func TestRegisterTypeAndDialects(t *testing.T) {
	sqldef.RegisterType("geometry", map[string]string{
		"postgres": "geometry", "mysql": "geometry", "sqlite": "blob",
	})
	geo := sqldef.Type("geometry")
	for dialect, want := range map[string]string{"postgres": "geometry", "mysql": "geometry", "sqlite": "blob"} {
		if eng, ok := geo.Engine(dialect); !ok || eng.Name != want {
			t.Errorf("geometry Engine(%s) = %q (ok=%v), want %q", dialect, eng.Name, ok, want)
		}
	}

	// Built-in BigInt resolves sqlite from the default registration.
	if eng, ok := sqldef.BigInt.Engine("sqlite"); !ok || eng.Name != "integer" {
		t.Errorf("BigInt Engine(sqlite) = %q (ok=%v), want integer", eng.Name, ok)
	}
	// A dialect not registered for a kind is unavailable.
	if _, ok := geo.Engine("oracle"); ok {
		t.Errorf("geometry should be unavailable on an unregistered dialect")
	}
}

// TestSQLiteEndToEnd: a catalog of built-in types generates SQLite bindings.
func TestSQLiteEndToEnd(t *testing.T) {
	c := sqldef.NewCatalog()
	c.Table("items",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey()),
		sqldef.Column("label", sqldef.Text),
		sqldef.Column("active", sqldef.Bool),
	)
	md := buildCatalog(t, c)
	files, err := codegen.Generate(md, codegen.Options{PackageName: "x", Dialect: "sqlite"})
	if err != nil {
		t.Fatalf("Generate(sqlite) error = %v", err)
	}
	body := string(files[0].Content)
	for _, want := range []string{
		`sqlkit.NewColumn[int64](info, "id")`,
		`sqlkit.NewColumn[string](info, "label")`,
		`sqlkit.NewColumn[bool](info, "active")`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SQLite end-to-end missing %q:\n%s", want, body)
		}
	}
}
