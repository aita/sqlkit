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

// A bare integer in GROUP BY / ORDER BY is a column position; bound as a
// parameter it is a constant (ORDER BY ? sorts nothing, GROUP BY ? collapses
// the groups), so it must stay literal.
func TestPositionalGroupAndOrderStayLiteral(t *testing.T) {
	assertSQL(t, "SELECT g, count(*) FROM t GROUP BY 1 ORDER BY 2 DESC, 1",
		`SELECT "g", COUNT(*) FROM "t" GROUP BY 1 ORDER BY 2 DESC, 1`)
	assertSQL(t, "SELECT a FROM t UNION SELECT b FROM u ORDER BY 1",
		`SELECT "a" FROM "t" UNION SELECT "b" FROM "u" ORDER BY 1`)
}

// rqlite gives the two-word negated operators (NOT LIKE, IS NOT, NOT GLOB, ...)
// no precedence and parses BETWEEN's low bound at the lowest level, so trailing
// AND/OR/comparisons end up inside the operand ("b NOT LIKE ('x%' AND a = 2)").
// Those statements must stay raw (their source text), not be converted with
// the wrong grouping.
func TestMisgroupedComparisonsStayRaw(t *testing.T) {
	for _, q := range []string{
		"SELECT id FROM t WHERE b NOT LIKE 'x%' AND a = 2",
		"SELECT id FROM t WHERE a IS NOT 2 AND b = 'x'",
		"SELECT id FROM t WHERE b NOT GLOB 'x*' OR a = 1",
		"SELECT id FROM t WHERE a BETWEEN 1 AND 2 AND b = 'x'",
		"SELECT id FROM t WHERE a NOT BETWEEN 1 AND 1 AND id > 1",
		"SELECT id FROM t WHERE NOT a = 1 IS NULL",
	} {
		assertRaw(t, q)
	}
	// Operands that genuinely bind tighter still convert.
	assertSQL(t, "SELECT id FROM t WHERE b NOT LIKE 'x' || '%'", `SELECT "id" FROM "t" WHERE ("b" NOT LIKE (? || ?))`, "x", "%")
}

// "NOT a IS NULL" is NOT (a IS NULL) in SQLite; rqlite parses (NOT a) IS NULL.
func TestNotBindsBelowNullTest(t *testing.T) {
	assertSQL(t, "SELECT id FROM t WHERE NOT a IS NULL", `SELECT "id" FROM "t" WHERE (NOT ("a" IS NULL))`)
	assertSQL(t, "SELECT id FROM t WHERE NOT a IS NOT NULL", `SELECT "id" FROM "t" WHERE (NOT ("a" IS NOT NULL))`)
}
