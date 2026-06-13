package ddl

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

// fixture builds the catalog with portable types for the given dialect: the
// dataTypes map gives the engine name per logical type, set on the dialect's
// side of meta.Type.
func fixture(dialect string, dataTypes map[string]string) meta.Meta {
	typ := func(key string, length, precision, scale int) meta.Type {
		t := meta.Type{Length: length, Precision: precision, Scale: scale}
		if dialect == "mysql" {
			t.MySQL = dataTypes[key]
		} else {
			t.Postgres = dataTypes[key]
		}
		return t
	}
	public := meta.Schema{Name: "public"}
	return meta.Meta{
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
					{Name: "created_at", Type: typ("timestamp", 0, 0, 0), HasDefault: true, DefaultExpr: "now()"},
				},
				Indexes: []meta.Index{
					{Name: "uq_users_email", Columns: []string{"email"}, Unique: true},
				},
				Checks: []meta.Check{
					{Name: "ck_users_1", Expression: "balance >= 0"},
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

func TestGeneratePostgres(t *testing.T) {
	catalog := fixture("postgres", map[string]string{
		"bigint":    "int8",
		"varchar":   "varchar",
		"text":      "text",
		"decimal":   "numeric",
		"timestamp": "timestamptz",
	})
	got, err := Generate(catalog, "postgres")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
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
		t.Errorf("Generate() =\n%s\nwant\n%s", got, want)
	}
}

func TestGenerateMySQL(t *testing.T) {
	catalog := fixture("mysql", map[string]string{
		"bigint":    "bigint",
		"varchar":   "varchar",
		"text":      "text",
		"decimal":   "decimal",
		"timestamp": "datetime",
	})
	got, err := Generate(catalog, "mysql")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
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
		t.Errorf("Generate() missing MySQL table:\n%s\ngot:\n%s", want, got)
	}
}

func TestGenerateUnqualified(t *testing.T) {
	catalog := meta.Meta{
		Tables: []meta.Table{{
			Name: "items",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Type{Postgres: "int8"}, PrimaryKey: true},
			},
		}},
	}
	got, err := Generate(catalog, "postgres")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !strings.Contains(got, `CREATE TABLE "items" (`) {
		t.Errorf("unqualified table name expected:\n%s", got)
	}
}

func TestGenerateUnsupportedDialect(t *testing.T) {
	if _, err := Generate(meta.Meta{}, "oracle"); err == nil {
		t.Error("Generate(oracle): expected error")
	}
}

func TestLiteralEscaping(t *testing.T) {
	catalog := meta.Meta{
		Tables: []meta.Table{{
			Name:    "items",
			Comment: "it's quoted",
			Columns: []meta.Column{{Name: "id", Type: meta.Type{Postgres: "int8"}, PrimaryKey: true}},
		}},
	}
	got, err := Generate(catalog, "postgres")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !strings.Contains(got, `'it''s quoted'`) {
		t.Errorf("single quote not escaped:\n%s", got)
	}
}
