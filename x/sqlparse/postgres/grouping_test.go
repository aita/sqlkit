package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestGroupByRollup(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b FROM t GROUP BY ROLLUP (a, b)",
		sql.Select(sql.Col("a"), sql.Col("b")).From(sql.Table{Name: "t"}).
			GroupBy(sql.Rollup(sql.Col("a"), sql.Col("b"))),
		`SELECT "a", "b" FROM "t" GROUP BY ROLLUP ("a", "b")`)
}

func TestGroupByCube(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t GROUP BY CUBE (a, b)",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t"}).
			GroupBy(sql.Cube(sql.Col("a"), sql.Col("b"))),
		`SELECT "a" FROM "t" GROUP BY CUBE ("a", "b")`)
}

func TestGroupByGroupingSets(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b FROM t GROUP BY GROUPING SETS ((a, b), (a), ())",
		sql.Select(sql.Col("a"), sql.Col("b")).From(sql.Table{Name: "t"}).
			GroupBy(sql.GroupingSets(
				[]sql.Expression{sql.Col("a"), sql.Col("b")},
				[]sql.Expression{sql.Col("a")},
				nil,
			)),
		`SELECT "a", "b" FROM "t" GROUP BY GROUPING SETS (("a", "b"), ("a"), ())`)
}
