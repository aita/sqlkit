package codegen

import (
	"flag"
	"fmt"
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

// relationFixtureTables extends fixtureTables with a profiles table whose
// user_id is a unique foreign key to users, exercising belongs-to, has-many
// (posts), and has-one (profile) relations.
func relationFixtureTables() []meta.Table {
	tables := fixtureTables()
	tables = append(tables, meta.Table{
		Schema: meta.Schema{Name: "public"},
		Name:   "profiles",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true, HasDefault: true},
			{Name: "user_id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
			{Name: "bio", Type: meta.Of("postgres", "text", 0, 0, 0), Nullable: true},
			{Name: "public", Type: meta.Of("postgres", "bool", 0, 0, 0), HasDefault: true},
		},
		ForeignKeys: []meta.ForeignKey{
			{Name: "profiles_user_id_fkey", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}},
		},
		Indexes: []meta.Index{
			{Name: "profiles_user_id_key", Columns: []string{"user_id"}, Unique: true},
		},
	})
	return tables
}

func generateSingle(t *testing.T, tables []meta.Table, opts Options) []byte {
	t.Helper()
	files, err := Generate(meta.Metadata{Tables: tables}, opts)
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

func TestGenerateRelationsGolden(t *testing.T) {
	got := generateSingle(t, relationFixtureTables(), Options{PackageName: "blogdb", EmitRelations: true})
	checkGolden(t, "blogdb_relations.go.golden", got)

	body := string(got)
	for _, want := range []string{
		"func (t UsersTable) Query(ex sqlkit.Querier) sqlkit.SelectQuery",
		"func (t UsersTable) Q(ex sqlkit.Querier) sqlkit.SelectQuery",
		"func (t UsersTable) Insert(ex sqlkit.Querier) sqlkit.InsertQuery",
		"func (t UsersTable) Update(ex sqlkit.Querier) sqlkit.UpdateQuery",
		"func (t UsersTable) Delete(ex sqlkit.Querier) sqlkit.DeleteQuery",
		"type UsersFilter struct",
		`sqlkit:"email"`, // column field carries its column tag
		"*PostsFilter",   // relation field is a nested filter pointer
		"type PostsFilter struct",
		// Target returns the target's package var (a RelationSource), so nested
		// relation filters can recurse through the target's own relations.
		"func (r UsersPostsRel) Target() sqlkit.Table { return Posts }",
		"type UsersColumns struct",
		"func newUsersColumns() UsersColumns",
		"Profile UsersProfileRel", // has-one (unique FK)
		"Posts   UsersPostsRel",   // has-many
		"User PostsUserRel",       // belongs-to
		"return sqlkit.ToOne",
		"return sqlkit.ToMany",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("relations golden missing %q", want)
		}
	}
	// Profile is has-one, Posts is has-many.
	if !strings.Contains(body, "func (r UsersProfileRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToOne }") {
		t.Errorf("Profile relation should be ToOne (unique FK)\n%s", body)
	}
	if !strings.Contains(body, "func (r UsersPostsRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToMany }") {
		t.Errorf("Posts relation should be ToMany\n%s", body)
	}
}

func TestGenerateSelfReferentialRelations(t *testing.T) {
	tables := []meta.Table{{
		Schema: meta.Schema{Name: "public"},
		Name:   "employees",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true, HasDefault: true},
			{Name: "manager_id", Type: meta.Of("postgres", "int8", 0, 0, 0), Nullable: true},
			{Name: "name", Type: meta.Of("postgres", "text", 0, 0, 0)},
		},
		ForeignKeys: []meta.ForeignKey{
			{Name: "employees_manager_id_fkey", Columns: []string{"manager_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "employees", RefColumns: []string{"id"}},
		},
	}}
	body := string(generateSingle(t, tables, Options{PackageName: "hrdb", EmitRelations: true}))

	for _, want := range []string{
		// Forward belongs-to "Manager" with the target aliased "manager".
		"type EmployeesManagerRel struct{ EmployeesColumns }",
		`EmployeesColumns: newEmployeesColumns().As("manager")`,
		"func (r EmployeesManagerRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToOne }",
		// JoinOn equates the aliased target id to the base manager_id.
		"sqlkit.Cond(sql.Eq(r.ID, newEmployeesColumns().ManagerID))",
		// A self-referential relation keeps the aliased columns value as Target (not
		// the package var) so the self-join stays unambiguous.
		"func (r EmployeesManagerRel) Target() sqlkit.Table { return r.EmployeesColumns }",
		// Reverse has-many; alias suffixed to avoid colliding with the table name.
		"func (r EmployeesEmployeesRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToMany }",
		`As("employees_ref")`,
		"sqlkit.Cond(sql.Eq(r.ManagerID, newEmployeesColumns().ID))",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("self-referential relations missing %q\n%s", want, body)
		}
	}
}

func TestGenerateRelationsNoForeignKeysOmitsSQLImport(t *testing.T) {
	// A relation-aware binding for a table without foreign keys emits the
	// Query surface but no JoinOn expressions, so it must not import sql — an
	// unused import would make the generated file uncompilable.
	got := string(generateSingle(t, fixtureTables()[:1], Options{PackageName: "blogdb", EmitRelations: true}))
	if strings.Contains(got, `"github.com/aita/sqlkit/sql"`) {
		t.Errorf("binding without relations must not import sql (unused)\n%s", got)
	}
}

func TestGenerateWithDTOGolden(t *testing.T) {
	got := generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", EmitDTO: true})
	checkGolden(t, "blogdb_dto.go.golden", got)
}

func TestGenerateFilePerTable(t *testing.T) {
	files, err := Generate(meta.Metadata{Tables: fixtureTables()}, Options{PackageName: "blogdb", FilePerTable: true, EmitDTO: true, DTOSeparateFile: true})
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

func TestFileNameForCustomNames(t *testing.T) {
	// FileNameFor implies per-table output and customizes each file's path.
	files, err := Generate(meta.Metadata{Tables: fixtureTables()}, Options{
		PackageName:     "blogdb",
		FileNameFor:     func(table meta.Table) string { return "gen_" + table.Name + ".go" },
		EmitDTO:         true,
		DTOSeparateFile: true,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Name] = true
	}
	for _, want := range []string{"gen_users.go", "gen_users_dto.go", "gen_posts.go", "gen_posts_dto.go"} {
		if !got[want] {
			t.Errorf("missing generated file %q; got %v", want, got)
		}
	}
}

func TestWriteCreatesSubdirectories(t *testing.T) {
	files := []File{{Name: "users/users.go", Content: []byte("package users\n")}}
	dir := t.TempDir()
	if err := Write(dir, files); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "users", "users.go"))
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != "package users\n" {
		t.Errorf("unexpected file contents: %q", got)
	}
}

func TestGenerateTablesFilter(t *testing.T) {
	files, err := Generate(meta.Metadata{Tables: fixtureTables()}, Options{PackageName: "blogdb", Tables: []string{"users"}})
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
	for _, banned := range []string{"sqlkit.Querier", ") Get(", ") User()", ") Posts()", "sqlkit.With"} {
		if strings.Contains(body, banned) {
			t.Errorf("generated binding should not contain %q:\n%s", banned, body)
		}
	}
	if !strings.Contains(body, `sqlkit.NewColumn[int64](info, "id")`) {
		t.Errorf("expected plain NewColumn call:\n%s", body)
	}
}

// By default an exact numeric (numeric/decimal) column maps to
// shopspring/decimal, on every dialect, without any override.
func TestDefaultDecimalMapping(t *testing.T) {
	cases := []struct {
		dialect string
		typ     meta.Type
	}{
		{"postgres", meta.Of("postgres", "numeric", 0, 10, 2)},
		{"mysql", meta.Of("mysql", "decimal", 0, 10, 2)},
	}
	for _, tc := range cases {
		tables := []meta.Table{{
			Schema: meta.Schema{Name: "public"},
			Name:   "items",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of(tc.dialect, intType(tc.dialect), 0, 0, 0), PrimaryKey: true},
				{Name: "price", Type: tc.typ},
			},
		}}
		body := string(generateSingle(t, tables, Options{PackageName: "shopdb", Dialect: tc.dialect}))
		for _, want := range []string{
			`sqlkit.NewColumn[decimal.Decimal](info, "price")`,
			`"github.com/shopspring/decimal"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("[%s] default decimal mapping missing %q:\n%s", tc.dialect, want, body)
			}
		}
	}
}

func intType(dialect string) string {
	switch dialect {
	case "mysql":
		return "bigint"
	default:
		return "int8"
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

func TestCustomBindingName(t *testing.T) {
	// BindingName overrides the binding's base identifier; every binding type
	// and var name is formed off it.
	body := string(generateSingle(t, fixtureTables(), Options{
		PackageName: "blogdb",
		BindingName: func(table meta.Table) string { return goName(singular(table.Name)) + "Tbl" },
	}))
	for _, want := range []string{
		"type UserTblTable struct",
		"var UserTbl = func() UserTblTable",
		"func (t UserTblTable) As(alias string) UserTblTable",
		"var _ sqlkit.Table = UserTbl",
		"type PostTblTable struct",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected renamed binding %q:\n%s", want, body)
		}
	}
	// The default-derived names must no longer appear.
	for _, banned := range []string{"type UsersTable struct", "var Users ="} {
		if strings.Contains(body, banned) {
			t.Errorf("default binding name %q should be replaced:\n%s", banned, body)
		}
	}
}

func TestCustomBindingNameWithRelations(t *testing.T) {
	// In the relation-aware layout, BindingName must flow through the columns
	// type, constructor, relation types, and relation target references so the
	// generated code stays consistent and compiles.
	body := string(generateSingle(t, relationFixtureTables(), Options{
		PackageName:   "blogdb",
		EmitRelations: true,
		BindingName:   func(table meta.Table) string { return "Tbl" + goName(table.Name) },
	}))
	for _, want := range []string{
		"type TblUsersColumns struct",
		"func newTblUsersColumns() TblUsersColumns",
		"type TblUsersTable struct",
		"type TblUsersRels struct",
		"type TblUsersFilter struct",
		// Reverse relation on users targets posts: target columns and ctor use
		// the renamed posts binding.
		"TblPostsColumns: newTblPostsColumns()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected renamed relation binding %q:\n%s", want, body)
		}
	}
}

func TestBindingNameEmptyFallsBackToDefault(t *testing.T) {
	// Returning "" for a table defers to the default derivation; a non-empty
	// return renames just that table.
	body := string(generateSingle(t, fixtureTables(), Options{
		PackageName: "blogdb",
		BindingName: func(table meta.Table) string {
			if table.Name == "users" {
				return "Account"
			}
			return "" // posts falls back to the default
		},
	}))
	for _, want := range []string{"type AccountTable struct", "var Account = func() AccountTable", "type PostsTable struct"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in output:\n%s", want, body)
		}
	}
}

func TestCustomTypeMapper(t *testing.T) {
	mapper := NewDefaultTypeMapper(TypeMapperConfig{Overrides: map[string]GoType{
		"timestamptz": {ImportPath: "time", Name: "time.Time"},
		"int8":        {Name: "uint64"},
	}})
	body := string(generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", TypeMapper: mapper}))
	if !strings.Contains(body, "sqlkit.Column[uint64]") {
		t.Fatalf("custom type mapper override not applied:\n%s", body)
	}
}

func TestColumnNameTypeMapper(t *testing.T) {
	mapper := NewDefaultTypeMapper(TypeMapperConfig{ColumnOverrides: map[string]GoType{
		// Fully qualified: only public.users.id, not posts.id.
		"public.users.id": {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"},
		// Bare column name: every "title" column.
		"title": {Name: "myString"},
	}})
	body := string(generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", TypeMapper: mapper}))
	if !strings.Contains(body, `sqlkit.NewColumn[uuid.UUID](info, "id")`) {
		t.Errorf("qualified column override not applied to users.id:\n%s", body)
	}
	// posts.id is not overridden, so it stays the default int64.
	if !strings.Contains(body, `sqlkit.NewColumn[int64](info, "id")`) {
		t.Errorf("posts.id should keep the default int64 mapping:\n%s", body)
	}
	if !strings.Contains(body, `sqlkit.NewColumn[myString](info, "title")`) {
		t.Errorf("bare column override not applied to title:\n%s", body)
	}
	if !strings.Contains(body, `"github.com/google/uuid"`) {
		t.Errorf("override import not emitted:\n%s", body)
	}
}

func TestColumnOverrideNullablePointer(t *testing.T) {
	// A name-based override on a nullable column still becomes a pointer.
	mapper := NewDefaultTypeMapper(TypeMapperConfig{ColumnOverrides: map[string]GoType{
		"name": {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"},
	}})
	body := string(generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", TypeMapper: mapper}))
	if !strings.Contains(body, `sqlkit.NewColumn[*uuid.UUID](info, "name")`) {
		t.Errorf("nullable column override should be a pointer:\n%s", body)
	}
}

// pinnedType is a meta.Type that fixes an engine name and pins a Go type,
// mirroring what decl's DataType.Go produces, to exercise the GoTyped path of
// DefaultTypeMapper without importing decl.
type pinnedType struct {
	name   string
	goType meta.GoType
}

func (p pinnedType) Engine(string) (meta.Engine, bool) { return meta.Engine{Name: p.name}, true }
func (p pinnedType) GoType(string) (meta.GoType, bool) { return p.goType, true }

func TestPinnedGoTypeFromType(t *testing.T) {
	// A column whose meta.Type pins a Go type generates without an override, even
	// for an engine type the built-in tables don't know.
	tables := []meta.Table{{
		Schema: meta.Schema{Name: "public"},
		Name:   "things",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
			{Name: "tag", Type: pinnedType{name: "citext", goType: meta.GoType{Name: "string"}}},
			{Name: "ref", Type: pinnedType{name: "uuid", goType: meta.GoType{ImportPath: "github.com/google/uuid", Name: "uuid.UUID"}}},
		},
	}}
	body := string(generateSingle(t, tables, Options{PackageName: "x"}))
	if !strings.Contains(body, `sqlkit.NewColumn[string](info, "tag")`) {
		t.Errorf("pinned string type not used for citext:\n%s", body)
	}
	if !strings.Contains(body, `sqlkit.NewColumn[uuid.UUID](info, "ref")`) {
		t.Errorf("pinned uuid.UUID type not used:\n%s", body)
	}
}

func TestExplicitOverrideBeatsPinnedType(t *testing.T) {
	// A codegen-side Override wins over a Go type pinned on the meta.Type.
	tables := []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "things",
		Columns: []meta.Column{{Name: "tag", Type: pinnedType{name: "citext", goType: meta.GoType{Name: "string"}}}},
	}}
	mapper := NewDefaultTypeMapper(TypeMapperConfig{Overrides: map[string]GoType{"citext": {Name: "myText"}}})
	body := string(generateSingle(t, tables, Options{PackageName: "x", TypeMapper: mapper}))
	if !strings.Contains(body, `sqlkit.NewColumn[myText](info, "tag")`) {
		t.Errorf("explicit override should beat pinned type:\n%s", body)
	}
}

func TestTypeMapperConfigOverrideFunc(t *testing.T) {
	// OverrideFunc is consulted first; ok=false falls through to the rest.
	mapper := NewDefaultTypeMapper(TypeMapperConfig{
		Overrides: map[string]GoType{"int8": {Name: "shouldNotWin"}},
		OverrideFunc: func(table meta.Table, column meta.Column, dialect string) (GoType, bool, error) {
			if column.Name == "id" {
				return GoType{ImportPath: "github.com/google/uuid", Name: "uuid.UUID"}, true, nil
			}
			return GoType{}, false, nil
		},
	})
	body := string(generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", TypeMapper: mapper}))
	// OverrideFunc wins for id, even over the int8 Override.
	if !strings.Contains(body, `sqlkit.NewColumn[uuid.UUID](info, "id")`) {
		t.Errorf("OverrideFunc should resolve id:\n%s", body)
	}
	// title falls through OverrideFunc (ok=false) to the default string mapping.
	if !strings.Contains(body, `sqlkit.NewColumn[string](info, "title")`) {
		t.Errorf("OverrideFunc fallthrough should reach the default mapping:\n%s", body)
	}
}

func TestTypeMapperConfigOverrideFuncError(t *testing.T) {
	mapper := NewDefaultTypeMapper(TypeMapperConfig{
		OverrideFunc: func(table meta.Table, column meta.Column, dialect string) (GoType, bool, error) {
			return GoType{}, false, fmt.Errorf("boom")
		},
	})
	if _, err := Generate(meta.Metadata{Tables: fixtureTables()}, Options{PackageName: "x", TypeMapper: mapper}); err == nil {
		t.Fatal("Generate() should propagate the OverrideFunc error")
	}
}

func TestTypeMapperFuncSeesTable(t *testing.T) {
	// The mapper receives the table, so a function mapper can key on its name.
	mapper := TypeMapperFunc(func(table meta.Table, column meta.Column, dialect string) (GoType, error) {
		if table.Name == "posts" && column.Name == "title" {
			return GoType{Name: "headline"}, nil
		}
		return DefaultTypeMapper{}.GoTypeFor(table, column, dialect)
	})
	body := string(generateSingle(t, fixtureTables(), Options{PackageName: "blogdb", TypeMapper: mapper}))
	if !strings.Contains(body, `sqlkit.NewColumn[headline](info, "title")`) {
		t.Errorf("function mapper did not see the table name:\n%s", body)
	}
}

func TestArrayTypeMapping(t *testing.T) {
	// An array column maps to a Go slice of the element's type, through the
	// built-in tables, the Overrides map (keyed by the element type), and a
	// nullable array (which stays a slice, not a pointer).
	tables := []meta.Table{{
		Schema: meta.Schema{Name: "public"},
		Name:   "docs",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true, HasDefault: true},
			{Name: "tags", Type: meta.OfArray("postgres", "text", 0, 0, 0)},
			{Name: "scores", Type: meta.OfArray("postgres", "int4", 0, 0, 0), Nullable: true},
			{Name: "amounts", Type: meta.OfArray("postgres", "numeric", 0, 10, 2)},
		},
	}}
	mapper := NewDefaultTypeMapper(TypeMapperConfig{Overrides: map[string]GoType{
		"numeric": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
	}})
	body := string(generateSingle(t, tables, Options{PackageName: "docsdb", TypeMapper: mapper}))
	for _, want := range []string{
		`sqlkit.NewColumn[[]string](info, "tags")`,
		`sqlkit.NewColumn[[]int32](info, "scores")`,
		`sqlkit.NewColumn[[]decimal.Decimal](info, "amounts")`,
		`"github.com/shopspring/decimal"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("array mapping missing %q:\n%s", want, body)
		}
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
	md := meta.Metadata{Tables: []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "things",
		Columns: []meta.Column{{Name: "weird", Type: meta.Of("postgres", "hstore", 0, 0, 0)}},
	}}}
	if _, err := Generate(md, Options{PackageName: "x"}); err == nil {
		t.Fatal("Generate() with unsupported type succeeded, want error")
	}
}

func TestGeneratorIDStableAndStructured(t *testing.T) {
	id := GeneratorID()
	if id != GeneratorID() {
		t.Fatal("GeneratorID is not deterministic")
	}
	// fmt<format>+tpl<hash>+sqlkit<version>
	for _, part := range []string{"fmt" + formatVersion + "+", "+tpl", "+sqlkit"} {
		if !strings.Contains(id, part) {
			t.Fatalf("GeneratorID %q missing %q", id, part)
		}
	}
}

func TestSqlkitVersionFallsBackToDevel(t *testing.T) {
	// Under `go test` of the main module the version is unversioned, so the
	// fingerprint reports the devel fallback rather than an empty string.
	if got := sqlkitVersion(); got == "" {
		t.Fatal("sqlkitVersion returned empty; want a version or (devel)")
	}
}
