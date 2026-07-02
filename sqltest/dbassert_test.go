package sqltest

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

func TestBuildWhere(t *testing.T) {
	clause, args := buildWhere(sqlkit.PostgreSQLDialect{}, map[string]any{
		"email":   "a@example.com",
		"deleted": nil,
	})
	// Columns sort: deleted, email. A nil value renders IS NULL with no arg.
	want := ` WHERE "deleted" IS NULL AND "email" = $1`
	if clause != want {
		t.Fatalf("clause = %q, want %q", clause, want)
	}
	if !reflect.DeepEqual(args, []any{"a@example.com"}) {
		t.Fatalf("args = %v, want [a@example.com]", args)
	}

	if c, a := buildWhere(sqlkit.MySQLDialect{}, nil); c != "" || a != nil {
		t.Fatalf("empty where = %q, %v; want empty", c, a)
	}
}

func TestNormalizeValueAndRowKey(t *testing.T) {
	// A column compares equal whatever concrete type it scans back as.
	if rowKey([]any{int64(1)}) != rowKey([]any{"1"}) || rowKey([]any{[]byte("1")}) != rowKey([]any{1}) {
		t.Fatal("rowKey not tolerant of int64/string/[]byte for the same value")
	}
	// NULL is distinct from the empty string.
	if normalizeValue(nil) == normalizeValue("") {
		t.Fatal("NULL collides with empty string")
	}
}

func TestDiffRows(t *testing.T) {
	cols := []string{"id", "name"}
	got := [][]any{
		{int64(1), "alice"},
		{int64(2), "bob"},
		{int64(2), "bob"}, // a duplicate in the table
	}
	t.Run("exact match incl duplicate", func(t *testing.T) {
		want := []map[string]any{
			{"id": 1, "name": "alice"},
			{"id": 2, "name": "bob"},
			{"id": 2, "name": "bob"},
		}
		missing, extra := diffRows(cols, want, got)
		if len(missing) != 0 || len(extra) != 0 {
			t.Fatalf("missing=%v extra=%v, want none", missing, extra)
		}
	})
	t.Run("missing and extra", func(t *testing.T) {
		want := []map[string]any{
			{"id": 1, "name": "alice"},
			{"id": 2, "name": "bob"}, // table has two of these → one left extra
			{"id": 9, "name": "zoe"}, // not in table → missing
		}
		missing, extra := diffRows(cols, want, got)
		if len(missing) != 1 || !strings.Contains(missing[0], "zoe") {
			t.Fatalf("missing = %v, want one zoe", missing)
		}
		if len(extra) != 1 || !strings.Contains(extra[0], "bob") {
			t.Fatalf("extra = %v, want one leftover bob", extra)
		}
	})
}

func TestAssertCount(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(sql string, _ []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(3)}}}, nil
		},
	}
	// Pass path: 3 rows == want 3 (no failure).
	AssertCount(t, fake.DB(), sqlkit.PostgreSQLDialect{}, "users", 3)

	// The COUNT query carries the WHERE clause.
	countWhere(t, fake.DB(), sqlkit.PostgreSQLDialect{}, "users", map[string]any{"active": true})
	var found bool
	for _, c := range fake.Queries() {
		if strings.Contains(c.SQL, "COUNT(*)") && strings.Contains(c.SQL, `WHERE "active" = $1`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("count query with WHERE not issued: %v", fake.Queries())
	}
}

func TestAssertTableContents(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(sql string, _ []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"email", "id"},
				Rows: [][]driver.Value{
					{"a@example.com", int64(1)},
					{"b@example.com", int64(2)},
				},
			}, nil
		},
	}
	// Only the named columns (email, id) are compared; a title column the table
	// might also have is ignored because want does not name it.
	AssertTableContents(t, fake.DB(), sqlkit.PostgreSQLDialect{}, "users", []map[string]any{
		{"id": 1, "email": "a@example.com"},
		{"id": 2, "email": "b@example.com"},
	})

	// The SELECT lists only the wanted columns, sorted.
	q := fake.Queries()
	if len(q) == 0 || !strings.HasPrefix(q[0].SQL, `SELECT "email", "id" FROM "users"`) {
		t.Fatalf("select = %q, want only the named columns", q)
	}
}
