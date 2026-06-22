package introspection

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit/internal/testutil"
)

// TestInspectMySQLQueryErrors verifies that a failure of each mandatory
// per-stage query is wrapped and propagated. The checks query is best-effort
// (a failure is swallowed) and is covered separately.
func TestInspectMySQLQueryErrors(t *testing.T) {
	stages := []struct {
		name  string
		match string
	}{
		{"columns", "information_schema.COLUMNS"},
		{"foreign keys", "REFERENCED_TABLE_NAME"},
		{"indexes", "STATISTICS"},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			fake := &testutil.Fake{
				QueryFunc: func(query string, _ []any) (testutil.Result, error) {
					if strings.Contains(query, stage.match) {
						return testutil.Result{}, errBoom
					}
					return testutil.Result{}, nil
				},
			}
			_, err := Schema(context.Background(), fake.DB(), "mysql", "app")
			if !errors.Is(err, errBoom) {
				t.Fatalf("Schema() err = %v, want wrapped errBoom", err)
			}
		})
	}
}

// TestInspectMySQLChecksBestEffort verifies the CHECK_CONSTRAINTS query is
// best-effort: a failure (e.g. on MySQL < 8.0.16 where the view is absent) is
// swallowed and the inspection still succeeds with no checks.
func TestInspectMySQLChecksBestEffort(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(query string, _ []any) (testutil.Result, error) {
			if strings.Contains(query, "CHECK_CONSTRAINTS") {
				return testutil.Result{}, errBoom
			}
			if strings.Contains(query, "information_schema.COLUMNS") {
				return testutil.Result{
					Columns: []string{"table_name", "column_name", "data_type", "nullable", "column_default", "auto_increment", "is_pk", "length", "precision", "scale", "column_comment", "table_comment"},
					Rows: [][]driver.Value{
						{"users", "id", "bigint", false, nil, true, true, 0, 19, 0, "", ""},
					},
				}, nil
			}
			return testutil.Result{}, nil
		},
	}
	got, err := Schema(context.Background(), fake.DB(), "mysql", "app")
	if err != nil {
		t.Fatalf("Schema() error = %v, want nil (checks are best-effort)", err)
	}
	if len(got.Tables) != 1 || len(got.Tables[0].Checks) != 0 {
		t.Fatalf("tables = %#v, want one table with no checks", got.Tables)
	}
}

// TestInspectMySQLCurrentSchema verifies that an empty schema name resolves the
// current database via SELECT DATABASE() and binds it on subsequent queries.
func TestInspectMySQLCurrentSchema(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(query string, _ []any) (testutil.Result, error) {
			if strings.Contains(query, "DATABASE()") {
				return testutil.Result{
					Columns: []string{"db"},
					Rows:    [][]driver.Value{{"resolved_db"}},
				}, nil
			}
			return testutil.Result{}, nil
		},
	}
	if _, err := Schema(context.Background(), fake.DB(), "mysql", ""); err != nil {
		t.Fatalf("Schema() error = %v", err)
	}
	for _, q := range fake.Queries() {
		if strings.Contains(q.SQL, "DATABASE()") {
			continue
		}
		if len(q.Args) != 1 || q.Args[0] != "resolved_db" {
			t.Fatalf("query bind = %#v, want [\"resolved_db\"]", q.Args)
		}
	}
}

// TestInspectMySQLNoCurrentSchema verifies that when DATABASE() returns NULL
// (no database selected on the connection) and no schema was passed, the
// inspection fails with a descriptive error rather than querying with an empty
// schema name.
func TestInspectMySQLNoCurrentSchema(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(query string, _ []any) (testutil.Result, error) {
			if strings.Contains(query, "DATABASE()") {
				return testutil.Result{
					Columns: []string{"db"},
					Rows:    [][]driver.Value{{nil}}, // NULL current database
				}, nil
			}
			return testutil.Result{}, nil
		},
	}
	_, err := Schema(context.Background(), fake.DB(), "mysql", "")
	if err == nil || !strings.Contains(err.Error(), "no MySQL schema selected") {
		t.Fatalf("Schema() err = %v, want 'no MySQL schema selected'", err)
	}
}

// TestInspectMySQLCurrentSchemaQueryError verifies that a failure of the
// DATABASE() resolution query is wrapped and propagated.
func TestInspectMySQLCurrentSchemaQueryError(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(query string, _ []any) (testutil.Result, error) {
			if strings.Contains(query, "DATABASE()") {
				return testutil.Result{}, errBoom
			}
			return testutil.Result{}, nil
		},
	}
	_, err := Schema(context.Background(), fake.DB(), "mysql", "")
	if !errors.Is(err, errBoom) {
		t.Fatalf("Schema() err = %v, want wrapped errBoom", err)
	}
}
