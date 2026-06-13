package codegen

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// DesignOptions configures GenerateDesign.
type DesignOptions struct {
	// PackageName is the package of the generated design file. Required.
	PackageName string

	// VarName is the exported variable holding the schema handle (the result
	// of Catalog.Schema). Defaults to the schema name as a Go identifier; it
	// is unused when the schema is unnamed, in which case tables hang off the
	// catalog directly.
	VarName string

	// FileName is the output file name (default "design.go").
	FileName string

	// Tables, when non-empty, restricts generation to these table names.
	Tables []string

	// Dialect overrides Schema.Dialect ("postgres" or "mysql"); it selects how
	// engine type names map back to schema package types.
	Dialect string
}

const schemaImport = "github.com/aita/sqlkit/schema"

// GenerateDesign renders the schema as a design source file built on the
// schema package — the inverse of that package: introspect a live database
// once, then maintain the schema as Go code and generate bindings and DDL
// from it. Unlike binding generation the result is a starting point meant to
// be edited, so it carries no DO NOT EDIT marker.
func GenerateDesign(model meta.Schema, opts DesignOptions) (File, error) {
	if opts.PackageName == "" {
		return File{}, fmt.Errorf("codegen: package name is required")
	}
	dialect := opts.Dialect
	if dialect == "" {
		dialect = model.Dialect
	}
	fileName := opts.FileName
	if fileName == "" {
		fileName = "design.go"
	}

	tables := filterTables(model.Tables, opts.Tables)
	if len(tables) == 0 {
		return File{}, fmt.Errorf("codegen: no tables to generate")
	}

	schemaVar := ""
	if model.Name != "" {
		schemaVar = opts.VarName
		if schemaVar == "" {
			schemaVar = goName(model.Name)
		}
	}

	// Assign a Go variable to each table so foreign keys can reference the
	// table values; names colliding with the fixed variables get a suffix.
	reserved := map[string]bool{"Catalog": true}
	if schemaVar != "" {
		reserved[schemaVar] = true
	}
	tableVars := map[string]string{}
	for _, table := range tables {
		name := goName(table.Name)
		for reserved[name] {
			name += "Table"
		}
		reserved[name] = true
		tableVars[table.Name] = name
	}

	owner := "Catalog"
	if schemaVar != "" {
		owner = schemaVar
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "// Catalog holds the design of schema %q, bootstrapped from a live\n", model.Name)
	sb.WriteString("// database; edit it and regenerate the bindings from it.\n")
	sb.WriteString("var Catalog = schema.NewCatalog()\n")
	if schemaVar != "" {
		fmt.Fprintf(&sb, "\n// %s is schema %q of the catalog.\nvar %s = Catalog.Schema(%q)\n", schemaVar, model.Name, schemaVar, model.Name)
	}
	var trailers []string
	for _, table := range tables {
		sb.WriteString("\n")
		renderDesignTable(&sb, &trailers, table, owner, tableVars, dialect)
	}
	if len(trailers) > 0 {
		sb.WriteString("\n")
		for _, line := range trailers {
			sb.WriteString(line + "\n")
		}
	}

	fb := newFileBuf()
	fb.add(schemaImport)
	fb.write(sb.String())
	return fb.fileWithHeader(opts.PackageName, fileName, "// Bootstrapped by sqlkitgen; maintained by hand.\n\n")
}

func renderDesignTable(sb *strings.Builder, trailers *[]string, table meta.Table, owner string, tableVars map[string]string, dialect string) {
	varName := tableVars[table.Name]

	// Single-column foreign keys to another generated table become a
	// References option on their column; composite ones become ForeignKey
	// elements. Self-referencing keys cannot mention the table's own variable
	// inside its declaration and trail behind as method calls; keys into
	// tables outside the generated set are flagged.
	refs := map[string]meta.ForeignKey{}
	var elements, foreign []meta.ForeignKey
	for _, fk := range table.ForeignKeys {
		refVar, ok := tableVars[fk.RefTable]
		switch {
		case !ok || (fk.RefSchema != "" && fk.RefSchema != table.Schema):
			foreign = append(foreign, fk)
		case fk.RefTable == table.Name:
			*trailers = append(*trailers, fmt.Sprintf("var _ = %s.ForeignKey(%s, %s, %s)",
				varName, goStringSlice(fk.Columns), refVar, goStringSlice(fk.RefColumns)))
		case len(fk.Columns) == 1 && len(fk.RefColumns) == 1:
			if _, dup := refs[fk.Columns[0]]; dup {
				elements = append(elements, fk)
			} else {
				refs[fk.Columns[0]] = fk
			}
		default:
			elements = append(elements, fk)
		}
	}

	// Single-column unique indexes likewise become a Unique option on their
	// column; the rest stay table-level Index/UniqueIndex declarations.
	uniques := map[string]bool{}
	var indexes []meta.Index
	for _, ix := range table.Indexes {
		if ix.Unique && len(ix.Columns) == 1 && !uniques[ix.Columns[0]] {
			uniques[ix.Columns[0]] = true
		} else {
			indexes = append(indexes, ix)
		}
	}

	fmt.Fprintf(sb, "var %s = %s.Table(%q,\n", varName, owner, table.Name)
	if table.Comment != "" {
		fmt.Fprintf(sb, "\tschema.TableComment(%q),\n", table.Comment)
	}
	for _, column := range table.Columns {
		fmt.Fprintf(sb, "\tschema.Column(%q, %s", column.Name, dslTypeExpr(column, dialect))
		if column.PrimaryKey {
			sb.WriteString(", schema.PrimaryKey()")
		}
		if column.HasDefault {
			if column.DefaultExpr != "" {
				fmt.Fprintf(sb, ", schema.Default(%q)", column.DefaultExpr)
			} else {
				sb.WriteString(", schema.Default()")
			}
		}
		if column.Nullable {
			sb.WriteString(", schema.Nullable()")
		}
		if uniques[column.Name] {
			sb.WriteString(", schema.Unique()")
		}
		if fk, ok := refs[column.Name]; ok {
			fmt.Fprintf(sb, ", schema.References(%s, %q)", tableVars[fk.RefTable], fk.RefColumns[0])
		}
		if column.Comment != "" {
			fmt.Fprintf(sb, ", schema.Comment(%q)", column.Comment)
		}
		sb.WriteString("),\n")
	}
	for _, fk := range elements {
		fmt.Fprintf(sb, "\tschema.ForeignKey(%s, %s, %s),\n", goStringSlice(fk.Columns), tableVars[fk.RefTable], goStringSlice(fk.RefColumns))
	}
	for _, ix := range indexes {
		call := "schema.Index"
		if ix.Unique {
			call = "schema.UniqueIndex"
		}
		fmt.Fprintf(sb, "\t%s(%s),\n", call, quotedList(ix.Columns))
	}
	for _, check := range table.Checks {
		fmt.Fprintf(sb, "\tschema.Check(%q),\n", check.Expression)
	}
	for _, fk := range foreign {
		fmt.Fprintf(sb, "\t// NOTE: foreign key %s references %s.%s outside this design and must be declared by hand.\n",
			fk.Name, fk.RefSchema, fk.RefTable)
	}
	sb.WriteString(")\n")
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

// dslTypeExpr maps an engine type name back to the schema package expression
// that produces it. Types without a portable name fall back to schema.Type,
// which keeps the engine name verbatim.
func dslTypeExpr(column meta.Column, dialect string) string {
	dataType := strings.ToLower(strings.TrimSpace(column.DataType))
	if dialect == "mysql" {
		switch dataType {
		case "bool", "boolean":
			return "schema.Bool"
		case "smallint":
			return "schema.SmallInt"
		case "int", "integer":
			return "schema.Int"
		case "bigint":
			return "schema.BigInt"
		case "float":
			return "schema.Float"
		case "double":
			return "schema.Double"
		case "decimal":
			return fmt.Sprintf("schema.Decimal(%d, %d)", column.Precision, column.Scale)
		case "text":
			return "schema.Text"
		case "varchar":
			return fmt.Sprintf("schema.VarChar(%d)", column.Length)
		case "char":
			return fmt.Sprintf("schema.Char(%d)", column.Length)
		case "blob":
			return "schema.Bytes"
		case "json":
			return "schema.JSON"
		case "date":
			return "schema.Date"
		case "time":
			return "schema.Time"
		case "datetime":
			return "schema.Timestamp"
		}
		return fmt.Sprintf("schema.Type(%q)", dataType)
	}
	switch dataType {
	case "bool":
		return "schema.Bool"
	case "int2":
		return "schema.SmallInt"
	case "int4":
		return "schema.Int"
	case "int8":
		return "schema.BigInt"
	case "float4":
		return "schema.Float"
	case "float8":
		return "schema.Double"
	case "numeric":
		return fmt.Sprintf("schema.Decimal(%d, %d)", column.Precision, column.Scale)
	case "text":
		return "schema.Text"
	case "varchar":
		return fmt.Sprintf("schema.VarChar(%d)", column.Length)
	case "bpchar":
		return fmt.Sprintf("schema.Char(%d)", column.Length)
	case "uuid":
		return "schema.UUID"
	case "bytea":
		return "schema.Bytes"
	case "jsonb":
		return "schema.JSON"
	case "date":
		return "schema.Date"
	case "time":
		return "schema.Time"
	case "timestamptz":
		return "schema.Timestamp"
	}
	return fmt.Sprintf("schema.Type(%q)", dataType)
}
