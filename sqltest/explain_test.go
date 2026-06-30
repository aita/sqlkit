package sqltest_test

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sqltest"
)

func TestPlanPostgresSeqScan(t *testing.T) {
	seq := sqltest.NewPlan(sqlkit.PostgreSQLDialect{}, []string{"QUERY PLAN"}, [][]string{
		{"Seq Scan on users  (cost=0.00..35.50 rows=10 width=36)"},
		{"  Filter: (email = 'a'::text)"},
	})
	if !seq.HasSeqScan() {
		t.Error("HasSeqScan = false, want true for a Seq Scan plan")
	}
	if seq.UsesIndex("users_email_idx") {
		t.Error("UsesIndex = true, want false for a Seq Scan plan")
	}

	idx := sqltest.NewPlan(sqlkit.PostgreSQLDialect{}, []string{"QUERY PLAN"}, [][]string{
		{"Index Scan using users_email_idx on users  (cost=0.29..8.30 rows=1 width=36)"},
		{"  Index Cond: (email = 'a'::text)"},
	})
	if idx.HasSeqScan() {
		t.Error("HasSeqScan = true, want false for an Index Scan plan")
	}
	if !idx.UsesIndex("users_email_idx") {
		t.Error("UsesIndex = false, want true")
	}
	if idx.UsesIndex("some_other_idx") {
		t.Error("UsesIndex(other) = true, want false")
	}
}

func TestPlanMySQL(t *testing.T) {
	header := []string{"id", "select_type", "table", "type", "possible_keys", "key", "key_len", "ref", "rows", "Extra"}
	full := sqltest.NewPlan(sqlkit.MySQLDialect{}, header, [][]string{
		{"1", "SIMPLE", "users", "ALL", "", "", "", "", "10", "Using where"},
	})
	if !full.HasSeqScan() {
		t.Error("HasSeqScan = false, want true for type=ALL")
	}

	idx := sqltest.NewPlan(sqlkit.MySQLDialect{}, header, [][]string{
		{"1", "SIMPLE", "users", "ref", "users_email_idx", "users_email_idx", "767", "const", "1", "Using index"},
	})
	if idx.HasSeqScan() {
		t.Error("HasSeqScan = true, want false for type=ref")
	}
	if !idx.UsesIndex("users_email_idx") {
		t.Error("UsesIndex = false, want true (key column)")
	}
}

func TestPlanTextAndContains(t *testing.T) {
	p := sqltest.NewPlan(sqlkit.PostgreSQLDialect{}, []string{"QUERY PLAN"}, [][]string{
		{"Limit  (cost=0.00..1.00 rows=1 width=4)"},
	})
	if !p.Contains("limit") {
		t.Error("Contains(limit) = false, want true (case-insensitive)")
	}
	if p.Contains("hash join") {
		t.Error("Contains(hash join) = true, want false")
	}
	// A single unnamed plan column omits the header line.
	if got := p.Text(); got != "Limit  (cost=0.00..1.00 rows=1 width=4)\n" {
		t.Errorf("Text = %q", got)
	}
}

func TestExplainQuery(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(sql string, _ []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"QUERY PLAN"},
				Rows:    [][]driver.Value{{"Seq Scan on users  (cost=0.00..10.0 rows=1 width=4)"}},
			}, nil
		},
	}
	plan := sqltest.ExplainQuery(t, fake.DB(), sqlkit.PostgreSQLDialect{}, "SELECT * FROM users WHERE email = $1", "a@example.com")
	if !plan.HasSeqScan() {
		t.Error("HasSeqScan = false, want true")
	}
	// The statement was issued prefixed with EXPLAIN, and only EXPLAIN'd (no
	// direct execution of the SELECT).
	q := fake.Queries()
	if len(q) != 1 || !strings.HasPrefix(q[0].SQL, "EXPLAIN SELECT * FROM users") {
		t.Fatalf("issued queries = %v, want one EXPLAIN", q)
	}
}
