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
// schema as Go code and generate bindings from it. Unlike binding generation
// the result is a starting point meant to be edited, so it carries no
// DO NOT EDIT marker.
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
	fmt.Fprintf(&sb, "var %s = dsl.Schema(%q, func() {\n", varName, model.Name)
	for i, table := range tables {
		if i > 0 {
			sb.WriteString("\n")
		}
		renderDesignTable(&sb, table, dialect)
	}
	sb.WriteString("})\n")

	fb := newFileBuf()
	fb.add(dslImport)
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

	fmt.Fprintf(sb, "\tdsl.Table(%q, func() {\n", table.Name)
	for _, column := range table.Columns {
		fmt.Fprintf(sb, "\t\tdsl.Column(%q, %s", column.Name, dslTypeExpr(column, dialect))
		if column.PrimaryKey {
			sb.WriteString(", dsl.PrimaryKey()")
		}
		if column.HasDefault {
			sb.WriteString(", dsl.Default()")
		}
		if column.Nullable {
			sb.WriteString(", dsl.Nullable()")
		}
		if fk, ok := refs[column.Name]; ok {
			fmt.Fprintf(sb, ", dsl.References(%q, %q)", fk.RefTable, fk.RefColumns[0])
		}
		sb.WriteString(")\n")
	}
	for _, fk := range rest {
		fmt.Fprintf(sb, "\t\tdsl.ForeignKey(%s, %q, %s)\n", goStringSlice(fk.Columns), fk.RefTable, goStringSlice(fk.RefColumns))
	}
	for _, fk := range foreign {
		fmt.Fprintf(sb, "\t\t// NOTE: foreign key %s references %s.%s in another schema and cannot be declared in the DSL.\n",
			fk.Name, fk.RefSchema, fk.RefTable)
	}
	sb.WriteString("\t})\n")
}

func goStringSlice(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// dslTypeExpr maps an engine type name back to the DSL expression that
// produces it. Types without a portable DSL name fall back to dsl.Type, which
// keeps the engine name verbatim.
func dslTypeExpr(column schema.Column, dialect string) string {
	dataType := strings.ToLower(strings.TrimSpace(column.DataType))
	if dialect == "mysql" {
		switch dataType {
		case "bool", "boolean":
			return "dsl.Bool"
		case "smallint":
			return "dsl.SmallInt"
		case "int", "integer":
			return "dsl.Int"
		case "bigint":
			return "dsl.BigInt"
		case "float":
			return "dsl.Float"
		case "double":
			return "dsl.Double"
		case "decimal":
			return fmt.Sprintf("dsl.Decimal(%d, %d)", column.Precision, column.Scale)
		case "text":
			return "dsl.Text"
		case "varchar":
			return fmt.Sprintf("dsl.VarChar(%d)", column.Length)
		case "char":
			return fmt.Sprintf("dsl.Char(%d)", column.Length)
		case "blob":
			return "dsl.Bytes"
		case "json":
			return "dsl.JSON"
		case "date":
			return "dsl.Date"
		case "time":
			return "dsl.Time"
		case "datetime":
			return "dsl.Timestamp"
		}
		return fmt.Sprintf("dsl.Type(%q)", dataType)
	}
	switch dataType {
	case "bool":
		return "dsl.Bool"
	case "int2":
		return "dsl.SmallInt"
	case "int4":
		return "dsl.Int"
	case "int8":
		return "dsl.BigInt"
	case "float4":
		return "dsl.Float"
	case "float8":
		return "dsl.Double"
	case "numeric":
		return fmt.Sprintf("dsl.Decimal(%d, %d)", column.Precision, column.Scale)
	case "text":
		return "dsl.Text"
	case "varchar":
		return fmt.Sprintf("dsl.VarChar(%d)", column.Length)
	case "bpchar":
		return fmt.Sprintf("dsl.Char(%d)", column.Length)
	case "uuid":
		return "dsl.UUID"
	case "bytea":
		return "dsl.Bytes"
	case "jsonb":
		return "dsl.JSON"
	case "date":
		return "dsl.Date"
	case "time":
		return "dsl.Time"
	case "timestamptz":
		return "dsl.Timestamp"
	}
	return fmt.Sprintf("dsl.Type(%q)", dataType)
}
