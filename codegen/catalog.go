package codegen

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// CatalogOptions configures GenerateCatalog.
type CatalogOptions struct {
	// PackageName is the package of the generated catalog file. Required.
	PackageName string

	// FileName is the output file name (default "schema.go").
	FileName string

	// Tables, when non-empty, restricts generation to these table names.
	Tables []string

	// Dialect ("postgres" or "mysql", default postgres) selects how engine
	// type names map back to decl package types.
	Dialect string
}

const (
	declImport = "github.com/aita/sqlkit/decl"
	sqlImport  = "github.com/aita/sqlkit/sql"
)

// GenerateCatalog renders the schema as decl catalog source — the inverse of
// building a catalog: introspect a live database once, then maintain the
// schema as Go code and generate bindings and DDL from it. Unlike binding
// generation the result is a starting point meant to be edited, so it carries
// no DO NOT EDIT marker.
func GenerateCatalog(md meta.Metadata, opts CatalogOptions) (File, error) {
	if opts.PackageName == "" {
		return File{}, fmt.Errorf("codegen: package name is required")
	}
	dialect := opts.Dialect
	if dialect == "" {
		dialect = "postgres"
	}
	sqlDialect, err := sql.DialectByName(dialect)
	if err != nil {
		return File{}, err
	}
	fileName := opts.FileName
	if fileName == "" {
		fileName = "schema.go"
	}

	tables := filterTables(md.Tables, opts.Tables)
	if len(tables) == 0 {
		return File{}, fmt.Errorf("codegen: no tables to generate")
	}

	// Enums are rendered unfiltered by opts.Tables (like Generate's binding
	// generator), so a Catalog.EnumType column always resolves against a
	// declared enum even when the table filter narrows which tables render.
	enumNames := map[string]bool{}
	for _, e := range md.Enums {
		enumNames[e.Name] = true
		enumNames[e.QualifiedName()] = true
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
	for _, e := range md.Enums {
		if !schemaSeen[e.Schema] {
			schemaSeen[e.Schema] = true
			schemaOrder = append(schemaOrder, e.Schema)
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
	// Keyed by schema+name so same-named tables in different schemas don't
	// collide.
	tableVars := map[string]string{}
	for _, table := range tables {
		name := goName(table.Name)
		for reserved[name] {
			name += "Table"
		}
		reserved[name] = true
		tableVars[tableVarKey(table.Schema.Name, table.Name)] = name
	}

	var sb strings.Builder
	sb.WriteString("// Catalog holds the schema bootstrapped from a live database; edit it\n")
	sb.WriteString("// and regenerate the bindings and DDL from it.\n")
	sb.WriteString("var Catalog = decl.NewCatalog()\n")
	for _, name := range schemaOrder {
		if name == "" {
			continue
		}
		fmt.Fprintf(&sb, "\n// %s is schema %q of the catalog.\nvar %s = Catalog.Schema(%q)\n", schemaVars[name], name, schemaVars[name], name)
	}
	if len(md.Enums) > 0 {
		// Enum/Sequence/Extension declarations are statements (they return
		// nothing to bind to a var), so they can't sit at package scope like the
		// Table/Schema declarations above — a bare call there isn't valid Go.
		// init() is the idiomatic place to run declarative setup like this.
		sb.WriteString("\nfunc init() {\n")
		for _, e := range md.Enums {
			owner := schemaVars[e.Schema]
			if owner == "" {
				owner = "Catalog"
			}
			fmt.Fprintf(&sb, "\t%s.Enum(%q, %s)\n", owner, e.Name, quotedList(e.Values))
		}
		sb.WriteString("}\n")
	}
	var usedSQL bool
	for _, table := range tables {
		sb.WriteString("\n")
		if err := renderCatalogTable(&sb, table, schemaVars[table.Schema.Name], tableVars, dialect, sqlDialect, enumNames, &usedSQL); err != nil {
			return File{}, err
		}
	}

	fb := newFileBuf()
	fb.add(declImport)
	if usedSQL {
		fb.add(sqlImport)
	}
	fb.write(sb.String())
	return fb.fileWithHeader(opts.PackageName, fileName, "// Bootstrapped by sqlkit; maintained by hand.\n\n")
}

func renderCatalogTable(sb *strings.Builder, table meta.Table, owner string, tableVars map[string]string, dialect string, sqlDialect sql.Dialect, enumNames map[string]bool, usedSQL *bool) error {
	varName := tableVars[tableVarKey(table.Schema.Name, table.Name)]

	// Single-column foreign keys to another generated table become a
	// References option on their column; composite ones become ForeignKey
	// elements. Both name their target, so a self-reference is just an ordinary
	// References to this table. Keys into tables outside the generated set are
	// flagged. References may cross schemas within the catalog, so only the ref
	// table's presence matters.
	refs := map[string]meta.ForeignKey{}
	var elements, foreign []meta.ForeignKey
	for _, fk := range table.ForeignKeys {
		_, inSet := tableVars[tableVarKey(fk.RefSchema.Name, fk.RefTable)]
		switch {
		case !inSet:
			foreign = append(foreign, fk)
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
		fmt.Fprintf(sb, "\tdecl.TableComment(%q),\n", table.Comment)
	}
	for _, column := range table.Columns {
		if column.Type == nil {
			return fmt.Errorf("codegen: table %q column %q has no type", table.Name, column.Name)
		}
		fmt.Fprintf(sb, "\tdecl.Column(%q, %s", column.Name, declTypeExprForCatalog(column, dialect, enumNames))
		if column.PrimaryKey {
			sb.WriteString(", decl.PrimaryKey()")
		}
		if column.HasDefault {
			if column.DefaultExpr != nil {
				text, err := sql.CompileStatic(sqlDialect, column.DefaultExpr)
				if err != nil {
					return fmt.Errorf("codegen: table %q column %q default: %w", table.Name, column.Name, err)
				}
				fmt.Fprintf(sb, ", decl.Default(sql.Raw(%q))", text)
				*usedSQL = true
			} else {
				sb.WriteString(", decl.Default()")
			}
		}
		if column.Nullable {
			sb.WriteString(", decl.Nullable()")
		}
		if uniques[column.Name] {
			sb.WriteString(", decl.Unique()")
		}
		if fk, ok := refs[column.Name]; ok {
			fmt.Fprintf(sb, ", decl.References(%q, %q%s)", refName(table.Schema.Name, fk), fk.RefColumns[0], fkActionArgs(fk))
		}
		if column.Comment != "" {
			fmt.Fprintf(sb, ", decl.Comment(%q)", column.Comment)
		}
		sb.WriteString("),\n")
	}
	for _, fk := range elements {
		fmt.Fprintf(sb, "\tdecl.ForeignKey(%s, %q, %s%s),\n", goStringSlice(fk.Columns), refName(table.Schema.Name, fk), goStringSlice(fk.RefColumns), fkActionArgs(fk))
	}
	for _, ix := range indexes {
		call := "decl.Index"
		if ix.Unique {
			call = "decl.UniqueIndex"
		}
		fmt.Fprintf(sb, "\t%s(%s),\n", call, quotedList(ix.Columns))
	}
	for _, check := range table.Checks {
		text, err := sql.CompileStatic(sqlDialect, check.Expression)
		if err != nil {
			return fmt.Errorf("codegen: table %q check %q: %w", table.Name, check.Name, err)
		}
		fmt.Fprintf(sb, "\tdecl.Check(sql.Raw(%q)),\n", text)
		*usedSQL = true
	}
	for _, fk := range foreign {
		ref := fk.RefTable
		if fk.RefSchema.Name != "" {
			ref = fk.RefSchema.Name + "." + fk.RefTable
		}
		fmt.Fprintf(sb, "\t// NOTE: foreign key %s references %s outside this catalog and must be declared by hand.\n",
			fk.Name, ref)
	}
	sb.WriteString(")\n")
	return nil
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

// tableVarKey keys the table-variable map by schema and name so same-named
// tables in different schemas get distinct entries.
func tableVarKey(schema, name string) string { return schema + "\x00" + name }

// refName renders a foreign key's target as a name for decl.References /
// decl.ForeignKey, seen from the referencing table's schema: a bare table
// name when the target shares that schema (resolved there at Build), or a
// "schema.table" qualified name when it crosses schemas.
func refName(fromSchema string, fk meta.ForeignKey) string {
	if fk.RefSchema.Name != fromSchema {
		return fk.RefSchema.Name + "." + fk.RefTable
	}
	return fk.RefTable
}

// ReferentialActionExpr renders a foreign-key referential action as the decl
// package constant that produces it (e.g. "decl.Cascade"), or "" when the
// action is empty (the engine default). It is shared with the migration
// autogenerator, which emits the same decl vocabulary.
func ReferentialActionExpr(action meta.ReferentialAction) string {
	switch action {
	case "":
		return ""
	case meta.NoAction:
		return "decl.NoAction"
	case meta.Restrict:
		return "decl.Restrict"
	case meta.Cascade:
		return "decl.Cascade"
	case meta.SetNull:
		return "decl.SetNull"
	case meta.SetDefault:
		return "decl.SetDefault"
	default:
		return fmt.Sprintf("decl.ReferentialAction(%q)", string(action))
	}
}

// fkActionArgs renders the trailing OnDelete/OnUpdate FK options for a foreign
// key, e.g. ", decl.OnDelete(decl.Cascade)", or "" when both are default.
func fkActionArgs(fk meta.ForeignKey) string {
	var s string
	if expr := ReferentialActionExpr(fk.OnDelete); expr != "" {
		s += ", decl.OnDelete(" + expr + ")"
	}
	if expr := ReferentialActionExpr(fk.OnUpdate); expr != "" {
		s += ", decl.OnUpdate(" + expr + ")"
	}
	return s
}

// declTypeExprForCatalog is DeclTypeExpr, but a column whose engine type names
// a declared enum renders as decl.EnumType(name) instead of the generic
// decl.Type(name) fallback DeclTypeExpr would otherwise use — matching what a
// hand-written catalog declaration for that column would use, and what the
// binding generator's enum-column resolution (columnGoType) requires to map
// the column to the generated Go enum type rather than treating it as an
// opaque type.
func declTypeExprForCatalog(column meta.Column, dialect string, enumNames map[string]bool) string {
	engine, _ := column.Type.Engine(dialect)
	var expr string
	if enumNames[engine.Name] {
		expr = fmt.Sprintf("decl.EnumType(%q)", engine.Name)
	} else {
		expr = declElementTypeExpr(engine, dialect)
	}
	if engine.Array {
		return "decl.Array(" + expr + ")"
	}
	return expr
}

// DeclTypeExpr maps an engine type name back to the decl package expression
// that produces it. Types without a portable name fall back to decl.Type,
// which keeps the engine name verbatim. It is exported for reuse by the
// migration autogenerator, which renders the same decl vocabulary.
func DeclTypeExpr(column meta.Column, dialect string) string {
	engine, _ := column.Type.Engine(dialect)
	expr := declElementTypeExpr(engine, dialect)
	if engine.Array {
		return "decl.Array(" + expr + ")"
	}
	return expr
}

// declElementTypeExpr renders the decl expression for a (non-array) engine
// type; DeclTypeExpr wraps it in decl.Array for array columns.
func declElementTypeExpr(engine meta.Engine, dialect string) string {
	dataType := strings.ToLower(strings.TrimSpace(engine.Name))
	if dialect == "mysql" {
		switch dataType {
		case "bool", "boolean":
			return "decl.Bool"
		case "smallint":
			return "decl.SmallInt"
		case "int", "integer":
			return "decl.Int"
		case "bigint":
			return "decl.BigInt"
		case "float":
			return "decl.Float"
		case "double":
			return "decl.Double"
		case "decimal":
			return fmt.Sprintf("decl.Decimal(%d, %d)", engine.Precision, engine.Scale)
		case "text":
			return "decl.Text"
		case "varchar":
			return fmt.Sprintf("decl.VarChar(%d)", engine.Length)
		case "char":
			return fmt.Sprintf("decl.Char(%d)", engine.Length)
		case "blob":
			return "decl.Bytes"
		case "json":
			return "decl.JSON"
		case "date":
			return "decl.Date"
		case "time":
			return "decl.Time"
		case "datetime":
			return "decl.Timestamp"
		}
		return fmt.Sprintf("decl.Type(%q)", dataType)
	}
	switch dataType {
	case "bool":
		return "decl.Bool"
	case "int2":
		return "decl.SmallInt"
	case "int4":
		return "decl.Int"
	case "int8":
		return "decl.BigInt"
	case "float4":
		return "decl.Float"
	case "float8":
		return "decl.Double"
	case "numeric":
		return fmt.Sprintf("decl.Decimal(%d, %d)", engine.Precision, engine.Scale)
	case "text":
		return "decl.Text"
	case "varchar":
		return fmt.Sprintf("decl.VarChar(%d)", engine.Length)
	case "bpchar":
		return fmt.Sprintf("decl.Char(%d)", engine.Length)
	case "uuid":
		return "decl.UUID"
	case "bytea":
		return "decl.Bytes"
	case "jsonb":
		return "decl.JSON"
	case "date":
		return "decl.Date"
	case "time":
		return "decl.Time"
	case "timestamptz":
		return "decl.Timestamp"
	}
	return fmt.Sprintf("decl.Type(%q)", dataType)
}
