package decl_test

import (
	"strings"
	"testing"

	querysql "github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/decl"
)

func TestObjectsCreateSQL(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Extension("pg_trgm")
	catalog.Enum("mood", "happy", "sad")
	catalog.Sequence("order_no", decl.SequenceStart(1000), decl.SequenceIncrement(1))
	catalog.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	md := buildCatalog(t, catalog)

	got, err := md.CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}

	// Order matters: extension and enum/sequence before the table.
	wantOrder := []string{
		`CREATE EXTENSION IF NOT EXISTS "pg_trgm";`,
		`CREATE TYPE "public"."mood" AS ENUM ('happy', 'sad');`,
		`CREATE SEQUENCE "public"."order_no" INCREMENT BY 1 START WITH 1000;`,
		`CREATE TABLE "public"."users"`,
	}
	assertOrdered(t, got, wantOrder)

	drop := md.DropSQL(querysql.PostgreSQLDialect{})
	// Reverse: table dropped before the sequence/enum, extension last.
	assertOrdered(t, drop, []string{
		`DROP TABLE`,
		`DROP SEQUENCE IF EXISTS "public"."order_no";`,
		`DROP TYPE IF EXISTS "public"."mood";`,
		`DROP EXTENSION IF EXISTS "pg_trgm";`,
	})
}

func TestObjectsDuplicateAndEmpty(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Enum("mood", "happy")
	catalog.Enum("mood", "sad") // duplicate name
	catalog.Enum("blank")       // no values
	if _, err := catalog.Build(); err == nil {
		t.Fatal("expected Build to fail on duplicate and empty enum")
	}
}

func TestObjectsOnlyCatalogBuilds(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Extension("uuid-ossp")
	if _, err := catalog.Build(); err != nil {
		t.Fatalf("a catalog with only objects (no tables) should build: %v", err)
	}
}

// assertOrdered checks that each want substring appears in got, in order.
func assertOrdered(t *testing.T, got string, want []string) {
	t.Helper()
	pos := 0
	for _, w := range want {
		i := strings.Index(got[pos:], w)
		if i < 0 {
			t.Fatalf("missing or out-of-order %q in:\n%s", w, got)
		}
		pos += i + len(w)
	}
}
