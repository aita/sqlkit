package analysis_test

import (
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// A FOR UPDATE nested in a derived table, a CTE, a set-operation arm or a
// subquery still locks rows when the statement runs, so it must be a locking
// read (routed to the primary) just like a top-level one.
func TestNestedLockingReadIsWrite(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	locked := sql.Select(sql.Col("id")).From(users).ForUpdate()
	plain := sql.Select(sql.Col("id")).From(users)

	cases := map[string]sql.Query{
		"derived table": sql.Select(sql.Col("s.id")).From(locked.As("s")),
		"cte":           sql.Select(sql.Col("id")).WithCTE(locked.CTE("l")).From(sql.Table{Name: sql.NewIdent("l")}),
		"where subquery": sql.Select(sql.Col("id")).From(users).
			Where(sql.InQuery(sql.Col("id"), locked)),
		"union arm": plain.Union(sql.Select(sql.Col("id")).From(sql.Select(sql.Col("id")).From(users).ForShare().As("s"))),
	}
	for name, q := range cases {
		a := analysis.New().Analyze("", q)
		if !a.LockingRead || !a.IsWrite() {
			t.Errorf("%s: LockingRead=%v IsWrite=%v, want both true", name, a.LockingRead, a.IsWrite())
		}
		if !analysis.IsLockingRead(q) {
			t.Errorf("%s: IsLockingRead = false, want true", name)
		}
	}

	if a := analysis.New().Analyze("", sql.Select(sql.Col("s.id")).From(plain.As("s"))); a.LockingRead {
		t.Error("plain derived table: LockingRead = true, want false")
	}
}
