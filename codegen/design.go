package codegen

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/schema"
)

// DesignOptions configures GenerateDesign.
type DesignOptions struct {
	// PackageName is the package of the generated design file. Required.
	PackageName string

	// VarName is the exported variable holding the design. Defaults to the
	// schema name as a Go identifier, or "Design" when the schema is unnamed.
	VarName string

	// FileName is the output file name (default "design.go").
	FileName string

	// Tables, when non-empty, restricts generation to these table names.
	Tables []string

	// Dialect overrides Schema.Dialect ("postgres" or "mysql"); it selects how
	// engine type names map back to DSL types.
	Dialect string
}

const dslImport = "github.com/aita/sqlkit/dsl"

// GenerateDesign renders the schema as a dsl design source file — the inverse
// of the dsl package: introspect a live database once, then maintain the
// schema as Go code and generate bindings from it. The dsl package is
// dot-imported, goa-style, so the design reads as bare Schema/Table/Column
// calls. Unlike binding generation the result is a starting point meant to be
// edited, so it carries no DO NOT EDIT marker.
func GenerateDesign(model schema.Schema, opts DesignOptions) (File, error) {
	if opts.PackageName == "" {
		return File{}, fmt.Errorf("codegen: package name is required")
	}
	dialect := opts.Dialect
	if dialect == "" {
		dialect = model.Dialect
	}
	varName := opts.VarName
	if varName == "" {
		if model.Name != "" {
			varName = goName(model.Name)
		} else {
			varName = "Design"
		}
	}
	fileName := opts.FileName
	if fileName == "" {
		fileName = "design.go"
	}

	tables := filterTables(model.Tables, opts.Tables)
	if len(tables) == 0 {
		return File{}, fmt.Errorf("codegen: no tables to generate")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "// %s is the design of schema %q, bootstrapped from a live database;\n", varName, model.Name)
	sb.WriteString("// edit it and regenerate the bindings from it.\n")
	fmt.Fprintf(&sb, "var %s = Schema(%q, func() {\n", varName, model.Name)
	for i, table := range tables {
		if i > 0 {
			sb.WriteString("\n")
		}
		renderDesignTable(&sb, table, dialect)
	}
	sb.WriteString("})\n")

	fb := newFileBuf()
	fb.add(". " + dslImport)
	fb.write(sb.String())
	return fb.fileWithHeader(opts.PackageName, fileName, "// Bootstrapped by sqlkitgen; maintained by hand.\n\n")
}

func renderDesignTable(sb *strings.Builder, table schema.Table, dialect string) {
	// Single-column foreign keys become a References option on their column;
	// the rest are table-level ForeignKey calls. Foreign keys into another
	// schema are not expressible in the DSL and are flagged instead.
	refs := map[string]schema.ForeignKey{}
	var rest, foreign []schema.ForeignKey
	for _, fk := range table.ForeignKeys {
		switch {
		case fk.RefSchema != "" && fk.RefSchema != table.Schema:
			foreign = append(foreign, fk)
		case len(fk.Columns) == 1 && len(fk.RefColumns) == 1:
			if _, dup := refs[fk.Columns[0]]; dup {
				rest = append(rest, fk)
			} else {
				refs[fk.Columns[0]] = fk
			}
		default:
			rest = append(rest, fk)
		}
	}

	// Single-column unique indexes likewise become a Unique option on their
	// column; the rest stay table-level Index/UniqueIndex declarations.
	uniques := map[string]bool{}
	var indexes []schema.Index
	for _, ix := range table.Indexes {
		if ix.Unique && len(ix.Columns) == 1 && !uniques[ix.Columns[0]] {
			uniques[ix.Columns[0]] = true
		} else {
			indexes = append(indexes, ix)
		}
	}

	fmt.Fprintf(sb, "\tTable(%q, func() {\n", table.Name)
	if table.Comment != "" {
		fmt.Fprintf(sb, "\t\tTableComment(%q)\n", table.Comment)
	}
	for _, column := range table.Columns {
		fmt.Fprintf(sb, "\t\tColumn(%q, %s", column.Name, dslTypeExpr(column, dialect))
		if column.PrimaryKey {
			sb.WriteString(", PrimaryKey()")
		}
		if column.HasDefault {
			sb.WriteString(", Default()")
		}
		if column.Nullable {
			sb.WriteString(", Nullable()")
		}
		if uniques[column.Name] {
			sb.WriteString(", Unique()")
		}
		if fk, ok := refs[column.Name]; ok {
			fmt.Fprintf(sb, ", References(%q, %q)", fk.RefTable, fk.RefColumns[0])
		}
		if column.Comment != "" {
			fmt.Fprintf(sb, ", Comment(%q)", column.Comment)
		}
		sb.WriteString(")\n")
	}
	for _, fk := range rest {
		fmt.Fprintf(sb, "\t\tForeignKey(%s, %q, %s)\n", goStringSlice(fk.Columns), fk.RefTable, goStringSlice(fk.RefColumns))
	}
	for _, ix := range indexes {
		call := "Index"
		if ix.Unique {
			call = "UniqueIndex"
		}
		fmt.Fprintf(sb, "\t\t%s(%s)\n", call, quotedList(ix.Columns))
	}
	for _, check := range table.Checks {
		fmt.Fprintf(sb, "\t\tCheck(%q)\n", check.Expression)
	}
	for _, fk := range foreign {
		fmt.Fprintf(sb, "\t\t// NOTE: foreign key %s references %s.%s in another schema and cannot be declared in the DSL.\n",
			fk.Name, fk.RefSchema, fk.RefTable)
	}
	sb.WriteString("\t})\n")
}

func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return strings.Join(quoted, ", ")
}

func goStringSlice(items []string) string {
	return "[]string{" + quotedList(items) + "}"
}

// dslTypeExpr maps an engine type name back to the DSL expression that
// produces it. Types without a portable DSL name fall back to Type, which
// keeps the engine name verbatim.
func dslTypeExpr(column schema.Column, dialect string) string {
	dataType := strings.ToLower(strings.TrimSpace(column.DataType))
	if dialect == "mysql" {
		switch dataType {
		case "bool", "boolean":
			return "Bool"
		case "smallint":
			return "SmallInt"
		case "int", "integer":
			return "Int"
		case "bigint":
			return "BigInt"
		case "float":
			return "Float"
		case "double":
			return "Double"
		case "decimal":
			return fmt.Sprintf("Decimal(%d, %d)", column.Precision, column.Scale)
		case "text":
			return "Text"
		case "varchar":
			return fmt.Sprintf("VarChar(%d)", column.Length)
		case "char":
			return fmt.Sprintf("Char(%d)", column.Length)
		case "blob":
			return "Bytes"
		case "json":
			return "JSON"
		case "date":
			return "Date"
		case "time":
			return "Time"
		case "datetime":
			return "Timestamp"
		}
		return fmt.Sprintf("Type(%q)", dataType)
	}
	switch dataType {
	case "bool":
		return "Bool"
	case "int2":
		return "SmallInt"
	case "int4":
		return "Int"
	case "int8":
		return "BigInt"
	case "float4":
		return "Float"
	case "float8":
		return "Double"
	case "numeric":
		return fmt.Sprintf("Decimal(%d, %d)", column.Precision, column.Scale)
	case "text":
		return "Text"
	case "varchar":
		return fmt.Sprintf("VarChar(%d)", column.Length)
	case "bpchar":
		return fmt.Sprintf("Char(%d)", column.Length)
	case "uuid":
		return "UUID"
	case "bytea":
		return "Bytes"
	case "jsonb":
		return "JSON"
	case "date":
		return "Date"
	case "time":
		return "Time"
	case "timestamptz":
		return "Timestamp"
	}
	return fmt.Sprintf("Type(%q)", dataType)
}
