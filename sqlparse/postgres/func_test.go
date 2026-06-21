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
