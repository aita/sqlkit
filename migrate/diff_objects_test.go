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

// TestDiffAppendedEnumAlters covers the common enum change: appending a label.
// It must become ALTER TYPE ... ADD VALUE (so a column using the type survives),
// never DROP TYPE + CREATE TYPE, which PostgreSQL rejects while the type is in
// use. Two labels appended at once emit one ADD VALUE each, in order.
func TestDiffAppendedEnumAlters(t *testing.T) {
	old := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad"}}}}
	next := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad", "neutral", "excited"}}}}
	stmts := renderAll(t, mustDiff(t, old, next))

	assertOrderedSQL(t, stmts, []string{
		`ALTER TYPE "public"."mood" ADD VALUE 'neutral';`,
		`ALTER TYPE "public"."mood" ADD VALUE 'excited';`,
	})
	joined := strings.Join(stmts, "\n")
	if strings.Contains(joined, "DROP TYPE") {
		t.Fatalf("appended enum must not drop the type:\n%s", joined)
	}
	if strings.Contains(joined, "CREATE TYPE") {
		t.Fatalf("appended enum must not recreate the type:\n%s", joined)
	}
}

// TestDiffAppendedEnumUsedByColumn checks the ordering guarantee: the ADD VALUE
// runs in the alter phase, never in the pre-table drop phase, so a column using
// the type is never disturbed and the migration applies cleanly.
func TestDiffAppendedEnumUsedByColumn(t *testing.T) {
	enumType := func(values ...string) meta.Metadata {
		return meta.Metadata{
			Enums:  []meta.Enum{{Schema: "public", Name: "mood", Values: values}},
			Tables: []meta.Table{tbl("users", col("id", "bigint"))},
		}
	}
	old := enumType("happy", "sad")
	next := enumType("happy", "sad", "neutral")
	stmts := renderAll(t, mustDiff(t, old, next))
	joined := strings.Join(stmts, "\n")
	if strings.Contains(joined, "DROP TYPE") || strings.Contains(joined, "CREATE TYPE") {
		t.Fatalf("appended enum used by a column must only ALTER:\n%s", joined)
	}
	if !strings.Contains(joined, `ALTER TYPE "public"."mood" ADD VALUE 'neutral';`) {
		t.Fatalf("missing ADD VALUE:\n%s", joined)
	}
}

// TestDiffEnumNoChange asserts an identical enum (A -> A) produces no statements.
func TestDiffEnumNoChange(t *testing.T) {
	md := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad"}}}}
	stmts := renderAll(t, mustDiff(t, md, md))
	if len(stmts) != 0 {
		t.Fatalf("A -> A should be empty, got:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestDiffNonAppendEnumRecreates keeps the drop+create fallback for changes that
// are not a pure append: a removed label, a reorder, a rename, and an inserted
// label all recreate the type (renames are not special-cased here).
func TestDiffNonAppendEnumRecreates(t *testing.T) {
	cases := map[string][2][]string{
		"removed":  {{"happy", "sad"}, {"happy"}},
		"reorder":  {{"happy", "sad"}, {"sad", "happy"}},
		"rename":   {{"happy", "sad"}, {"happy", "glum"}},
		"inserted": {{"happy", "sad"}, {"happy", "neutral", "sad"}},
	}
	for name, vals := range cases {
		t.Run(name, func(t *testing.T) {
			old := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: vals[0]}}}
			next := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: vals[1]}}}
			stmts := renderAll(t, mustDiff(t, old, next))
			assertOrderedSQL(t, stmts, []string{
				`DROP TYPE IF EXISTS "public"."mood";`,
				`CREATE TYPE "public"."mood" AS ENUM`,
			})
			if strings.Contains(strings.Join(stmts, "\n"), "ADD VALUE") {
				t.Fatalf("non-append change must not ADD VALUE:\n%s", strings.Join(stmts, "\n"))
			}
		})
	}
}

// TestDiffGrantsDistinctGranteesOnSameObject covers two distinct grants on the
// same object (different grantees, different privileges): adding one must not
// revoke the other. grantKey used to be keyed on (object type, objects) alone,
// so alice's unrelated, unchanged SELECT grant collided in the lookup map with
// bob's new INSERT grant and was silently revoked with no replacement GRANT.
func TestDiffGrantsDistinctGranteesOnSameObject(t *testing.T) {
	aliceSelect := meta.Grant{
		Privileges: []string{"SELECT"}, ObjectType: "TABLE",
		Objects: []string{"public.posts"}, Grantees: []string{"alice"},
	}
	old := meta.Metadata{Grants: []meta.Grant{aliceSelect}}
	next := meta.Metadata{Grants: []meta.Grant{
		aliceSelect, // unchanged
		{
			Privileges: []string{"INSERT"}, ObjectType: "TABLE",
			Objects: []string{"public.posts"}, Grantees: []string{"bob"},
		},
	}}
	stmts := renderAll(t, mustDiff(t, old, next))
	joined := strings.Join(stmts, "\n")

	if strings.Contains(joined, `REVOKE`) {
		t.Fatalf("alice's unchanged grant must not be revoked:\n%s", joined)
	}
	if !strings.Contains(joined, `GRANT INSERT ON TABLE "public"."posts" TO "bob";`) {
		t.Fatalf("missing bob's new grant:\n%s", joined)
	}
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
