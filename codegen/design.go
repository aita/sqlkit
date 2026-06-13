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

	// FileName is the output file name (default "design.go").
	FileName string

	// Tables, when non-empty, restricts generation to these table names.
	Tables []string

	// Dialect ("postgres" or "mysql", default postgres) selects how engine
	// type names map back to sqldef package types.
	Dialect string
}

const sqldefImport = "github.com/aita/sqlkit/sqldef"

// GenerateDesign renders the catalog as a sqldef design source file — the
// inverse of building a catalog: introspect a live database once, then
// maintain the schema as Go code and generate bindings and DDL from it.
// Unlike binding generation the result is a starting point meant to be
// edited, so it carries no DO NOT EDIT marker.
func GenerateDesign(catalog meta.Meta, opts DesignOptions) (File, error) {
	if opts.PackageName == "" {
		return File{}, fmt.Errorf("codegen: package name is required")
	}
	dialect := opts.Dialect
	if dialect == "" {
		dialect = "postgres"
	}
	fileName := opts.FileName
	if fileName == "" {
		fileName = "design.go"
	}

	tables := filterTables(catalog.Tables, opts.Tables)
	if len(tables) == 0 {
		return File{}, fmt.Errorf("codegen: no tables to generate")
	}

	// Distinct schema names in declaration order; each named schema gets a
	// Catalog.Schema handle variable, the unnamed default hangs off the
	// catalog directly.
	reserved := map[string]bool{"Catalog": true}
	var schemaOrder []string
	schemaSeen := map[string]bool{}
	for _, table := range tables {
		name := table.Schema.Name
		if !schemaSeen[name] {
			schemaSeen[name] = true
			schemaOrder = append(schemaOrder, name)
		}
	}
	schemaVars := map[string]string{}
	for _, name := range schemaOrder {
		if name == "" {
			schemaVars[name] = "Catalog"
			continue
		}
		v := goName(name)
		for reserved[v] {
			v += "Schema"
		}
		reserved[v] = true
		schemaVars[name] = v
	}

	// Assign a Go variable to each table so foreign keys can reference the
	// table values; names colliding with reserved identifiers get a suffix.
	tableVars := map[string]string{}
	for _, table := range tables {
		name := goName(table.Name)
		for reserved[name] {
			name += "Table"
		}
		reserved[name] = true
		tableVars[table.Name] = name
	}

	var sb strings.Builder
	sb.WriteString("// Catalog holds the design bootstrapped from a live database; edit it\n")
	sb.WriteString("// and regenerate the bindings and DDL from it.\n")
	sb.WriteString("var Catalog = sqldef.NewCatalog()\n")
	for _, name := range schemaOrder {
		if name == "" {
			continue
		}
		fmt.Fprintf(&sb, "\n// %s is schema %q of the catalog.\nvar %s = Catalog.Schema(%q)\n", schemaVars[name], name, schemaVars[name], name)
	}
	var trailers []string
	for _, table := range tables {
		sb.WriteString("\n")
		renderDesignTable(&sb, &trailers, table, schemaVars[table.Schema.Name], tableVars, dialect)
	}
	if len(trailers) > 0 {
		sb.WriteString("\n")
		for _, line := range trailers {
			sb.WriteString(line + "\n")
		}
	}

	fb := newFileBuf()
	fb.add(sqldefImport)
	fb.write(sb.String())
	return fb.fileWithHeader(opts.PackageName, fileName, "// Bootstrapped by sqlkitgen; maintained by hand.\n\n")
}

func renderDesignTable(sb *strings.Builder, trailers *[]string, table meta.Table, owner string, tableVars map[string]string, dialect string) {
	varName := tableVars[table.Name]

	// Single-column foreign keys to another generated table become a
	// References option on their column; composite ones become ForeignKey
	// elements. Self-referencing keys cannot mention the table's own variable
	// inside its declaration and trail behind as method calls; keys into
	// tables outside the generated set are flagged. References may cross
	// schemas within the catalog, so only the ref table's presence matters.
	refs := map[string]meta.ForeignKey{}
	var elements, foreign []meta.ForeignKey
	for _, fk := range table.ForeignKeys {
		refVar, ok := tableVars[fk.RefTable]
		switch {
		case !ok:
			foreign = append(foreign, fk)
		case fk.RefTable == table.Name:
			*trailers = append(*trailers, fmt.Sprintf("var _ = Catalog.AddForeignKey(%s, %s, %s, %s)",
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
		fmt.Fprintf(sb, "\tsqldef.TableComment(%q),\n", table.Comment)
	}
	for _, column := range table.Columns {
		fmt.Fprintf(sb, "\tsqldef.Column(%q, %s", column.Name, dslTypeExpr(column, dialect))
		if column.PrimaryKey {
			sb.WriteString(", sqldef.PrimaryKey()")
		}
		if column.HasDefault {
			if column.DefaultExpr != "" {
				fmt.Fprintf(sb, ", sqldef.Default(%q)", column.DefaultExpr)
			} else {
				sb.WriteString(", sqldef.Default()")
			}
		}
		if column.Nullable {
			sb.WriteString(", sqldef.Nullable()")
		}
		if uniques[column.Name] {
			sb.WriteString(", sqldef.Unique()")
		}
		if fk, ok := refs[column.Name]; ok {
			fmt.Fprintf(sb, ", sqldef.References(%s, %q)", tableVars[fk.RefTable], fk.RefColumns[0])
		}
		if column.Comment != "" {
			fmt.Fprintf(sb, ", sqldef.Comment(%q)", column.Comment)
		}
		sb.WriteString("),\n")
	}
	for _, fk := range elements {
		fmt.Fprintf(sb, "\tsqldef.ForeignKey(%s, %s, %s),\n", goStringSlice(fk.Columns), tableVars[fk.RefTable], goStringSlice(fk.RefColumns))
	}
	for _, ix := range indexes {
		call := "sqldef.Index"
		if ix.Unique {
			call = "sqldef.UniqueIndex"
		}
		fmt.Fprintf(sb, "\t%s(%s),\n", call, quotedList(ix.Columns))
	}
	for _, check := range table.Checks {
		fmt.Fprintf(sb, "\tsqldef.Check(%q),\n", check.Expression)
	}
	for _, fk := range foreign {
		ref := fk.RefTable
		if fk.RefSchema.Name != "" {
			ref = fk.RefSchema.Name + "." + fk.RefTable
		}
		fmt.Fprintf(sb, "\t// NOTE: foreign key %s references %s outside this design and must be declared by hand.\n",
			fk.Name, ref)
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

// dslTypeExpr maps an engine type name back to the sqldef package expression
// that produces it. Types without a portable name fall back to sqldef.Type,
// which keeps the engine name verbatim.
func dslTypeExpr(column meta.Column, dialect string) string {
	engine, _ := column.Type.Engine(dialect)
	dataType := strings.ToLower(strings.TrimSpace(engine))
	if dialect == "mysql" {
		switch dataType {
		case "bool", "boolean":
			return "sqldef.Bool"
		case "smallint":
			return "sqldef.SmallInt"
		case "int", "integer":
			return "sqldef.Int"
		case "bigint":
			return "sqldef.BigInt"
		case "float":
			return "sqldef.Float"
		case "double":
			return "sqldef.Double"
		case "decimal":
			return fmt.Sprintf("sqldef.Decimal(%d, %d)", column.Type.Precision, column.Type.Scale)
		case "text":
			return "sqldef.Text"
		case "varchar":
			return fmt.Sprintf("sqldef.VarChar(%d)", column.Type.Length)
		case "char":
			return fmt.Sprintf("sqldef.Char(%d)", column.Type.Length)
		case "blob":
			return "sqldef.Bytes"
		case "json":
			return "sqldef.JSON"
		case "date":
			return "sqldef.Date"
		case "time":
			return "sqldef.Time"
		case "datetime":
			return "sqldef.Timestamp"
		}
		return fmt.Sprintf("sqldef.Type(%q)", dataType)
	}
	switch dataType {
	case "bool":
		return "sqldef.Bool"
	case "int2":
		return "sqldef.SmallInt"
	case "int4":
		return "sqldef.Int"
	case "int8":
		return "sqldef.BigInt"
	case "float4":
		return "sqldef.Float"
	case "float8":
		return "sqldef.Double"
	case "numeric":
		return fmt.Sprintf("sqldef.Decimal(%d, %d)", column.Type.Precision, column.Type.Scale)
	case "text":
		return "sqldef.Text"
	case "varchar":
		return fmt.Sprintf("sqldef.VarChar(%d)", column.Type.Length)
	case "bpchar":
		return fmt.Sprintf("sqldef.Char(%d)", column.Type.Length)
	case "uuid":
		return "sqldef.UUID"
	case "bytea":
		return "sqldef.Bytes"
	case "jsonb":
		return "sqldef.JSON"
	case "date":
		return "sqldef.Date"
	case "time":
		return "sqldef.Time"
	case "timestamptz":
		return "sqldef.Timestamp"
	}
	return fmt.Sprintf("sqldef.Type(%q)", dataType)
}
