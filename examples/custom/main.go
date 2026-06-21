// Command custom demonstrates using the codegen package as a library to
// customize binding generation, instead of driving it through the
// `sqlkit generate` CLI.
//
// The CLI is the easy path: it builds the catalog and calls codegen.Generate
// with a fixed set of options. Calling the library directly buys you the things
// the CLI can't express — most importantly full control over the SQL→Go type
// mapping (codegen.TypeMapper) and the DTO naming scheme (Options.DTOName).
//
// The schema is the same blog schema as examples/generated, declared as a
// decl.Catalog in the schema package next to this file. Generating it through
// the CLI (examples/generated) yields the default types; generating it here with
// a customized mapper yields different bindings — uuid/numeric/jsonb columns and
// a status enum mapped to the domain types declared by hand in blogdb/types.go
// (UUID, Decimal, UserStatus) and to encoding/json.RawMessage, with a "Record"
// suffix on the DTOs. The local domain types keep the generated package
// compiling with no extra dependencies. Regenerate them with:
//
//	go generate ./examples/custom
//
// The same hooks accept third-party types just as well, e.g.
// {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"}; this example sticks
// to local types only to keep the module dependency-free.
package main

//go:generate go run .

import (
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/examples/custom/schema"
	"github.com/aita/sqlkit/meta"
)

// typeMapper customizes the SQL→Go type mapping. It layers the three override
// hooks of the default mapper, which are consulted most-specific first:
//
//  1. OverrideFunc      — programmatic, sees the whole table/column.
//  2. ColumnOverrides   — by "schema.table.column" / "table.column" / "column".
//  3. Overrides         — by lowercase SQL type name.
//
// Anything not matched falls through to the built-in type tables, so we only
// have to spell the columns we care about. Nullability is applied on top of the
// resolved type (a nullable column becomes a pointer), so overrides pin the
// bare type. A type whose GoType has no ImportPath is emitted unqualified, so it
// resolves to a type declared in the generated package itself — that is how the
// blogdb.UUID / blogdb.Decimal / UserStatus domain types are wired in.
func typeMapper() codegen.TypeMapper {
	return codegen.NewDefaultTypeMapper(codegen.TypeMapperConfig{
		// Map by SQL type: every uuid column to blogdb.UUID (rather than the
		// default string) and every numeric/decimal column to blogdb.Decimal.
		Overrides: map[string]codegen.GoType{
			"uuid":    {Name: "UUID"},
			"numeric": {Name: "Decimal"},
			"decimal": {Name: "Decimal"},
		},
		// Pin specific columns regardless of their SQL type. Keys may be fully
		// qualified or bare; the most specific match wins. Here the jsonb
		// metadata column (a []byte by default) becomes json.RawMessage.
		ColumnOverrides: map[string]codegen.GoType{
			"metadata": {ImportPath: "encoding/json", Name: "json.RawMessage"},
		},
		// Decide programmatically: map any column named "status" to a typed enum
		// named after its table (UserStatus), keeping the SQL type a plain
		// varchar. Returning ok=false falls through to the maps above.
		OverrideFunc: func(table meta.Table, column meta.Column, _ string) (codegen.GoType, bool, error) {
			if column.Name == "status" {
				return codegen.GoType{Name: goName(singular(table.Name)) + "Status"}, true, nil
			}
			return codegen.GoType{}, false, nil
		},
	})
}

// dtoName derives the DTO type name from a table. The default singularizes the
// table name ("users" -> "User"); here we add a "Record" suffix to match a
// house style ("users" -> "UserRecord").
func dtoName(table meta.Table) string {
	return goName(singular(table.Name)) + "Record"
}

func main() {
	// 1. Build the schema model from the catalog. The CLI does this for you; as
	//    a library you call Build yourself (or feed in a model from the inspector
	//    or one you assemble by hand).
	md, err := schema.Catalog.Build()
	if err != nil {
		log.Fatal(err)
	}

	// 2. Generate with our customizations wired into Options.
	files, err := codegen.Generate(md, codegen.Options{
		PackageName:     "blogdb",
		Dialect:         "postgres",
		EmitRelations:   true, // FK relation metadata + Query/Q entry points.
		EmitDTO:         true, // a row struct per table…
		DTOSeparateFile: true, // …written to its own dtos.go.
		TypeMapper:      typeMapper(),
		DTOName:         dtoName,
	})
	if err != nil {
		log.Fatal(err)
	}

	// 3. Write the result next to this source file, into the blogdb package whose
	//    hand-written types.go declares the domain types the bindings reference.
	outDir := filepath.Join(sourceDir(), "blogdb")
	if err := codegen.Write(outDir, files); err != nil {
		log.Fatal(err)
	}
	for _, file := range files {
		fmt.Printf("wrote %s\n", filepath.Join("blogdb", file.Name))
	}
}

// sourceDir returns the directory of this source file, so generation targets the
// example's blogdb package regardless of the working directory it runs from.
func sourceDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(file)
}

// goName converts a snake_case identifier to an exported Go name, the same way
// codegen names its generated identifiers. codegen keeps its own version
// unexported, so we reimplement the slice of it we need for the DTO namer and
// the enum-type names above.
func goName(name string) string {
	var sb strings.Builder
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == ' ' || r == '-'
	}) {
		if part == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(part[:1]))
		sb.WriteString(strings.ToLower(part[1:]))
	}
	return sb.String()
}

// singular is a best-effort singularizer for the table names in this example.
func singular(name string) string {
	if strings.HasSuffix(name, "ies") {
		return name[:len(name)-3] + "y"
	}
	if strings.HasSuffix(name, "s") {
		return name[:len(name)-1]
	}
	return name
}
