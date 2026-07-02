package meta_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// fixture builds the metadata with single-dialect types for the given dialect:
// the dataTypes map gives the engine name per logical type.
func fixture(dialect string, dataTypes map[string]string) meta.Metadata {
	typ := func(key string, length, precision, scale int) meta.Type {
		return meta.Of(dialect, dataTypes[key], length, precision, scale)
	}
	public := meta.Schema{Name: "public"}
	return meta.Metadata{
		Tables: []meta.Table{
			{
				Schema:  public,
				Name:    "users",
				Comment: "Registered blog authors",
				Columns: []meta.Column{
					{Name: "id", Type: typ("bigint", 0, 0, 0), PrimaryKey: true, HasDefault: true},
					{Name: "email", Type: typ("varchar", 255, 0, 0), Comment: "Unique login address"},
					{Name: "name", Type: typ("text", 0, 0, 0), Nullable: true},
					{Name: "balance", Type: typ("decimal", 0, 10, 2)},
					{Name: "created_at", Type: typ("timestamp", 0, 0, 0), HasDefault: true, DefaultExpr: sql.Raw("now()")},
				},
				Indexes: []meta.Index{
					{Name: "uq_users_email", Columns: []string{"email"}, Unique: true},
				},
				Checks: []meta.Check{
					{Name: "ck_users_1", Expression: sql.Raw("balance >= 0")},
				},
			},
			{
				Schema: public,
				Name:   "posts",
				Columns: []meta.Column{
					{Name: "id", Type: typ("bigint", 0, 0, 0), PrimaryKey: true, HasDefault: true},
					{Name: "user_id", Type: typ("bigint", 0, 0, 0)},
				},
				ForeignKeys: []meta.ForeignKey{
					{Name: "fk_posts_user_id", Columns: []string{"user_id"}, RefSchema: public, RefTable: "users", RefColumns: []string{"id"}},
				},
				Indexes: []meta.Index{
					{Name: "idx_posts_user_id", Columns: []string{"user_id"}},
				},
			},
		},
	}
}

func mustDialect(t *testing.T, name string) sql.Dialect {
	t.Helper()
	d, err := sql.DialectByName(name)
	if err != nil {
		t.Fatalf("DialectByName(%q) error = %v", name, err)
	}
	return d
}

func TestDDLPostgres(t *testing.T) {
	md := fixture("postgres", map[string]string{
		"bigint":    "int8",
		"varchar":   "varchar",
		"text":      "text",
		"decimal":   "numeric",
		"timestamp": "timestamptz",
	})
	got, err := md.CreateSQL(mustDialect(t, "postgres"))
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	want := `CREATE TABLE "public"."users" (
    "id" bigserial,
    "email" varchar(255) NOT NULL,
    "name" text,
    "balance" numeric(10, 2) NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "ck_users_1" CHECK (balance >= 0)
);

CREATE TABLE "public"."posts" (
    "id" bigserial,
    "user_id" bigint NOT NULL,
    PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");

CREATE INDEX "idx_posts_user_id" ON "public"."posts" ("user_id");

ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id");

COMMENT ON TABLE "public"."users" IS 'Registered blog authors';
COMMENT ON COLUMN "public"."users"."email" IS 'Unique login address';
`
	if got != want {
		t.Errorf("CreateSQL() =\n%s\nwant\n%s", got, want)
	}
}

func TestDDLMySQL(t *testing.T) {
	md := fixture("mysql", map[string]string{
		"bigint":    "bigint",
		"varchar":   "varchar",
		"text":      "text",
		"decimal":   "decimal",
		"timestamp": "datetime",
	})
	got, err := md.CreateSQL(mustDialect(t, "mysql"))
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	want := "CREATE TABLE `public`.`users` (\n" +
		"    `id` bigint NOT NULL AUTO_INCREMENT,\n" +
		"    `email` varchar(255) NOT NULL COMMENT 'Unique login address',\n" +
		"    `name` text,\n" +
		"    `balance` decimal(10, 2) NOT NULL,\n" +
		"    `created_at` datetime NOT NULL DEFAULT now(),\n" +
		"    PRIMARY KEY (`id`),\n" +
		"    CONSTRAINT `ck_users_1` CHECK (balance >= 0)\n" +
		") COMMENT='Registered blog authors';"
	if !strings.Contains(got, want) {
		t.Errorf("CreateSQL() missing MySQL table:\n%s\ngot:\n%s", want, got)
	}
}

func TestDDLUnqualified(t *testing.T) {
	md := meta.Metadata{
		Tables: []meta.Table{{
			Name: "items",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
			},
		}},
	}
	got, err := md.CreateSQL(mustDialect(t, "postgres"))
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if !strings.Contains(got, `CREATE TABLE "items" (`) {
		t.Errorf("unqualified table name expected:\n%s", got)
	}
	// The unnamed default schema has no CREATE SCHEMA to emit.
	if strings.Contains(got, "CREATE SCHEMA") {
		t.Errorf("default schema should not emit CREATE SCHEMA:\n%s", got)
	}
	if strings.Contains(md.DropSQL(mustDialect(t, "postgres")), "DROP SCHEMA") {
		t.Errorf("default schema should not emit DROP SCHEMA")
	}
}

// TestDDLSchemaDependency verifies CreateSQL declares each distinct schema
// marked for creation (once, in first-seen order) before the tables that depend
// on it, and DropSQL drops the schemas last, after the tables they hold.
func TestDDLSchemaDependency(t *testing.T) {
	intType := meta.Of("postgres", "int8", 0, 0, 0)
	auth := meta.Schema{Name: "auth", Create: true}
	billing := meta.Schema{Name: "billing", Create: true}
	md := meta.Metadata{
		Tables: []meta.Table{
			{Schema: auth, Name: "users", Columns: []meta.Column{{Name: "id", Type: intType, PrimaryKey: true}}},
			{Schema: billing, Name: "invoices", Columns: []meta.Column{{Name: "id", Type: intType, PrimaryKey: true}}},
			{Schema: auth, Name: "sessions", Columns: []meta.Column{{Name: "id", Type: intType, PrimaryKey: true}}},
		},
	}
	pg := mustDialect(t, "postgres")

	got, err := md.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	// Each schema declared once, before any table.
	if c := strings.Count(got, "CREATE SCHEMA"); c != 2 {
		t.Errorf("CREATE SCHEMA count = %d, want 2:\n%s", c, got)
	}
	for _, s := range []string{`CREATE SCHEMA IF NOT EXISTS "auth";`, `CREATE SCHEMA IF NOT EXISTS "billing";`} {
		idx := strings.Index(got, s)
		if idx < 0 {
			t.Fatalf("missing %q:\n%s", s, got)
		}
		if idx > strings.Index(got, "CREATE TABLE") {
			t.Errorf("%q must precede the first CREATE TABLE:\n%s", s, got)
		}
	}
	// First-seen order: auth before billing.
	if strings.Index(got, `"auth"`) > strings.Index(got, `"billing"`) {
		t.Errorf("schemas not in first-seen order:\n%s", got)
	}

	drop := md.DropSQL(pg)
	if last := strings.LastIndex(drop, "DROP TABLE"); last > strings.Index(drop, "DROP SCHEMA") {
		t.Errorf("DROP SCHEMA must follow all DROP TABLE statements:\n%s", drop)
	}
}

// TestDDLSchemaCreateGating verifies a named schema is created only when marked
// Create — a schema assumed to already exist (Create=false) is left alone — and
// that Schemas merges the flag when a name appears both ways.
func TestDDLSchemaCreateGating(t *testing.T) {
	intType := meta.Of("postgres", "int8", 0, 0, 0)
	pg := mustDialect(t, "postgres")

	// A named but unmanaged schema gets no CREATE/DROP SCHEMA.
	unmanaged := meta.Metadata{Tables: []meta.Table{
		{Schema: meta.Schema{Name: "public"}, Name: "t", Columns: []meta.Column{{Name: "id", Type: intType, PrimaryKey: true}}},
	}}
	got, err := unmanaged.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if strings.Contains(got, "CREATE SCHEMA") {
		t.Errorf("unmanaged schema should not emit CREATE SCHEMA:\n%s", got)
	}
	if strings.Contains(unmanaged.DropSQL(pg), "DROP SCHEMA") {
		t.Errorf("unmanaged schema should not emit DROP SCHEMA")
	}

	// Mixed Create flags on the same schema name: managed if any table asks.
	mixed := meta.Metadata{Tables: []meta.Table{
		{Schema: meta.Schema{Name: "audit"}, Name: "a", Columns: []meta.Column{{Name: "id", Type: intType, PrimaryKey: true}}},
		{Schema: meta.Schema{Name: "audit", Create: true}, Name: "b", Columns: []meta.Column{{Name: "id", Type: intType, PrimaryKey: true}}},
	}}
	if schemas := mixed.Schemas(); len(schemas) != 1 || !schemas[0].Create {
		t.Errorf("Schemas() = %+v, want one schema with Create=true", schemas)
	}
	got, err = mixed.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if c := strings.Count(got, "CREATE SCHEMA"); c != 1 {
		t.Errorf("CREATE SCHEMA count = %d, want 1:\n%s", c, got)
	}
}

// TestDDLManagedSchemaCreatedAndDropped pins that a ManagedSchema with no table
// of its own (one hosting only an enum or sequence) is still folded into
// Schemas() and so created by CreateSQL and dropped by DropSQL.
func TestDDLManagedSchemaCreatedAndDropped(t *testing.T) {
	pg := mustDialect(t, "postgres")
	md := meta.Metadata{
		Enums:          []meta.Enum{{Schema: "audit", Name: "mood", Values: []string{"happy", "sad"}}},
		ManagedSchemas: []meta.Schema{{Name: "audit", Create: true}},
	}
	if schemas := md.Schemas(); len(schemas) != 1 || schemas[0].Name != "audit" || !schemas[0].Create {
		t.Fatalf("Schemas() = %+v, want one managed audit schema", md.Schemas())
	}
	got, err := md.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if !strings.Contains(got, `CREATE SCHEMA IF NOT EXISTS "audit";`) {
		t.Errorf("expected CREATE SCHEMA for the enum-only managed schema:\n%s", got)
	}
	if !strings.Contains(md.DropSQL(pg), `DROP SCHEMA IF EXISTS "audit"`) {
		t.Errorf("expected DROP SCHEMA for the managed schema:\n%s", md.DropSQL(pg))
	}
}

// TestDDLExtensionSchemaOrder covers an extension installed into a schema
// this same Metadata also manages (Extension.Schema + a matching
// ManagedSchemas/Create entry): CREATE SCHEMA must precede CREATE EXTENSION
// ... WITH SCHEMA, or a real engine rejects the extension (the schema doesn't
// exist yet), and DROP EXTENSION must precede DROP SCHEMA, or dropping the
// still-non-empty schema fails.
func TestDDLExtensionSchemaOrder(t *testing.T) {
	pg := mustDialect(t, "postgres")
	md := meta.Metadata{
		Extensions:     []meta.Extension{{Name: "pgcrypto", Schema: "extensions"}},
		ManagedSchemas: []meta.Schema{{Name: "extensions", Create: true}},
	}
	got, err := md.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	schemaIdx := strings.Index(got, `CREATE SCHEMA IF NOT EXISTS "extensions"`)
	extIdx := strings.Index(got, `CREATE EXTENSION IF NOT EXISTS "pgcrypto"`)
	if schemaIdx < 0 || extIdx < 0 {
		t.Fatalf("missing CREATE SCHEMA or CREATE EXTENSION:\n%s", got)
	}
	if schemaIdx > extIdx {
		t.Errorf("CREATE SCHEMA must precede CREATE EXTENSION ... WITH SCHEMA:\n%s", got)
	}

	drop := md.DropSQL(pg)
	dropSchemaIdx := strings.Index(drop, `DROP SCHEMA IF EXISTS "extensions"`)
	dropExtIdx := strings.Index(drop, `DROP EXTENSION IF EXISTS "pgcrypto"`)
	if dropSchemaIdx < 0 || dropExtIdx < 0 {
		t.Fatalf("missing DROP SCHEMA or DROP EXTENSION:\n%s", drop)
	}
	if dropExtIdx > dropSchemaIdx {
		t.Errorf("DROP EXTENSION must precede DROP SCHEMA:\n%s", drop)
	}
}

func TestDDLUnsupportedDialect(t *testing.T) {
	if _, err := sql.DialectByName("oracle"); err == nil {
		t.Error("DialectByName(oracle): expected error")
	}
}

func TestDDLLiteralEscaping(t *testing.T) {
	md := meta.Metadata{
		Tables: []meta.Table{{
			Name:    "items",
			Comment: "it's quoted",
			Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}},
		}},
	}
	got, err := md.CreateSQL(mustDialect(t, "postgres"))
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if !strings.Contains(got, `'it''s quoted'`) {
		t.Errorf("single quote not escaped:\n%s", got)
	}
}

// TestDDLDottedNameQuotedAsOne guards against splitting an identifier that
// itself contains a literal dot: it must be quoted as a single name, not as a
// dot-qualified reference.
func TestDDLDottedNameQuotedAsOne(t *testing.T) {
	md := meta.Metadata{
		Tables: []meta.Table{{
			Name: "v1.users",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
			},
		}},
	}
	got, err := md.CreateSQL(mustDialect(t, "postgres"))
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	if !strings.Contains(got, `CREATE TABLE "v1.users" (`) {
		t.Errorf("dotted table name should be one quoted identifier:\n%s", got)
	}
}

// TestTableCreateTable exercises per-table DDL directly.
func TestTableCreateTable(t *testing.T) {
	md := fixture("postgres", map[string]string{
		"bigint":    "int8",
		"varchar":   "varchar",
		"text":      "text",
		"decimal":   "numeric",
		"timestamp": "timestamptz",
	})
	got, err := md.Tables[1].CreateSQL(mustDialect(t, "postgres"))
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	want := `CREATE TABLE "public"."posts" (
    "id" bigserial,
    "user_id" bigint NOT NULL,
    PRIMARY KEY ("id")
);`
	if got != want {
		t.Errorf("CreateSQL() =\n%s\nwant\n%s", got, want)
	}
}

// TestIndexCreateIndex exercises per-index DDL directly.
func TestIndexCreateIndex(t *testing.T) {
	md := fixture("postgres", map[string]string{"varchar": "varchar", "bigint": "int8", "text": "text", "decimal": "numeric", "timestamp": "timestamptz"})
	users := md.Tables[0]
	got := users.Indexes[0].CreateSQL(users, mustDialect(t, "postgres"))
	want := `CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");`
	if got != want {
		t.Errorf("CreateSQL() = %q, want %q", got, want)
	}
}

func TestDropDDLPostgres(t *testing.T) {
	md := fixture("postgres", map[string]string{"bigint": "int8", "varchar": "varchar", "text": "text", "decimal": "numeric", "timestamp": "timestamptz"})
	got := md.DropSQL(mustDialect(t, "postgres"))
	want := `ALTER TABLE "public"."posts" DROP CONSTRAINT IF EXISTS "fk_posts_user_id";

DROP TABLE IF EXISTS "public"."users";

DROP TABLE IF EXISTS "public"."posts";
`
	if got != want {
		t.Errorf("DropSQL() =\n%s\nwant\n%s", got, want)
	}
}

func TestDropDDLMySQL(t *testing.T) {
	md := fixture("mysql", map[string]string{"bigint": "bigint", "varchar": "varchar", "text": "text", "decimal": "decimal", "timestamp": "datetime"})
	got := md.DropSQL(mustDialect(t, "mysql"))
	want := "ALTER TABLE `public`.`posts` DROP FOREIGN KEY `fk_posts_user_id`;\n\n" +
		"DROP TABLE IF EXISTS `public`.`users`;\n\n" +
		"DROP TABLE IF EXISTS `public`.`posts`;\n"
	if got != want {
		t.Errorf("DropSQL() =\n%s\nwant\n%s", got, want)
	}
}

// TestDropPerElement exercises per-table and per-index DROP directly, including
// the dialect-specific DROP INDEX syntax.
func TestDropPerElement(t *testing.T) {
	pgMd := fixture("postgres", map[string]string{"bigint": "int8", "varchar": "varchar", "text": "text", "decimal": "numeric", "timestamp": "timestamptz"})
	pg := mustDialect(t, "postgres")
	users := pgMd.Tables[0]
	if got, want := users.DropSQL(pg), `DROP TABLE IF EXISTS "public"."users";`; got != want {
		t.Errorf("DropSQL() = %q, want %q", got, want)
	}
	if got, want := users.Indexes[0].DropSQL(users, pg), `DROP INDEX IF EXISTS "public"."uq_users_email";`; got != want {
		t.Errorf("DropSQL(postgres) = %q, want %q", got, want)
	}

	myMd := fixture("mysql", map[string]string{"bigint": "bigint", "varchar": "varchar", "text": "text", "decimal": "decimal", "timestamp": "datetime"})
	my := mustDialect(t, "mysql")
	myUsers := myMd.Tables[0]
	if got, want := myUsers.Indexes[0].DropSQL(myUsers, my), "DROP INDEX `uq_users_email` ON `public`.`users`;"; got != want {
		t.Errorf("DropSQL(mysql) = %q, want %q", got, want)
	}
}
