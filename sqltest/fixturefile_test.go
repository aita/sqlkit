package sqltest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

func TestTableNameFromFile(t *testing.T) {
	tests := map[string]string{
		"users.json":    "users",
		"01_users.json": "users",
		"2-orders.csv":  "orders",
		"posts.csv":     "posts",
		"10_line.json":  "line",
	}
	for file, want := range tests {
		if got := tableNameFromFile(file); got != want {
			t.Errorf("tableNameFromFile(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestDecodeJSONRows(t *testing.T) {
	rows, err := decodeJSONRows([]byte(`[
		{"id": 1, "ratio": 1.5, "name": "alice", "deleted": null, "tags": ["a","b"]}
	]`))
	if err != nil {
		t.Fatalf("decodeJSONRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if got, ok := row["id"].(int64); !ok || got != 1 {
		t.Errorf("id = %#v, want int64(1)", row["id"])
	}
	if got, ok := row["ratio"].(float64); !ok || got != 1.5 {
		t.Errorf("ratio = %#v, want float64(1.5)", row["ratio"])
	}
	if row["deleted"] != nil {
		t.Errorf("deleted = %#v, want nil", row["deleted"])
	}
	if got, ok := row["tags"].(string); !ok || got != `["a","b"]` {
		t.Errorf("tags = %#v, want JSON text", row["tags"])
	}
}

func TestDecodeJSONRowsEmpty(t *testing.T) {
	rows, err := decodeJSONRows([]byte("  \n "))
	if err != nil || rows != nil {
		t.Fatalf("decodeJSONRows(empty) = %v, %v; want nil, nil", rows, err)
	}
}

func TestDecodeCSVRows(t *testing.T) {
	rows, err := decodeCSVRows([]byte("id,title,author\n10,Hello,alice\n11,Draft,\\N\n"))
	if err != nil {
		t.Fatalf("decodeCSVRows: %v", err)
	}
	want := []map[string]any{
		{"id": "10", "title": "Hello", "author": "alice"},
		{"id": "11", "title": "Draft", "author": nil},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v\nwant %#v", rows, want)
	}
}

func TestLoadFixtures(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("01_users.json", `[{"id":1,"email":"a@example.com","name":"alice"},{"id":2,"email":"b@example.com","name":null}]`)
	write("02_posts.csv", "id,user_id,title\n10,1,Hello\n")
	write("README.md", "ignored")

	fake := &testutil.Fake{}
	LoadFixtures(t, fake.DB(), sqlkit.PostgreSQLDialect{}, dir)

	execs := fake.Execs()
	if len(execs) != 3 {
		t.Fatalf("execs = %d, want 3 (2 users + 1 post)", len(execs))
	}
	// Files load in lexical order: users (01_) before posts (02_).
	if execs[0].SQL != `INSERT INTO "users" ("email", "id", "name") VALUES ($1, $2, $3)` {
		t.Fatalf("users insert SQL = %q", execs[0].SQL)
	}
	if !reflect.DeepEqual(execs[0].Args, []any{"a@example.com", int64(1), "alice"}) {
		t.Fatalf("users row 1 args = %#v", execs[0].Args)
	}
	if !reflect.DeepEqual(execs[1].Args, []any{"b@example.com", int64(2), nil}) {
		t.Fatalf("users row 2 args = %#v", execs[1].Args)
	}
	if execs[2].SQL != `INSERT INTO "posts" ("id", "title", "user_id") VALUES ($1, $2, $3)` {
		t.Fatalf("posts insert SQL = %q", execs[2].SQL)
	}
	if !reflect.DeepEqual(execs[2].Args, []any{"10", "Hello", "1"}) {
		t.Fatalf("posts row args = %#v", execs[2].Args)
	}
}

// A JSON integer outside the int64 range must stay exact (as its decimal text)
// rather than silently losing precision through float64.
func TestNormalizeJSONValueBigInteger(t *testing.T) {
	if got := normalizeJSONValue(json.Number("18446744073709551615")); got != "18446744073709551615" {
		t.Fatalf("big integer = %#v, want exact decimal string", got)
	}
	if got := normalizeJSONValue(json.Number("1.5")); got != 1.5 {
		t.Fatalf("1.5 = %#v, want float64 1.5", got)
	}
	if got := normalizeJSONValue(json.Number("42")); got != int64(42) {
		t.Fatalf("42 = %#v, want int64 42", got)
	}
}
