package redaction_test

import (
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// A VALUES derived table feeds its rows into the statement through its alias
// columns — the bulk UPDATE ... FROM (VALUES ...) a unit of work emits assigns
// v.n to the sensitive column. Its string literals must be masked; a numeric
// key is kept.
func TestApplyMasksValuesSource(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	const s = "SECRETTARO"
	vals := sql.ValuesSource([][]sql.Expression{
		{sql.Cast(sql.Val(1), "bigint"), sql.Cast(sql.Val(s), "text")},
		{sql.Val(2), sql.Val(s + "2")},
	}, "v", "id", "n")
	cases := []struct {
		name string
		q    sql.Query
	}{
		{"bulk update", sql.Update(users).Set(sql.Set("name", sql.Col("v.n"))).From(vals).
			Where(sql.Eq(sql.Col("users.id"), sql.Col("v.id")))},
		{"in subquery over values", sql.Select(sql.Star()).From(users).
			Where(sql.InQuery(sql.Col("name"), sql.Select(sql.Col("v.n")).From(vals)))},
		{"join values", sql.Select(sql.Star()).From(users).
			Join(vals, sql.Eq(sql.Col("users.name"), sql.Col("v.n")))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertNoLeak(t, tc.name, tc.q, s) })
	}

	q := cases[0].q
	a := args(t, redaction.New(redaction.Columns("users.name")).Apply(q))
	if len(a) != 4 || a[0] != 1 || a[2] != 2 {
		t.Errorf("args = %v, want the numeric keys kept", a)
	}
}
