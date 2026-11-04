package sql_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
)

// TestMultiTableRowLimitRejected covers MySQL's prohibition on ORDER BY / LIMIT
// for a multi-table (joined) UPDATE or DELETE. The single-table form accepts
// them; the multi-table form must be rejected at compile time rather than emit
// SQL the engine refuses to prepare.
func TestMultiTableRowLimitRejected(t *testing.T) {
	my := sql.MySQLDialect{}

	t.Run("multi-table delete with limit errors", func(t *testing.T) {
		q := sql.DeleteFrom(sql.Tbl("t")).
			Join(sql.Tbl("u"), sql.Eq(sql.Col("t.id"), sql.Col("u.id"))).
			Limit(3)
		if _, _, err := q.ToSQL(my); err == nil || !strings.Contains(err.Error(), "multi-table") {
			t.Fatalf("ToSQL err = %v, want a multi-table error", err)
		}
	})

	t.Run("multi-table update with order by errors", func(t *testing.T) {
		q := sql.Update(sql.Tbl("t")).
			Set(sql.Set("a", 1)).
			Join(sql.Tbl("u"), sql.Eq(sql.Col("t.id"), sql.Col("u.id"))).
			OrderBy(sql.Asc(sql.Col("t.id")))
		if _, _, err := q.ToSQL(my); err == nil || !strings.Contains(err.Error(), "multi-table") {
			t.Fatalf("ToSQL err = %v, want a multi-table error", err)
		}
	})

	t.Run("single-table delete with limit is allowed", func(t *testing.T) {
		q := sql.DeleteFrom(sql.Tbl("t")).
			Where(sql.Eq(sql.Col("id"), sql.Val(1))).
			OrderBy(sql.Asc(sql.Col("id"))).
			Limit(3)
		got, _, err := q.ToSQL(my)
		if err != nil {
			t.Fatalf("single-table delete ToSQL err = %v", err)
		}
		if !strings.Contains(got, "LIMIT 3") {
			t.Errorf("single-table delete = %q, want a LIMIT 3 tail", got)
		}
	})

	t.Run("multi-table delete without limit is allowed", func(t *testing.T) {
		q := sql.DeleteFrom(sql.Tbl("t")).
			Join(sql.Tbl("u"), sql.Eq(sql.Col("t.id"), sql.Col("u.id")))
		if _, _, err := q.ToSQL(my); err != nil {
			t.Fatalf("multi-table delete without limit ToSQL err = %v", err)
		}
	})
}
