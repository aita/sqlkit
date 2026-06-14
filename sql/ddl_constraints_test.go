package sql_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestCreateTableInlineConstraints(t *testing.T) {
	stmt := sql.CreateTable(sql.Table{Name: "posts"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.EngineType{Name: "int8"}, Identity: sql.IdentityAlways, NotNull: true}).
		Column(sql.ColumnDef{Name: "slug", Type: sql.EngineType{Name: "text"}, NotNull: true, Unique: true}).
		Column(sql.ColumnDef{Name: "author_id", Type: sql.EngineType{Name: "int8"}, NotNull: true,
			References: &sql.ColumnReference{Table: "users", Column: "id", OnDelete: "CASCADE"}}).
		Column(sql.ColumnDef{Name: "search", Type: sql.EngineType{Name: "text"},
			Generated: &sql.GeneratedColumn{Expression: sql.Raw("lower(slug)"), Stored: true}}).
		PrimaryKey("id")

	got := mustDDL(t, stmt, "postgres")
	want := `CREATE TABLE "posts" (
    "id" bigint GENERATED ALWAYS AS IDENTITY NOT NULL,
    "slug" text NOT NULL UNIQUE,
    "author_id" bigint NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,
    "search" text GENERATED ALWAYS AS (lower(slug)) STORED,
    PRIMARY KEY ("id")
);`
	if got != want {
		t.Errorf("CreateTable(inline) =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableTableLevelUniqueAndForeignKey(t *testing.T) {
	stmt := sql.CreateTable(sql.Table{Schema: "public", Name: "memberships"}).
		Column(sql.ColumnDef{Name: "user_id", Type: sql.EngineType{Name: "int8"}, NotNull: true}).
		Column(sql.ColumnDef{Name: "team_id", Type: sql.EngineType{Name: "int8"}, NotNull: true}).
		Unique("uq_membership", "user_id", "team_id").
		ForeignKey(sql.ForeignKeyConstraint{
			Name: "fk_user", Columns: []string{"user_id"},
			RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}, OnDelete: "CASCADE",
		})

	got := mustDDL(t, stmt, "postgres")
	want := `CREATE TABLE "public"."memberships" (
    "user_id" bigint NOT NULL,
    "team_id" bigint NOT NULL,
    CONSTRAINT "uq_membership" UNIQUE ("user_id", "team_id"),
    CONSTRAINT "fk_user" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON DELETE CASCADE
);`
	if got != want {
		t.Errorf("CreateTable(table-level) =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateTableUnnamedUnique(t *testing.T) {
	stmt := sql.CreateTable(sql.Table{Name: "t"}).
		Column(sql.ColumnDef{Name: "a", Type: sql.EngineType{Name: "int8"}}).
		Unique("", "a")
	got := mustDDL(t, stmt, "postgres")
	if !strings.Contains(got, `    UNIQUE ("a")`) {
		t.Errorf("expected an unnamed UNIQUE (\"a\"), got:\n%s", got)
	}
}

func TestIdentityRejectedOnMySQL(t *testing.T) {
	stmt := sql.CreateTable(sql.Table{Name: "t"}).
		Column(sql.ColumnDef{Name: "id", Type: sql.EngineType{Name: "bigint"}, Identity: sql.IdentityAlways})
	if _, _, err := stmt.ToSQL(mustDialect(t, "mysql")); err == nil || !strings.Contains(err.Error(), "IDENTITY") {
		t.Fatalf("ToSQL(mysql) error = %v, want IDENTITY rejection", err)
	}
}

func TestAlterTableAddUnique(t *testing.T) {
	stmt := sql.AlterTable(sql.Table{Name: "users"}).AddUnique("uq_email", "email")
	got := mustDDL(t, stmt, "postgres")
	if want := `ALTER TABLE "users" ADD CONSTRAINT "uq_email" UNIQUE ("email");`; got != want {
		t.Errorf("AddUnique() = %q, want %q", got, want)
	}
}

func TestGeneratedColumnMySQL(t *testing.T) {
	stmt := sql.CreateTable(sql.Table{Name: "t"}).
		Column(sql.ColumnDef{Name: "a", Type: sql.EngineType{Name: "int"}, NotNull: true}).
		Column(sql.ColumnDef{Name: "b", Type: sql.EngineType{Name: "int"},
			Generated: &sql.GeneratedColumn{Expression: sql.Raw("a * 2")}})
	got := mustDDL(t, stmt, "mysql")
	if !strings.Contains(got, "`b` int GENERATED ALWAYS AS (a * 2) VIRTUAL") {
		t.Errorf("expected a VIRTUAL generated column, got:\n%s", got)
	}
}
