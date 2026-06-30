package sqltest_test

import (
	"context"
	"database/sql/driver"
	"os"
	"path/filepath"
	"testing"

	"github.com/aita/sqlkit/internal/testutil"
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
