package sqltest_test

import (
	"context"
	"database/sql/driver"
	"os"
	"path/filepath"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sqltest"
)

func TestRenderRows(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "name", "note"},
				Rows: [][]driver.Value{
					{int64(1), "alice", nil},
					{int64(2), []byte("bob"), "x"},
				},
			}, nil
		},
	}
	rows, err := fake.DB().QueryContext(context.Background(), "SELECT id, name, note FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got, err := sqltest.RenderRows(rows)
	if err != nil {
		t.Fatalf("RenderRows: %v", err)
	}
	want := "id\tname\tnote\n1\talice\tNULL\n2\tbob\tx\n"
	if got != want {
		t.Fatalf("RenderRows =\n%q\nwant\n%q", got, want)
	}
}

func TestGoldenUpdateThenCompare(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "out.golden")

	// Update mode writes the file (creating parent dirs).
	t.Setenv("SQLKIT_UPDATE_GOLDEN", "1")
	sqltest.Golden(t, path, "hello\nworld\n")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file not written: %v", err)
	}
	if string(data) != "hello\nworld\n" {
		t.Fatalf("golden contents = %q", string(data))
	}

	// Compare mode against the just-written file passes for equal content.
	t.Setenv("SQLKIT_UPDATE_GOLDEN", "")
	sqltest.Golden(t, path, "hello\nworld\n")
}

func TestAssertGoldenRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.golden")
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(7)}}}, nil
		},
	}

	// Update mode records the rendered rows.
	t.Setenv("SQLKIT_UPDATE_GOLDEN", "1")
	r1, _ := fake.DB().QueryContext(context.Background(), "SELECT id FROM users ORDER BY id")
	sqltest.AssertGoldenRows(t, path, r1)

	// Compare mode passes for the same rows.
	t.Setenv("SQLKIT_UPDATE_GOLDEN", "")
	r2, _ := fake.DB().QueryContext(context.Background(), "SELECT id FROM users ORDER BY id")
	sqltest.AssertGoldenRows(t, path, r2)
}

func TestDBAssertGoldenQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.golden")
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}, {int64(2)}}}, nil
		},
	}
	tdb := sqltest.New(sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.PostgreSQLDialect{})), meta.Metadata{})

	t.Setenv("SQLKIT_UPDATE_GOLDEN", "1")
	tdb.AssertGoldenQuery(t, path, "SELECT id FROM users ORDER BY id")

	t.Setenv("SQLKIT_UPDATE_GOLDEN", "")
	tdb.AssertGoldenQuery(t, path, "SELECT id FROM users ORDER BY id")
}

// A tab or newline inside a value is escaped, so a value can never be mistaken
// for a column or row boundary and distinct results never render alike.
func TestRenderRowsEscapesSeparators(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"a", "b"},
				Rows:    [][]driver.Value{{"x\ty", "line1\nline2\\"}},
			}, nil
		},
	}
	rows, err := fake.DB().QueryContext(context.Background(), "SELECT a, b FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got, err := sqltest.RenderRows(rows)
	if err != nil {
		t.Fatalf("RenderRows: %v", err)
	}
	if want := "a\tb\nx\\ty\tline1\\nline2\\\\\n"; got != want {
		t.Fatalf("RenderRows =\n%q\nwant\n%q", got, want)
	}
}
