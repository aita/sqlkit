package sqltest

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

func TestBuildInsert(t *testing.T) {
	tests := []struct {
		name    string
		dialect sqlkit.Dialect
		table   string
		cols    []string
		rows    int
		want    string
	}{
		{
			name:    "postgres multi-row qualified",
			dialect: sqlkit.PostgreSQLDialect{},
			table:   "public.users",
			cols:    []string{"email", "name"},
			rows:    2,
			want:    `INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4)`,
		},
		{
			name:    "mysql single row",
			dialect: sqlkit.MySQLDialect{},
			table:   "users",
			cols:    []string{"a"},
			rows:    1,
			want:    "INSERT INTO `users` (`a`) VALUES (?)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildInsert(tc.dialect, tc.table, tc.cols, tc.rows); got != tc.want {
				t.Fatalf("buildInsert =\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

func TestColumnsOf(t *testing.T) {
	cols, err := columnsOf([]map[string]any{
		{"name": "x", "email": "a"},
		{"email": "b", "name": "y"},
	})
	if err != nil {
		t.Fatalf("columnsOf: %v", err)
	}
	if want := []string{"email", "name"}; !reflect.DeepEqual(cols, want) {
		t.Fatalf("columns = %v, want sorted %v", cols, want)
	}

	if _, err := columnsOf([]map[string]any{{}}); !errors.Is(err, errEmptyRow) {
		t.Fatalf("empty row error = %v, want errEmptyRow", err)
	}
	if _, err := columnsOf([]map[string]any{{"a": 1}, {"b": 2}}); !errors.Is(err, errColumnMismatch) {
		t.Fatalf("mismatch error = %v, want errColumnMismatch", err)
	}
	if _, err := columnsOf([]map[string]any{{"a": 1}, {"a": 2, "b": 3}}); !errors.Is(err, errColumnMismatch) {
		t.Fatalf("count mismatch error = %v, want errColumnMismatch", err)
	}
}

func TestInsertMap(t *testing.T) {
	fake := &testutil.Fake{}
	InsertMap(t, fake.DB(), sqlkit.PostgreSQLDialect{}, "users",
		map[string]any{"email": "a@example.com", "name": "alice"},
		map[string]any{"name": "bob", "email": "b@example.com"},
	)
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("execs = %d, want 1 multi-row insert", len(execs))
	}
	wantSQL := `INSERT INTO "users" ("email", "name") VALUES ($1, $2), ($3, $4)`
	if execs[0].SQL != wantSQL {
		t.Fatalf("sql = %q, want %q", execs[0].SQL, wantSQL)
	}
	// Args follow column order (email, name) for each row.
	wantArgs := []any{"a@example.com", "alice", "b@example.com", "bob"}
	if !reflect.DeepEqual(execs[0].Args, wantArgs) {
		t.Fatalf("args = %v, want %v", execs[0].Args, wantArgs)
	}
}

func TestInsertMapNoRows(t *testing.T) {
	fake := &testutil.Fake{}
	InsertMap(t, fake.DB(), sqlkit.PostgreSQLDialect{}, "users")
	if execs := fake.Execs(); len(execs) != 0 {
		t.Fatalf("execs = %d, want 0 for no rows", len(execs))
	}
}

func TestInsertMapIDReturning(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(7)}}}, nil
		},
	}
	id := InsertMapID(t, fake.DB(), sqlkit.PostgreSQLDialect{}, "users", "id",
		map[string]any{"email": "a@example.com"})
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
	queries := fake.Queries()
	if len(queries) != 1 {
		t.Fatalf("queries = %d, want 1 (RETURNING reads the key back)", len(queries))
	}
	if want := `RETURNING "id"`; !strings.Contains(queries[0].SQL, want) {
		t.Fatalf("query = %q, want it to contain %q", queries[0].SQL, want)
	}
}

func TestInsertMapIDLastInsertID(t *testing.T) {
	fake := &testutil.Fake{LastInsertID: 99}
	id := InsertMapID(t, fake.DB(), sqlkit.MySQLDialect{}, "users", "id",
		map[string]any{"email": "a@example.com"})
	if id != 99 {
		t.Fatalf("id = %d, want 99", id)
	}
	// MySQL has no RETURNING: the row is written with an exec, not a query.
	if queries := fake.Queries(); len(queries) != 0 {
		t.Fatalf("queries = %d, want 0 on MySQL", len(queries))
	}
	if execs := fake.Execs(); len(execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(execs))
	}
}
