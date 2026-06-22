package codegen

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

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
