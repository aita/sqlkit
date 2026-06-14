package sql_test

import (
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestNamedWindowClause(t *testing.T) {
	// Two aggregates reference one window defined in the WINDOW clause.
	q := Select(
		As(Over(Sum(Col("score"))).Named("w"), "running_total"),
		As(Over(Avg(Col("score"))).Named("w"), "running_avg"),
	).From(Table{Name: "games"}).
		Window(WindowDef("w").PartitionBy(Col("team")).OrderBy(Asc(Col("played_at"))))

	assertSQL(t, q,
		`SELECT SUM("score") OVER "w" AS "running_total", AVG("score") OVER "w" AS "running_avg" `+
			`FROM "games" WINDOW "w" AS (PARTITION BY "team" ORDER BY "played_at")`,
		nil)
}

func TestNamedWindowExtended(t *testing.T) {
	// A reference may extend the named base window with extra clauses.
	q := Select(
		As(Over(RowNumber()).Named("w").OrderBy(Desc(Col("score"))), "rn"),
	).From(Table{Name: "games"}).
		Window(WindowDef("w").PartitionBy(Col("team")))

	assertSQL(t, q,
		`SELECT ROW_NUMBER() OVER ("w" ORDER BY "score" DESC) AS "rn" FROM "games" WINDOW "w" AS (PARTITION BY "team")`,
		nil)
}
