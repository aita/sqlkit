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

// TestGroupingSetsEmptyRejected pins that GROUPING SETS with zero sets is rejected
// rather than compiled to the invalid "GROUPING SETS ()". A single empty (nil) set
// is still valid — it is the grand total "()" — so only zero sets is an error.
func TestGroupingSetsEmptyRejected(t *testing.T) {
	q := Select(Col("a")).From(Table{Name: NewIdent("t")}).GroupBy(GroupingSets())
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("ToSQL() error = nil, want an error for GROUPING SETS with no sets")
	}

	// A single empty set remains valid: it renders as the grand-total "()".
	q = Select(Col("a")).From(Table{Name: NewIdent("t")}).GroupBy(GroupingSets(nil))
	got, _, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v, want a valid grand-total grouping set", err)
	}
	if want := `SELECT "a" FROM "t" GROUP BY GROUPING SETS (())`; got != want {
		t.Errorf("ToSQL() = %q, want %q", got, want)
	}
}
