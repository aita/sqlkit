package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sqlkit-dev/sqlkit/schema"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixtureSchema mirrors examples/blog/schema.sql: users and posts with a
// foreign key from posts.user_id to users.id.
func fixtureSchema() schema.Schema {
	return schema.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []schema.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []schema.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "email", DataType: "varchar", Length: 255},
					{Name: "name", DataType: "text", Nullable: true},
					{Name: "created_at", DataType: "timestamptz", HasDefault: true},
				},
			},
			{
				Schema: "public",
				Name:   "posts",
				Columns: []schema.Column{
					{Name: "id", DataType: "int8", PrimaryKey: true, HasDefault: true},
					{Name: "user_id", DataType: "int8"},
					{Name: "title", DataType: "text"},
					{Name: "body", DataType: "text", Nullable: true},
					{Name: "published", DataType: "bool", HasDefault: true},
				},
				ForeignKeys: []schema.ForeignKey{
					{Name: "posts_user_id_fkey", Columns: []string{"user_id"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
				},
			},
		},
	}
}

func generateSingle(t *testing.T, model schema.Schema, opts Options) []byte {
	t.Helper()
	files, err := Generate(model, opts)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("Generate() produced %d files, want 1", len(files))
	}
	return files[0].Content
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", name, err)
	}
	if string(got) != string(want) {
		t.Fatalf("generated code differs from golden %s.\n--- got ---\n%s", name, got)
	}
}

func TestGenerateGolden(t *testing.T) {
	got := generateSingle(t, fixtureSchema(), Options{PackageName: "blogdb"})
	checkGolden(t, "blogdb.go.golden", got)
}

func TestGenerateWithDTOGolden(t *testing.T) {
	got := generateSingle(t, fixtureSchema(), Options{PackageName: "blogdb", EmitDTO: true})
	checkGolden(t, "blogdb_dto.go.golden", got)
}

func TestGenerateFilePerTable(t *testing.T) {
	files, err := Generate(fixtureSchema(), Options{PackageName: "blogdb", FilePerTable: true, EmitDTO: true, DTOSeparateFile: true})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Name] = true
	}
	for _, want := range []string{"users.go", "users_dto.go", "posts.go", "posts_dto.go"} {
		if !got[want] {
			t.Errorf("missing generated file %q; got %v", want, got)
		}
	}
}

func TestGenerateTablesFilter(t *testing.T) {
	files, err := Generate(fixtureSchema(), Options{PackageName: "blogdb", Tables: []string{"users"}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := string(files[0].Content)
	if !strings.Contains(body, "UsersTable") || strings.Contains(body, "PostsTable") {
		t.Fatalf("Tables filter did not restrict output:\n%s", body)
	}
}

func TestBindingsAreNotAnORM(t *testing.T) {
	body := string(generateSingle(t, fixtureSchema(), Options{PackageName: "blogdb"}))
	// The binding is metadata: no executor-bound accessors, no relation
	// navigation methods, and plain NewColumn calls without metadata options.
	for _, banned := range []string{"sqlkit.Executor", ") Get(", ") User()", ") Posts()", "sqlkit.With"} {
		if strings.Contains(body, banned) {
			t.Errorf("generated binding should not contain %q:\n%s", banned, body)
		}
	}
	if !strings.Contains(body, `sqlkit.NewColumn[int64](info, "id")`) {
		t.Errorf("expected plain NewColumn call:\n%s", body)
	}
}

func TestDTONameCollisionForSingularTable(t *testing.T) {
	model := schema.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []schema.Table{{
			Schema:  "public",
			Name:    "account",
			Columns: []schema.Column{{Name: "id", DataType: "int8", PrimaryKey: true}},
		}},
	}
	body := string(generateSingle(t, model, Options{PackageName: "x", EmitDTO: true}))
	// var Account (binding) must not collide with the DTO type, which becomes
	// AccountRow.
	if !strings.Contains(body, "type AccountRow struct") {
		t.Fatalf("singular table DTO should be AccountRow to avoid collision:\n%s", body)
	}
}

func TestCustomTypeMapper(t *testing.T) {
	mapper := DefaultTypeMapper{Overrides: map[string]GoType{
		"timestamptz": {ImportPath: "time", Name: "time.Time"},
		"int8":        {Name: "uint64"},
	}}
	body := string(generateSingle(t, fixtureSchema(), Options{PackageName: "blogdb", TypeMapper: mapper}))
	if !strings.Contains(body, "sqlkit.Column[uint64]") {
		t.Fatalf("custom type mapper override not applied:\n%s", body)
	}
}

func TestMySQLTypeMapping(t *testing.T) {
	model := schema.Schema{
		Name:    "app",
		Dialect: "mysql",
		Tables: []schema.Table{{
			Schema: "app",
			Name:   "items",
			Columns: []schema.Column{
				{Name: "id", DataType: "bigint", PrimaryKey: true, HasDefault: true},
				{Name: "qty", DataType: "int"},
				{Name: "label", DataType: "varchar"},
				{Name: "created_at", DataType: "datetime"},
			},
		}},
	}
	body := string(generateSingle(t, model, Options{PackageName: "appdb"}))
	for _, want := range []string{"sqlkit.Column[int64]", "sqlkit.Column[int32]", "sqlkit.Column[string]", "sqlkit.Column[time.Time]"} {
		if !strings.Contains(body, want) {
			t.Errorf("MySQL mapping missing %q:\n%s", want, body)
		}
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

	singulars := []struct{ in, out string }{
		{"users", "user"},
		{"posts", "post"},
		{"categories", "category"},
		{"boxes", "box"},
		{"addresses", "address"},
		{"account", "account"},
		{"address", "address"},
	}
	for _, c := range singulars {
		if got := singular(c.in); got != c.out {
			t.Errorf("singular(%q) = %q, want %q", c.in, got, c.out)
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
	model := schema.Schema{Tables: []schema.Table{{
		Schema:  "public",
		Name:    "things",
		Columns: []schema.Column{{Name: "weird", DataType: "hstore"}},
	}}}
	if _, err := Generate(model, Options{PackageName: "x"}); err == nil {
		t.Fatal("Generate() with unsupported type succeeded, want error")
	}
}
