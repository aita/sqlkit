package introspection

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit/internal/testutil"
)

// errBoom is a sentinel for injected query failures.
var errBoom = errors.New("boom")

// pgFakeFailing builds a Fake whose query matching the given substring fails,
// while every other query returns an empty result set. This lets each
// per-stage error path of inspectPostgres be exercised in isolation.
func pgFakeFailing(match string) *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(query string, _ []any) (testutil.Result, error) {
			if strings.Contains(query, match) {
				return testutil.Result{}, errBoom
			}
			return testutil.Result{}, nil
		},
	}
}

// TestInspectPostgresQueryErrors verifies that a failure of each per-stage query
// is wrapped and propagated rather than swallowed.
func TestInspectPostgresQueryErrors(t *testing.T) {
	stages := []struct {
		name  string
		match string
	}{
		{"columns", "information_schema.columns"},
		{"primary keys", "PRIMARY KEY"},
		{"foreign keys", "con.contype = 'f'"},
		{"indexes", "pg_index"},
		{"checks", "con.contype = 'c'"},
		{"comments", "pg_description"},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			fake := pgFakeFailing(stage.match)
			_, err := Schema(context.Background(), fake.DB(), "postgres", "public")
			if !errors.Is(err, errBoom) {
				t.Fatalf("Schema() err = %v, want wrapped errBoom", err)
			}
		})
	}
}

// TestInspectPostgresDefaultSchema verifies that an empty schema name defaults
// to "public" and is the bind argument on every query.
func TestInspectPostgresDefaultSchema(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{}, nil
		},
	}
	if _, err := Schema(context.Background(), fake.DB(), "postgres", ""); err != nil {
		t.Fatalf("Schema() error = %v", err)
	}
	for _, q := range fake.Queries() {
		if len(q.Args) != 1 || q.Args[0] != "public" {
			t.Fatalf("query bind = %#v, want [\"public\"]", q.Args)
		}
	}
}

// TestInspectPostgresOrphanRowsIgnored verifies that primary-key, check and
// comment rows referring to tables not present in the columns result are
// skipped (the unknown-table continue branches) rather than panicking, and
// that a table-level comment (empty column name) is applied.
func TestInspectPostgresOrphanRowsIgnored(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(query string, _ []any) (testutil.Result, error) {
			switch {
			case strings.Contains(query, "information_schema.columns"):
				return testutil.Result{
					Columns: []string{"table_name", "column_name", "udt_name", "nullable", "column_default", "is_identity", "length", "precision", "scale"},
					Rows: [][]driver.Value{
						{"users", "id", "int8", false, nil, false, 0, 64, 0},
					},
				}, nil
			case strings.Contains(query, "PRIMARY KEY"):
				return testutil.Result{
					Columns: []string{"table_name", "column_name"},
					Rows: [][]driver.Value{
						{"ghost", "id"}, // unknown table -> skipped
					},
				}, nil
			case strings.Contains(query, "pg_description"):
				return testutil.Result{
					Columns: []string{"table_name", "column_name", "comment"},
					Rows: [][]driver.Value{
						{"users", "", "Table comment"},      // table-level comment
						{"ghost", "", "Ignored"},            // unknown table -> skipped
						{"users", "missing", "no such col"}, // unknown column -> no-op loop
					},
				}, nil
			default:
				return testutil.Result{}, nil
			}
		},
	}

	got, err := Schema(context.Background(), fake.DB(), "postgres", "public")
	if err != nil {
		t.Fatalf("Schema() error = %v", err)
	}
	if len(got.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(got.Tables))
	}
	tbl := got.Tables[0]
	if tbl.Comment != "Table comment" {
		t.Errorf("table comment = %q, want %q", tbl.Comment, "Table comment")
	}
	if tbl.Columns[0].PrimaryKey {
		t.Errorf("orphan primary-key row was applied to a real column")
	}
}
