package sqlite_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/sqlite"
)

// SQLite binds a prefix NOT below comparison operators (above AND/OR), so
// "NOT a = 1" means NOT (a = 1); the converted query must keep that grouping.
func TestNotBindsBelowComparison(t *testing.T) {
	cases := map[string]string{
		"SELECT id FROM t WHERE NOT a = 1":             `SELECT "id" FROM "t" WHERE (NOT ("a" = ?))`,
		"SELECT id FROM t WHERE NOT b BETWEEN 2 AND 5": `SELECT "id" FROM "t" WHERE (NOT ("b" BETWEEN ? AND ?))`,
		"SELECT id FROM t WHERE NOT name LIKE 'a%'":    `SELECT "id" FROM "t" WHERE (NOT ("name" LIKE ?))`,
		"SELECT id FROM t WHERE NOT a + 1 = 2":         `SELECT "id" FROM "t" WHERE (NOT (("a" + ?) = ?))`,
		"SELECT id FROM t WHERE NOT a = 1 AND b > 1":   `SELECT "id" FROM "t" WHERE ((NOT ("a" = ?)) AND ("b" > ?))`,
	}
	for in, want := range cases {
		q, err := sqlite.ParseOne(in)
		if err != nil {
			t.Fatalf("ParseOne(%q): %v", in, err)
		}
		got, _, err := q.ToSQL(sql.SQLiteDialect{})
		if err != nil {
			t.Fatalf("ToSQL(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("%s\n  got  %s\n  want %s", in, got, want)
		}
	}
}

// "LIMIT a, b" is OFFSET a LIMIT b in SQLite.
func TestLimitCommaIsOffsetThenCount(t *testing.T) {
	assertSQL(t, "SELECT id FROM t LIMIT 1, 2", `SELECT "id" FROM "t" LIMIT 2 OFFSET 1`)
	assertSQL(t, "SELECT id FROM t UNION SELECT id FROM u LIMIT 3, 4",
		`SELECT "id" FROM "t" UNION SELECT "id" FROM "u" LIMIT 4 OFFSET 3`)
}

// A statement the converters can't model (LIKE ... ESCAPE) falls back to its
// own source text as a RawQuery instead of panicking in rqlite's deparser.
func TestUnsupportedStatementKeepsSourceText(t *testing.T) {
	const q = "SELECT id FROM t WHERE name LIKE 'a!_%' ESCAPE '!'"
	assertRaw(t, q)
	queries, err := sqlite.Parse("SELECT 1; " + q + ";\nSELECT 2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	raw, ok := queries[1].(sql.RawQuery)
	if !ok || raw.SQL() != q {
		t.Fatalf("second statement = %#v, want RawQuery of %q", queries[1], q)
	}
	if !strings.Contains(raw.SQL(), "ESCAPE") {
		t.Fatalf("raw text lost ESCAPE: %q", raw.SQL())
	}
}
