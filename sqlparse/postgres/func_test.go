package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

// COUNT with more than one argument is not the modeled COUNT form, so the
// statement falls back to raw.
func TestCountMultipleArgsFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT count(a, b) FROM t")
}

// EXTRACT(field FROM x) is reported by libpg_query as pg_catalog.extract tagged
// COERCE_SQL_SYNTAX; it must rebuild as the dedicated node so it renders with the
// keyword syntax rather than the call form extract(field, x) the grammar rejects.
func TestExprExtract(t *testing.T) {
	assertRoundTrip(t,
		"SELECT EXTRACT(YEAR FROM d) FROM t",
		sql.Select(sql.Extract("year", sql.Col("d"))).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT EXTRACT(year FROM "d") FROM "t"`)
}

// POSITION(sub IN str) is stored with the operands swapped (position(str, sub));
// the dedicated node restores the source order and the IN keyword syntax.
func TestExprPosition(t *testing.T) {
	assertRoundTrip(t,
		"SELECT POSITION('a' IN s) FROM t",
		sql.Select(sql.Position("a", sql.Col("s"))).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT POSITION($1 IN "s") FROM "t"`,
		"a")
}

// A non-COUNT aggregate applied to * (e.g. sum(*)) is not representable and
// falls back to raw.
func TestAggregateStarFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT sum(*) FROM t")
}

// A searched CASE without an ELSE branch round-trips, omitting the ELSE.
func TestExprCaseWithoutElse(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE WHEN a > 0 THEN 1 END FROM t",
		sql.Select(sql.Case().When(sql.Gt(sql.Col("a"), 0), 1)).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CASE WHEN ("a" > $1) THEN $2 END FROM "t"`,
		0, 1)
}

// TABLE t is shorthand for SELECT * FROM t and parses to the equivalent select.
func TestTableShorthand(t *testing.T) {
	assertRoundTrip(t,
		"TABLE t",
		sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT * FROM "t"`)
}
