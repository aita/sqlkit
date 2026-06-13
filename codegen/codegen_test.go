package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixtureTables mirrors examples/blog/schema.sql: users and posts with a
// foreign key from posts.user_id to users.id.
func fixtureTables() []meta.Table {
	return []meta.Table{
		{
			Schema: meta.Schema{Name: "public"},
			Name:   "users",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true, HasDefault: true},
				{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0)},
				{Name: "name", Type: meta.Of("postgres", "text", 0, 0, 0), Nullable: true},
				{Name: "created_at", Type: meta.Of("postgres", "timestamptz", 0, 0, 0), HasDefault: true},
			},
		},
		{
			Schema: meta.Schema{Name: "public"},
			Name:   "posts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true, HasDefault: true},
				{Name: "user_id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
				{Name: "title", Type: meta.Of("postgres", "text", 0, 0, 0)},
				{Name: "body", Type: meta.Of("postgres", "text", 0, 0, 0), Nullable: true},
				{Name: "published", Type: meta.Of("postgres", "bool", 0, 0, 0), HasDefault: true},
			},
			ForeignKeys: []meta.ForeignKey{
				{Name: "posts_user_id_fkey", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}
}

func generateSingle(t *testing.T, tables []meta.Table, opts Options) []byte {
	t.Helper()
	files, err := generate(meta.Meta{Tables: tables}, opts)
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
	got := generateSingle(t, fixtureTables(), Options{PackageName: "blogdb"})
	checkGolden(t, "blogdb.go.golden", got)
}

func TestGenerateWithDTOGolden(t *testing.T) {
	got := generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", EmitDTO: true})
	checkGolden(t, "blogdb_dto.go.golden", got)
}

func TestGenerateFilePerTable(t *testing.T) {
	files, err := generate(meta.Meta{Tables: fixtureTables()}, Options{PackageName: "blogdb", FilePerTable: true, EmitDTO: true, DTOSeparateFile: true})
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
	files, err := generate(meta.Meta{Tables: fixtureTables()}, Options{PackageName: "blogdb", Tables: []string{"users"}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := string(files[0].Content)
	if !strings.Contains(body, "UsersTable") || strings.Contains(body, "PostsTable") {
		t.Fatalf("Tables filter did not restrict output:\n%s", body)
	}
}

func TestBindingsAreNotAnORM(t *testing.T) {
	body := string(generateSingle(t, fixtureTables(), Options{PackageName: "blogdb"}))
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
	tables := []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "account",
		Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}},
	}}
	body := string(generateSingle(t, tables, Options{PackageName: "x", EmitDTO: true}))
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
	body := string(generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", TypeMapper: mapper}))
	if !strings.Contains(body, "sqlkit.Column[uint64]") {
		t.Fatalf("custom type mapper override not applied:\n%s", body)
	}
}

func TestMySQLTypeMapping(t *testing.T) {
	tables := []meta.Table{{
		Schema: meta.Schema{Name: "app"},
		Name:   "items",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("mysql", "bigint", 0, 0, 0), PrimaryKey: true, HasDefault: true},
			{Name: "qty", Type: meta.Of("mysql", "int", 0, 0, 0)},
			{Name: "label", Type: meta.Of("mysql", "varchar", 0, 0, 0)},
			{Name: "created_at", Type: meta.Of("mysql", "datetime", 0, 0, 0)},
		},
	}}
	body := string(generateSingle(t, tables, Options{PackageName: "appdb", Dialect: "mysql"}))
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

func TestGenerateComments(t *testing.T) {
	tables := []meta.Table{
		{
			Schema:  meta.Schema{Name: "public"},
			Name:    "users",
			Comment: "Registered accounts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
				{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0), Comment: "Unique login address"},
			},
		},
	}
	got := string(generateSingle(t, tables, Options{PackageName: "appdb", EmitDTO: true}))
	for _, want := range []string{
		"// UsersTable is the typed binding for table public.users.\n// Registered accounts\ntype UsersTable struct {",
		"\t// Unique login address\n\tEmail sqlkit.Column[string]",
		"// Registered accounts\ntype User struct {",
		"\t// Unique login address\n\tEmail string `sqlkit:\"email\"`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated code missing %q\n%s", want, got)
		}
	}
}

func TestGenerateUnknownTypeFails(t *testing.T) {
	catalog := meta.Meta{Tables: []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "things",
		Columns: []meta.Column{{Name: "weird", Type: meta.Of("postgres", "hstore", 0, 0, 0)}},
	}}}
	if _, err := generate(catalog, Options{PackageName: "x"}); err == nil {
		t.Fatal("generate() with unsupported type succeeded, want error")
	}
}
