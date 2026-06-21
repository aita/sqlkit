package decl_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/decl"
	querysql "github.com/aita/sqlkit/sql"
)

// These tests pin the DDL a decl catalog generates: they declare a schema with
// the decl vocabulary and assert the exact SQL CreateSQL emits, so a change to
// either the decl assembly or the meta DDL renderer that alters the generated
// schema is caught here. They complement TestDDL (a substring smoke test) and
// the meta package's golden tests (which start from a hand-built meta.Metadata)
// by exercising the whole definition -> meta -> DDL path.

// TestDDLPortableTypesPostgres declares a column of every portable type and
// pins how each resolves in the PostgreSQL CREATE TABLE. The type columns are
// Nullable so each line is just `"name" type,` — isolating the rendered type.
func TestDDLPortableTypesPostgres(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Table("specimens",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("c_bool", decl.Bool, decl.Nullable()),
		decl.Column("c_smallint", decl.SmallInt, decl.Nullable()),
		decl.Column("c_int", decl.Int, decl.Nullable()),
		decl.Column("c_bigint", decl.BigInt, decl.Nullable()),
		decl.Column("c_float", decl.Float, decl.Nullable()),
		decl.Column("c_double", decl.Double, decl.Nullable()),
		decl.Column("c_text", decl.Text, decl.Nullable()),
		decl.Column("c_bytes", decl.Bytes, decl.Nullable()),
		decl.Column("c_json", decl.JSON, decl.Nullable()),
		decl.Column("c_uuid", decl.UUID, decl.Nullable()),
		decl.Column("c_date", decl.Date, decl.Nullable()),
		decl.Column("c_time", decl.Time, decl.Nullable()),
		decl.Column("c_timestamp", decl.Timestamp, decl.Nullable()),
		decl.Column("c_varchar", decl.VarChar(255), decl.Nullable()),
		decl.Column("c_char", decl.Char(10), decl.Nullable()),
		decl.Column("c_decimal", decl.Decimal(10, 2), decl.Nullable()),
	)
	ddl, err := buildCatalog(t, catalog).CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	for _, want := range []string{
		`"id" bigserial,`, // integer PK with bare Default -> serial
		`"c_bool" boolean,`,
		`"c_smallint" smallint,`,
		`"c_int" integer,`,
		`"c_bigint" bigint,`,
		`"c_float" real,`,
		`"c_double" double precision,`,
		`"c_text" text,`,
		`"c_bytes" bytea,`,
		`"c_json" jsonb,`,
		`"c_uuid" uuid,`,
		`"c_date" date,`,
		`"c_time" time,`,
		`"c_timestamp" timestamptz,`,
		`"c_varchar" varchar(255),`,
		`"c_char" char(10),`,
		`"c_decimal" numeric(10, 2),`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("Postgres DDL missing %q\n%s", want, ddl)
		}
	}
}

// TestDDLPortableTypesMySQL pins the same portable types resolving to their
// MySQL engine names (backtick-quoted identifiers).
func TestDDLPortableTypesMySQL(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Table("specimens",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("c_bool", decl.Bool, decl.Nullable()),
		decl.Column("c_smallint", decl.SmallInt, decl.Nullable()),
		decl.Column("c_int", decl.Int, decl.Nullable()),
		decl.Column("c_bigint", decl.BigInt, decl.Nullable()),
		decl.Column("c_float", decl.Float, decl.Nullable()),
		decl.Column("c_double", decl.Double, decl.Nullable()),
		decl.Column("c_text", decl.Text, decl.Nullable()),
		decl.Column("c_bytes", decl.Bytes, decl.Nullable()),
		decl.Column("c_json", decl.JSON, decl.Nullable()),
		decl.Column("c_uuid", decl.UUID, decl.Nullable()),
		decl.Column("c_date", decl.Date, decl.Nullable()),
		decl.Column("c_time", decl.Time, decl.Nullable()),
		decl.Column("c_timestamp", decl.Timestamp, decl.Nullable()),
		decl.Column("c_varchar", decl.VarChar(255), decl.Nullable()),
		decl.Column("c_char", decl.Char(10), decl.Nullable()),
		decl.Column("c_decimal", decl.Decimal(10, 2), decl.Nullable()),
	)
	ddl, err := buildCatalog(t, catalog).CreateSQL(querysql.MySQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	for _, want := range []string{
		"`id` bigint NOT NULL AUTO_INCREMENT,",
		"`c_bool` boolean,",
		"`c_smallint` smallint,",
		"`c_int` int,",
		"`c_bigint` bigint,",
		"`c_float` float,",
		"`c_double` double,",
		"`c_text` text,",
		"`c_bytes` blob,",
		"`c_json` json,",
		"`c_uuid` char(36),",
		"`c_date` date,",
		"`c_time` time,",
		"`c_timestamp` datetime,",
		"`c_varchar` varchar(255),",
		"`c_char` char(10),",
		"`c_decimal` decimal(10, 2),",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("MySQL DDL missing %q\n%s", want, ddl)
		}
	}
}

// TestDDLCompositePrimaryKey: marking several columns PrimaryKey renders one
// composite PRIMARY KEY clause listing them in declaration order (and they are
// NOT NULL, never serial, since the key spans more than one column).
func TestDDLCompositePrimaryKey(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Table("memberships",
		decl.Column("user_id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("team_id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("role", decl.Text),
	)
	ddl, err := buildCatalog(t, catalog).CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	for _, want := range []string{
		`"user_id" bigint NOT NULL,`,
		`"team_id" bigint NOT NULL,`,
		`PRIMARY KEY ("user_id", "team_id")`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("composite PK DDL missing %q\n%s", want, ddl)
		}
	}
}

// TestDDLForeignKeyReferentialActions: OnDelete/OnUpdate on a References column
// render as ON DELETE / ON UPDATE clauses on the ALTER TABLE that adds the key.
func TestDDLForeignKeyReferentialActions(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	catalog.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt,
			decl.References("users", "id", decl.OnDelete(decl.Cascade), decl.OnUpdate(decl.Restrict))),
	)
	ddl, err := buildCatalog(t, catalog).CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	want := `ALTER TABLE "public"."posts" ADD CONSTRAINT "fk_posts_user_id" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE RESTRICT;`
	if !strings.Contains(ddl, want) {
		t.Errorf("FK action DDL missing\n%s\ngot\n%s", want, ddl)
	}
}

// TestDDLCompositeForeignKey: a table-level ForeignKey over several columns
// renders one constraint listing the local and referenced columns in order.
func TestDDLCompositeForeignKey(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Table("orders",
		decl.Column("region", decl.Text, decl.PrimaryKey()),
		decl.Column("number", decl.BigInt, decl.PrimaryKey()),
	)
	catalog.Table("order_lines",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("order_region", decl.Text),
		decl.Column("order_number", decl.BigInt),
		decl.ForeignKey([]string{"order_region", "order_number"}, "orders", []string{"region", "number"}),
	)
	ddl, err := buildCatalog(t, catalog).CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	want := `ALTER TABLE "public"."order_lines" ADD CONSTRAINT "fk_order_lines_order_region_order_number" FOREIGN KEY ("order_region", "order_number") REFERENCES "public"."orders" ("region", "number");`
	if !strings.Contains(ddl, want) {
		t.Errorf("composite FK DDL missing\n%s\ngot\n%s", want, ddl)
	}
}

// TestDDLIndexesPostgres: Index, UniqueIndex, and the column-level Unique each
// render their CREATE [UNIQUE] INDEX with the derived constraint name.
func TestDDLIndexesPostgres(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Table("products",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("sku", decl.VarChar(64), decl.Unique()),
		decl.Column("vendor", decl.Text),
		decl.Column("name", decl.Text),
		decl.Index("name"),
		decl.UniqueIndex("vendor", "name"),
	)
	ddl, err := buildCatalog(t, catalog).CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	for _, want := range []string{
		`CREATE UNIQUE INDEX "uq_products_sku" ON "public"."products" ("sku");`,
		`CREATE INDEX "idx_products_name" ON "public"."products" ("name");`,
		`CREATE UNIQUE INDEX "uq_products_vendor_name" ON "public"."products" ("vendor", "name");`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("index DDL missing %q\n%s", want, ddl)
		}
	}
}

// TestDDLSequenceAllOptions pins the CREATE SEQUENCE a fully-configured decl
// Sequence emits, with the options in the renderer's fixed order.
func TestDDLSequenceAllOptions(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Sequence("order_no",
		decl.SequenceIncrement(2),
		decl.SequenceMinValue(10),
		decl.SequenceMaxValue(1000),
		decl.SequenceStart(100),
		decl.SequenceCache(5),
		decl.SequenceCycle(true),
	)
	catalog.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	ddl, err := buildCatalog(t, catalog).CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	want := `CREATE SEQUENCE "public"."order_no" INCREMENT BY 2 MINVALUE 10 MAXVALUE 1000 START WITH 100 CACHE 5 CYCLE;`
	if !strings.Contains(ddl, want) {
		t.Errorf("sequence DDL missing\n%s\ngot\n%s", want, ddl)
	}
}

// TestDDLFullTablePostgres is a golden test of the whole definition -> DDL path
// for one rich table: it pins the exact CreateSQL, including column order, the
// serial primary key, NOT NULL defaulting, inline DEFAULT, the composite-free
// PRIMARY KEY clause, the CHECK constraint, the unique index emitted after the
// table, and the trailing COMMENT ON statements (table then column).
func TestDDLFullTablePostgres(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Table("users",
		decl.TableComment("Application users"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.VarChar(255), decl.Unique(), decl.Comment("Login address")),
		decl.Column("name", decl.Text, decl.Nullable()),
		decl.Column("created_at", decl.Timestamp, decl.Default(querysql.Raw("now()"))),
		decl.Check(querysql.Raw("char_length(email) > 3")),
	)
	got, err := buildCatalog(t, catalog).CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	want := `CREATE TABLE "public"."users" (
    "id" bigserial,
    "email" varchar(255) NOT NULL,
    "name" text,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "ck_users_1" CHECK (char_length(email) > 3)
);

CREATE UNIQUE INDEX "uq_users_email" ON "public"."users" ("email");

COMMENT ON TABLE "public"."users" IS 'Application users';
COMMENT ON COLUMN "public"."users"."email" IS 'Login address';
`
	if got != want {
		t.Errorf("CreateSQL() =\n%s\nwant\n%s", got, want)
	}
}
