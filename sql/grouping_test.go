package sql

import "testing"

func TestGroupingCompile(t *testing.T) {
	cases := []struct {
		query Query
		want  string
	}{
		{Select(Col("a")).From(Table{Name: NewIdent("t")}).GroupBy(Rollup(Col("a"), Col("b"))),
			`SELECT "a" FROM "t" GROUP BY ROLLUP ("a", "b")`},
		{Select(Col("a")).From(Table{Name: NewIdent("t")}).GroupBy(Cube(Col("a"))),
			`SELECT "a" FROM "t" GROUP BY CUBE ("a")`},
		{Select(Col("a")).From(Table{Name: NewIdent("t")}).GroupBy(GroupingSets(
			[]Expression{Col("a"), Col("b")}, nil)),
			`SELECT "a" FROM "t" GROUP BY GROUPING SETS (("a", "b"), ())`},
	}
	for _, tc := range cases {
		got, _, err := tc.query.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if got != tc.want {
			t.Errorf("ToSQL() = %q, want %q", got, tc.want)
		}
	}
}
