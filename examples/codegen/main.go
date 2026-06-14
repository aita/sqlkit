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
// with a customized type mapper and DTO namer, and prints the result to stdout
// so you can see the effect of each customization. Run it with:
//
//	go run ./examples/codegen
//
// To write the files to disk instead of printing them, swap the print loop at
// the end for codegen.Write (see the comment there).
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqldef"
)

// catalog declares the schema we generate from. In a real project this is its
// own package (see examples/blog/schema) and the source of truth for both the
// bindings and the DDL.
func catalog() *sqldef.Catalog {
	cat := sqldef.NewCatalog()
	public := cat.Schema("public")

	users := public.Table("users",
		sqldef.TableComment("Registered accounts"),
		// A uuid primary key: by default codegen maps uuid -> string. We override
		// it to a real uuid.UUID below via ColumnOverrides.
		sqldef.Column("id", sqldef.UUID, sqldef.PrimaryKey()),
		sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique()),
		// A money amount: numeric defaults to string. We map every numeric column
		// to shopspring/decimal below via Overrides.
		sqldef.Column("balance", sqldef.Decimal(18, 2), sqldef.Default(sql.Raw("0"))),
		// A free-form JSON blob: jsonb defaults to []byte. We map it to
		// json.RawMessage below, again via ColumnOverrides.
		sqldef.Column("metadata", sqldef.JSON, sqldef.Default(sql.Raw("'{}'"))),
		// A status column we map to a typed Go enum via OverrideFunc.
		sqldef.Column("status", sqldef.VarChar(32), sqldef.Default(sql.Raw("'active'"))),
		sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(sql.Raw("now()"))),
	)

	public.Table("orders",
		sqldef.Column("id", sqldef.UUID, sqldef.PrimaryKey()),
		sqldef.Column("user_id", sqldef.UUID, sqldef.References(users, "id")),
		sqldef.Column("total", sqldef.Decimal(18, 2)),
		sqldef.Column("status", sqldef.VarChar(32), sqldef.Default(sql.Raw("'pending'"))),
		sqldef.Index("user_id"),
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
// bare type.
func typeMapper() codegen.TypeMapper {
	return codegen.NewDefaultTypeMapper(codegen.TypeMapperConfig{
		// Map every numeric/decimal column to a decimal type rather than string.
		Overrides: map[string]codegen.GoType{
			"numeric": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
			"decimal": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
		},
		// Pin specific columns regardless of their SQL type. Keys may be fully
		// qualified or bare; the most specific match wins.
		ColumnOverrides: map[string]codegen.GoType{
			"users.id":       {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"},
			"orders.id":      {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"},
			"orders.user_id": {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"},
			"metadata":       {ImportPath: "encoding/json", Name: "json.RawMessage"},
		},
		// Decide programmatically: map any column named "status" to a typed enum
		// named after its table (UserStatus, OrderStatus), keeping the SQL type a
		// plain varchar. Returning ok=false falls through to the maps above.
		OverrideFunc: func(table meta.Table, column meta.Column, _ string) (codegen.GoType, bool, error) {
			if column.Name == "status" {
				name := codegenGoName(table.Name) + "Status"
				return codegen.GoType{Name: name}, true, nil
			}
			return codegen.GoType{}, false, nil
		},
	})
}

// dtoName derives the DTO type name from a table. The default singularizes the
// table name ("users" -> "User"); here we add a "Record" suffix to match a
// house style ("users" -> "UserRecord").
func dtoName(table meta.Table) string {
	return codegenGoName(singular(table.Name)) + "Record"
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

	// 3. Use the result. Here we print each file to stdout to show the effect of
	//    the customizations; a real generator writes them with:
	//
	//	if err := codegen.Write("appdb", files); err != nil {
	//		log.Fatal(err)
	//	}
	for _, file := range files {
		fmt.Printf("// ===== %s =====\n%s\n", file.Name, file.Content)
	}
}

// codegenGoName converts a snake_case identifier to an exported Go name, the
// same way codegen names its generated identifiers. codegen keeps its own
// version of this unexported, so we reimplement the slice of it we need for the
// DTO namer and the enum-type names above.
func codegenGoName(name string) string {
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
