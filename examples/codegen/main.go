// Command codegen demonstrates using the codegen package as a library to
// customize binding generation, instead of driving it through the
// `sqlkit generate` CLI.
//
// The CLI is the easy path: it builds the catalog and calls codegen.Generate
// with a fixed set of options. Calling the library directly buys you the things
// the CLI can't express — most importantly full control over the SQL→Go type
// mapping (codegen.TypeMapper) and the DTO naming scheme (Options.DTOName).
//
// This program declares a small catalog, then generates bindings + DTOs from it
// with a customized type mapper and DTO namer, and writes them to the appdb
// package next to this file. The customizations map columns to the domain types
// declared by hand in appdb/types.go (UUID, Decimal, UserStatus, OrderStatus)
// and to encoding/json.RawMessage, so the generated package compiles with no
// extra dependencies. Regenerate them with:
//
//	go generate ./examples/codegen
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
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// catalog declares the schema we generate from. In a real project this is its
// own package (see examples/generated/schema) and the source of truth for both the
// bindings and the DDL.
func catalog() *decl.Catalog {
	cat := decl.NewCatalog()
	public := cat.Schema("public")

	public.Table("users",
		decl.TableComment("Registered accounts"),
		// A uuid primary key: by default codegen maps uuid -> string. We override
		// it to the appdb.UUID domain type below via ColumnOverrides.
		decl.Column("id", decl.UUID, decl.PrimaryKey()),
		decl.Column("email", decl.VarChar(255), decl.Unique()),
		// A money amount: numeric defaults to string. We map every numeric column
		// to appdb.Decimal below via Overrides.
		decl.Column("balance", decl.Decimal(18, 2), decl.Default(sql.Raw("0"))),
		// A free-form JSON blob: jsonb defaults to []byte. We map it to
		// json.RawMessage below via ColumnOverrides.
		decl.Column("metadata", decl.JSON, decl.Default(sql.Raw("'{}'"))),
		// A status column we map to a typed Go enum via OverrideFunc.
		decl.Column("status", decl.VarChar(32), decl.Default(sql.Raw("'active'"))),
		decl.Column("created_at", decl.Timestamp, decl.Default(sql.Raw("now()"))),
	)

	public.Table("orders",
		decl.Column("id", decl.UUID, decl.PrimaryKey()),
		decl.Column("user_id", decl.UUID, decl.References("users", "id")),
		decl.Column("total", decl.Decimal(18, 2)),
		decl.Column("status", decl.VarChar(32), decl.Default(sql.Raw("'pending'"))),
		decl.Index("user_id"),
	)

	return cat
}

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
// appdb.UUID / appdb.Decimal / *Status domain types below are wired in.
func typeMapper() codegen.TypeMapper {
	return codegen.NewDefaultTypeMapper(codegen.TypeMapperConfig{
		// Map every numeric/decimal column to the appdb.Decimal domain type
		// rather than string.
		Overrides: map[string]codegen.GoType{
			"numeric": {Name: "Decimal"},
			"decimal": {Name: "Decimal"},
		},
		// Pin specific columns regardless of their SQL type. Keys may be fully
		// qualified or bare; the most specific match wins.
		ColumnOverrides: map[string]codegen.GoType{
			"users.id":       {Name: "UUID"},
			"orders.id":      {Name: "UUID"},
			"orders.user_id": {Name: "UUID"},
			"metadata":       {ImportPath: "encoding/json", Name: "json.RawMessage"},
		},
		// Decide programmatically: map any column named "status" to a typed enum
		// named after its table (UserStatus, OrderStatus), keeping the SQL type a
		// plain varchar. Returning ok=false falls through to the maps above.
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
	md, err := catalog().Build()
	if err != nil {
		log.Fatal(err)
	}

	// 2. Generate with our customizations wired into Options.
	files, err := codegen.Generate(md, codegen.Options{
		PackageName:     "appdb",
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

	// 3. Write the result next to this source file, into the appdb package whose
	//    hand-written types.go declares the domain types the bindings reference.
	outDir := filepath.Join(sourceDir(), "appdb")
	if err := codegen.Write(outDir, files); err != nil {
		log.Fatal(err)
	}
	for _, file := range files {
		fmt.Printf("wrote %s\n", filepath.Join("appdb", file.Name))
	}
}

// sourceDir returns the directory of this source file, so generation targets the
// example's appdb package regardless of the working directory it runs from.
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
