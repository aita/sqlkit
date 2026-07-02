package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

// topLevelNames parses src and returns the package-level identifiers it
// declares (types, consts, vars, funcs). Duplicate declarations are valid Go
// syntax — only the type checker rejects them — so this is how a "redeclared in
// this block" collision is detected without a full go/types pass.
func topLevelNames(t *testing.T, name, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated %s does not parse: %v\n%s", name, err, src)
	}
	var names []string
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, id := range s.Names {
						names = append(names, id.Name)
					}
				}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil { // package-level func only
				names = append(names, decl.Name.Name)
			}
		}
	}
	return names
}

// TestGenerateNoIdentifierCollisions covers name collisions across the generated
// enum types, enum label consts, table binding vars, and DTOs: each must reduce
// to a distinct package-level Go identifier, or the generated package fails to
// compile ("redeclared in this block").
func TestGenerateNoIdentifierCollisions(t *testing.T) {
	cases := map[string]meta.Metadata{
		"two enums map to one Go type": {
			Enums: []meta.Enum{
				{Schema: "public", Name: "mood", Values: []string{"happy"}},
				{Schema: "public", Name: "mood_", Values: []string{"sad"}},
			},
		},
		"enum type vs table binding var": {
			Tables: []meta.Table{{Schema: meta.Schema{Name: "public"}, Name: "mood",
				Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}}}},
			Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy"}}},
		},
		"enum label const vs DTO type": {
			Tables: []meta.Table{{Schema: meta.Schema{Name: "public"}, Name: "user_admins",
				Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}}}},
			Enums: []meta.Enum{{Schema: "public", Name: "user", Values: []string{"admin"}}},
		},
	}
	for name, md := range cases {
		t.Run(name, func(t *testing.T) {
			files, err := Generate(md, Options{PackageName: "appdb", EmitDTO: true})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			seen := map[string]string{}
			for _, f := range files {
				if !strings.HasSuffix(f.Name, ".go") {
					continue
				}
				for _, id := range topLevelNames(t, f.Name, string(f.Content)) {
					if prev, dup := seen[id]; dup {
						t.Errorf("identifier %q declared in both %s and %s (generated code won't compile)", id, prev, f.Name)
					}
					seen[id] = f.Name
				}
			}
		})
	}
}

// structFieldNames returns the field names declared directly on the named
// struct type in src (not promoted/embedded fields), for detecting a
// duplicate field within one struct — a case topLevelNames can't see, since
// the struct's own type name is declared only once no matter how many
// duplicate fields it holds inside.
func structFieldNames(t *testing.T, name, src, structName string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated %s does not parse: %v\n%s", name, err, src)
	}
	var names []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != structName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, id := range field.Names {
					names = append(names, id.Name)
				}
			}
		}
	}
	return names
}

// TestGenerateNoColumnRelationFieldCollisions covers the <Var>Filter struct,
// which combines a table's (deduped) column fields and its relation fields in
// one struct declaration. buildRelations reserves column field names to keep
// a relation from colliding with one, but it must reserve the same
// post-dedup names buildTable actually assigns — not each column's raw,
// undeduped fieldName — or a relation name that happens to equal a renamed
// (suffixed) column field produces a duplicate struct field (syntactically
// legal Go, but "redeclared in this block" once type-checked).
//
// books has two columns colliding on the Go field "Author" ("author" and
// "author_", deduped to Author and Author2) plus a single-column FK
// "author2_id" whose derived relation name is also "Author2".
func TestGenerateNoColumnRelationFieldCollisions(t *testing.T) {
	md := meta.Metadata{
		Tables: []meta.Table{
			{Schema: meta.Schema{Name: "public"}, Name: "authors",
				Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}}},
			{Schema: meta.Schema{Name: "public"}, Name: "books",
				Columns: []meta.Column{
					{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
					{Name: "author", Type: meta.Of("postgres", "text", 0, 0, 0)},
					{Name: "author_", Type: meta.Of("postgres", "text", 0, 0, 0)},
					{Name: "author2_id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
				},
				ForeignKeys: []meta.ForeignKey{{Name: "books_author2_id_fkey", Columns: []string{"author2_id"},
					RefSchema: meta.Schema{Name: "public"}, RefTable: "authors", RefColumns: []string{"id"}}}},
		},
	}
	files, err := Generate(md, Options{PackageName: "appdb", EmitRelations: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f.Name, ".go") {
			continue
		}
		for _, name := range structFieldNames(t, f.Name, string(f.Content), "BooksFilter") {
			if seen[name] {
				t.Errorf("field %q declared twice in BooksFilter (generated code won't compile)", name)
			}
			seen[name] = true
		}
	}
}

// TestGenerateNoRelationTypeCollisions covers the per-relation struct types the
// relational template declares (<Var><Field>Rel, relationModel.RelType). They are
// deduped only within a table's relation set, not against the package-wide enum /
// DTO names, so an enum or DTO whose Go name maps to a relation type emitted a
// second declaration of it (`redeclared in this block`). Two tables joined by a
// foreign key yield relation types PostsUserRel (forward) and UsersPostsRel
// (reverse).
func TestGenerateNoRelationTypeCollisions(t *testing.T) {
	relTables := []meta.Table{
		{Schema: meta.Schema{Name: "public"}, Name: "users",
			Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}}},
		{Schema: meta.Schema{Name: "public"}, Name: "posts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
				{Name: "user_id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
			},
			ForeignKeys: []meta.ForeignKey{{Name: "posts_user_id_fkey", Columns: []string{"user_id"},
				RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}}}},
	}
	cases := []struct {
		name string
		md   meta.Metadata
	}{
		{"enum vs forward relation type", meta.Metadata{Tables: relTables,
			Enums: []meta.Enum{{Schema: "public", Name: "posts_user_rel", Values: []string{"a"}}}}},
		{"enum vs reverse relation type", meta.Metadata{Tables: relTables,
			Enums: []meta.Enum{{Schema: "public", Name: "users_posts_rel", Values: []string{"a"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, err := Generate(tc.md, Options{PackageName: "appdb", EmitRelations: true, EmitDTO: true})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			seen := map[string]string{}
			for _, f := range files {
				if !strings.HasSuffix(f.Name, ".go") {
					continue
				}
				for _, id := range topLevelNames(t, f.Name, string(f.Content)) {
					if id == "_" {
						continue
					}
					if prev, dup := seen[id]; dup {
						t.Errorf("identifier %q declared in both %s and %s (generated code won't compile)", id, prev, f.Name)
					}
					seen[id] = f.Name
				}
			}
		})
	}
}

// parseFile asserts the generated source parses and returns it.
func parseFile(t *testing.T, name, src string) string {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), name, src, parser.AllErrors); err != nil {
		t.Fatalf("generated %s does not parse: %v\n--- source ---\n%s", name, err, src)
	}
	return src
}

func TestGenerateEnums(t *testing.T) {
	md := meta.Metadata{
		Tables: []meta.Table{{
			Schema:  meta.Schema{Name: "public"},
			Name:    "users",
			Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}},
		}},
		Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "very_sad"}}},
	}
	files, err := Generate(md, Options{PackageName: "appdb"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var enums string
	for _, f := range files {
		if f.Name == "enums.go" {
			enums = string(f.Content)
		}
	}
	if enums == "" {
		t.Fatal("no enums.go generated")
	}
	// gofmt aligns the const block, so match each token rather than exact spacing.
	for _, want := range []string{
		"type Mood string",
		"MoodHappy", `Mood = "happy"`,
		"MoodVerySad", `Mood = "very_sad"`,
	} {
		if !strings.Contains(enums, want) {
			t.Fatalf("enums.go missing %q in:\n%s", want, enums)
		}
	}
}

func TestGenerateEnumColumn(t *testing.T) {
	// A column whose DB type names an enum maps to the generated Go enum type;
	// a nullable one becomes a pointer. meta.Of("postgres","mood") stands in for
	// a decl.EnumType("mood") column (both resolve to engine name "mood").
	md := meta.Metadata{
		Enums: []meta.Enum{{Name: "mood", Values: []string{"happy", "sad"}}},
		Tables: []meta.Table{{
			Name: "users",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
				{Name: "mood", Type: meta.Of("postgres", "mood", 0, 0, 0)},
				{Name: "backup_mood", Type: meta.Of("postgres", "mood", 0, 0, 0), Nullable: true},
			},
		}},
	}
	files, err := Generate(md, Options{PackageName: "appdb"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var tables string
	for _, f := range files {
		if f.Name == "tables.go" {
			tables = string(f.Content)
		}
	}
	for _, want := range []string{"Column[Mood]", "Column[*Mood]"} {
		if !strings.Contains(tables, want) {
			t.Fatalf("tables.go missing %q in:\n%s", want, tables)
		}
	}
}

// TestGenerateArrayEnumColumn covers a column whose DB type is an array of an
// enum (mood[]): the generated column must be a slice of the enum type,
// Column[[]Mood], not the bare element type Column[Mood]. meta.OfArray stands
// in for a decl.Array(decl.EnumType("mood")) column — the resolved Engine
// carries Array=true, same as the introspection would produce for mood[].
func TestGenerateArrayEnumColumn(t *testing.T) {
	md := meta.Metadata{
		Enums: []meta.Enum{{Name: "mood", Values: []string{"happy", "sad"}}},
		Tables: []meta.Table{{
			Name: "users",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true},
				{Name: "moods", Type: meta.OfArray("postgres", "mood", 0, 0, 0)},
			},
		}},
	}
	files, err := Generate(md, Options{PackageName: "appdb"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var tables string
	for _, f := range files {
		if f.Name == "tables.go" {
			tables = string(f.Content)
		}
	}
	if !strings.Contains(tables, "Column[[]Mood]") {
		t.Fatalf("tables.go missing %q in:\n%s", "Column[[]Mood]", tables)
	}
	// A slice is already nilable, so applyNullable must not wrap it again into
	// *[]Mood — guard against a regression that double-wraps array columns.
	if strings.Contains(tables, "Column[*[]Mood]") {
		t.Fatalf("tables.go must not double-wrap array-of-enum column as Column[*[]Mood]:\n%s", tables)
	}
}

// TestGenerateEnumCollidingLabels covers the enum analogue of the colliding
// column class: distinct labels that map to the same Go name ("foo", "foo_" and
// "foo__" all yield "Foo") must dedupe into distinct const identifiers rather
// than redeclaring one. Without dedup the const block has three KindFoo decls
// and does not compile.
func TestGenerateEnumCollidingLabels(t *testing.T) {
	if goName("foo") != goName("foo_") || goName("foo") != goName("foo__") {
		t.Fatalf("precondition: expected foo, foo_, foo__ to share a Go name, got %q %q %q",
			goName("foo"), goName("foo_"), goName("foo__"))
	}
	md := meta.Metadata{
		Enums: []meta.Enum{{Name: "kind", Values: []string{"foo", "foo_", "foo__"}}},
	}
	files, err := Generate(md, Options{PackageName: "appdb"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) != 1 || files[0].Name != "enums.go" {
		t.Fatalf("expected only enums.go, got %d files: %+v", len(files), files)
	}
	src := parseFile(t, "enums.go", string(files[0].Content))
	// The natural const keeps "KindFoo"; the two collisions dedupe to KindFoo2
	// and KindFoo3, each bound to its own label literal. gofmt aligns the const
	// block, so allow arbitrary intervening whitespace.
	for _, want := range []struct{ ident, label string }{
		{"KindFoo", "foo"},
		{"KindFoo2", "foo_"},
		{"KindFoo3", "foo__"},
	} {
		re := regexp.MustCompile(`\b` + want.ident + `\s+Kind = ` + regexp.QuoteMeta(`"`+want.label+`"`))
		if n := len(re.FindAllString(src, -1)); n != 1 {
			t.Errorf("expected exactly one `%s Kind = %q`, got %d:\n%s", want.ident, want.label, n, src)
		}
	}
}

// TestGenerateDTOEnumCollision covers a table DTO name colliding with an enum
// type name: enum "mood" and table "moods" both derive "Mood", which without a
// fix emits two `type Mood` declarations. The DTO must fall back to its
// "Row"-suffixed name so only the enum keeps the bare type.
func TestGenerateDTOEnumCollision(t *testing.T) {
	if goName(singular("moods")) != goName("mood") {
		t.Fatalf("precondition: expected DTO of moods and enum mood to share a Go name, got %q and %q",
			goName(singular("moods")), goName("mood"))
	}
	md := meta.Metadata{
		Tables: []meta.Table{{
			Schema:  meta.Schema{Name: "public"},
			Name:    "moods",
			Columns: []meta.Column{{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), PrimaryKey: true}},
		}},
		Enums: []meta.Enum{{Name: "mood", Values: []string{"happy", "sad"}}},
	}
	files, err := Generate(md, Options{PackageName: "appdb", EmitDTO: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var all strings.Builder
	for _, f := range files {
		all.WriteString(parseFile(t, f.Name, string(f.Content)))
		all.WriteString("\n")
	}
	src := all.String()
	enumDecl := regexp.MustCompile(`\btype Mood string\b`)
	if n := len(enumDecl.FindAllString(src, -1)); n != 1 {
		t.Errorf("expected exactly one enum `type Mood string`, got %d:\n%s", n, src)
	}
	dtoDecl := regexp.MustCompile(`\btype Mood struct\b`)
	if n := len(dtoDecl.FindAllString(src, -1)); n != 0 {
		t.Errorf("expected the DTO not to claim `type Mood struct` (it collides with the enum), got %d:\n%s", n, src)
	}
	dtoFallback := regexp.MustCompile(`\btype MoodRow struct\b`)
	if !dtoFallback.MatchString(src) {
		t.Errorf("expected the DTO to fall back to `type MoodRow struct`:\n%s", src)
	}
}

func TestGenerateEnumsOnly(t *testing.T) {
	md := meta.Metadata{
		Enums: []meta.Enum{{Name: "status", Values: []string{"on", "off"}}},
	}
	files, err := Generate(md, Options{PackageName: "appdb"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) != 1 || files[0].Name != "enums.go" {
		t.Fatalf("expected only enums.go, got %d files: %+v", len(files), files)
	}
}
