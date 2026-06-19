package codegen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

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
