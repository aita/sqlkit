package redaction_test

import (
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// A subquery's projections can feed a sensitive column positionally — the
// i-th projection of (id, name) IN (SELECT ...) is matched against name, and an
// INSERT ... SELECT without a column list writes its projections into the
// table's own columns. A literal in such a projection must be masked; a
// columnless INSERT ... SELECT can't be mapped, so it is masked fail-safe.
func TestApplyMasksPositionalProjections(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	const s = "SECRETTARO"
	sub := sql.Select(sql.As(sql.Cast(sql.Val(1), "int"), "a"), sql.As(sql.Cast(sql.Val(s), "text"), "b"))
	row := sql.Row(sql.Col("id"), sql.Col("name"))
	cases := []struct {
		name string
		q    sql.Query
	}{
		{"row in subquery", sql.Select(sql.Star()).From(users).Where(sql.InQuery(row, sub))},
		{"row in union subquery", sql.Select(sql.Star()).From(users).Where(sql.InQuery(row, sub.UnionAll(sub)))},
		{"row eq scalar subquery", sql.Select(sql.Star()).From(users).Where(sql.Eq(row, sql.ScalarSubquery(sub)))},
		{"scalar subquery eq row", sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.ScalarSubquery(sub), row))},
		{"insert select without columns", sql.InsertInto(users).FromSelect(sub)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertNoLeak(t, tc.name, tc.q, s) })
	}

	// The non-sensitive position is left intact.
	q := sql.Select(sql.Star()).From(users).Where(sql.InQuery(row, sub))
	if a := args(t, redaction.New(redaction.Columns("users.name")).Apply(q)); len(a) != 2 || a[0] != 1 {
		t.Errorf("args = %v, want the id literal 1 kept", a)
	}
}
