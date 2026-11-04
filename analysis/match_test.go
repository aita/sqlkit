package analysis_test

import (
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// TestColumnsReportsMatchColumns checks that the columns searched by a full-text
// MATCH predicate are reported by analysis.Columns. MatchExpression must be a
// sql.Node and walked, or its columns are invisible to every Walk-based pass.
func TestColumnsReportsMatchColumns(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	q := sql.Select(sql.Star()).From(users).
		Where(sql.Match(sql.Col("title"), sql.Col("body")).Against("x"))
	cols := analysis.Columns(q)
	want := map[string]bool{"title": false, "body": false}
	for _, c := range cols {
		if _, ok := want[c.Column.Name.String()]; ok {
			want[c.Column.Name.String()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("analysis.Columns did not report MATCH column %q; got %+v", name, cols)
		}
	}
}
