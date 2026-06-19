package migrate_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
)

func TestDiffAddsObjects(t *testing.T) {
	old := meta.Metadata{}
	start := int64(1000)
	next := meta.Metadata{
		Extensions: []meta.Extension{{Name: "pg_trgm"}},
		Enums:      []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad"}}},
		Sequences:  []meta.Sequence{{Schema: "public", Name: "order_no", Start: &start}},
		Tables:     []meta.Table{tbl("users", col("id", "bigint"))},
	}
	stmts := renderAll(t, mustDiff(t, old, next))

	// Objects create before the table.
	assertOrderedSQL(t, stmts, []string{
		`CREATE EXTENSION IF NOT EXISTS "pg_trgm";`,
		`CREATE TYPE "public"."mood" AS ENUM ('happy', 'sad');`,
		`CREATE SEQUENCE "public"."order_no" START WITH 1000;`,
		`CREATE TABLE "public"."users"`,
	})
}

func TestDiffDropsObjects(t *testing.T) {
	old := meta.Metadata{
		Extensions: []meta.Extension{{Name: "pg_trgm"}},
		Enums:      []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy"}}},
		Sequences:  []meta.Sequence{{Schema: "public", Name: "order_no"}},
	}
	stmts := renderAll(t, mustDiff(t, old, meta.Metadata{}))
	assertOrderedSQL(t, stmts, []string{
		`DROP SEQUENCE IF EXISTS "public"."order_no";`,
		`DROP TYPE IF EXISTS "public"."mood";`,
		`DROP EXTENSION IF EXISTS "pg_trgm";`,
	})
}

func TestDiffChangedEnumRecreates(t *testing.T) {
	old := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy"}}}}
	next := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad"}}}}
	stmts := renderAll(t, mustDiff(t, old, next))
	assertOrderedSQL(t, stmts, []string{
		`DROP TYPE IF EXISTS "public"."mood";`,
		`CREATE TYPE "public"."mood" AS ENUM ('happy', 'sad');`,
	})
}

func mustDiff(t *testing.T, old, next meta.Metadata) []sql.Statement {
	t.Helper()
	stmts, err := migrate.Diff(old, next, pg(t))
	if err != nil {
		t.Fatalf("Diff error = %v", err)
	}
	return stmts
}

func assertOrderedSQL(t *testing.T, got []string, want []string) {
	t.Helper()
	joined := strings.Join(got, "\n")
	pos := 0
	for _, w := range want {
		i := strings.Index(joined[pos:], w)
		if i < 0 {
			t.Fatalf("missing or out-of-order %q in:\n%s", w, joined)
		}
		pos += i + len(w)
	}
}
