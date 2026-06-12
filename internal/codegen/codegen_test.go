package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/sqlkit-dev/sqlkit/internal/genmodel"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixtureSchema() genmodel.Schema {
	return genmodel.Schema{
		Name: "public",
		Tables: []genmodel.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []genmodel.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "email", DataType: "varchar"},
					{Name: "name", DataType: "text", Nullable: true},
					{Name: "created_at", DataType: "timestamptz", HasDefault: true},
				},
			},
			{
				Schema: "public",
				Name:   "posts",
				Columns: []genmodel.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "user_id", DataType: "int8"},
					{Name: "title", DataType: "text"},
					{Name: "body", DataType: "text", Nullable: true},
					{Name: "published", DataType: "bool", HasDefault: true},
				},
			},
		},
	}
}

func TestGenerateGolden(t *testing.T) {
	source, err := Generate(fixtureSchema(), Options{PackageName: "blogdb"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	golden := filepath.Join("testdata", "blogdb.go.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, source, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create)", err)
	}
	if string(source) != string(want) {
		t.Fatalf("generated code differs from golden file.\n--- got ---\n%s", source)
	}
}

func TestNaming(t *testing.T) {
	cases := []struct{ in, out string }{
		{"users", "Users"},
		{"user_id", "UserID"},
		{"http_request_logs", "HTTPRequestLogs"},
		{"created_at", "CreatedAt"},
	}
	for _, c := range cases {
		if got := goName(c.in); got != c.out {
			t.Errorf("goName(%q) = %q, want %q", c.in, got, c.out)
		}
	}

	params := []struct{ in, out string }{
		{"ID", "id"},
		{"UserID", "userID"},
		{"URLPath", "urlPath"},
		{"Type", "type_"},
		{"Email", "email"},
	}
	for _, c := range params {
		if got := paramName(c.in); got != c.out {
			t.Errorf("paramName(%q) = %q, want %q", c.in, got, c.out)
		}
	}

}

func TestGenerateUnknownTypeFails(t *testing.T) {
	schema := genmodel.Schema{Tables: []genmodel.Table{{
		Schema:  "public",
		Name:    "things",
		Columns: []genmodel.Column{{Name: "weird", DataType: "hstore"}},
	}}}
	if _, err := Generate(schema, Options{PackageName: "x"}); err == nil {
		t.Fatal("Generate() with unsupported type succeeded, want error")
	}
}
