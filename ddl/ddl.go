// Package ddl renders the meta schema model as SQL DDL: CREATE TABLE
// statements with primary keys and CHECK constraints, ALTER TABLE statements
// for foreign keys (emitted after all tables so declaration order never
// matters), CREATE INDEX statements, and comments. Designs written with the
// schema package and schemas read by the inspector both feed it through the
// same model.
//
// Default expressions render only when the model carries them
// (meta.Column.DefaultExpr, declared as schema.Default("now()")); an integer
// primary key with a default but no expression renders as a serial
// (PostgreSQL) or AUTO_INCREMENT (MySQL) column.
package ddl

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// Generate renders the DDL for the catalog's tables in declaration order, for
// the dialect ("postgres" or "mysql"; empty defaults to postgres).
func Generate(catalog meta.Catalog, dialectName string) (string, error) {
	var d dialect
	switch dialectName {
	case "", "postgres":
		d = postgres{}
	case "mysql":
		d = mysql{}
	default:
		return "", fmt.Errorf("ddl: unsupported dialect %q (want postgres or mysql)", dialectName)
	}

	tables := catalog.Tables
	var stmts []string
	for _, table := range tables {
		stmts = append(stmts, createTable(table, d))
	}
	for _, table := range tables {
		for _, index := range table.Indexes {
			stmts = append(stmts, createIndex(table, index, d))
		}
	}
	for _, table := range tables {
		for _, fk := range table.ForeignKeys {
			stmts = append(stmts, addForeignKey(table, fk, d))
		}
	}
	for _, table := range tables {
		if block := d.comments(table); block != "" {
			stmts = append(stmts, block)
		}
	}
	return strings.Join(stmts, "\n\n") + "\n", nil
}

// dialect renders the engine-specific parts of the DDL.
type dialect interface {
	// quote quotes an identifier.
	quote(ident string) string
	// typeName renders the column's type, including length/precision.
	typeName(column meta.Column) string
	// serialColumn renders the full type clause of an auto-generated key
	// column (e.g. "bigserial"), or reports that the column is not one.
	serialColumn(column meta.Column) (string, bool)
	// columnSuffix is appended to a column definition (MySQL inline comment).
	columnSuffix(column meta.Column) string
	// tableSuffix is appended after the closing parenthesis (MySQL table
	// comment).
	tableSuffix(table meta.Table) string
	// comments renders the table's standalone comment statements (PostgreSQL
	// COMMENT ON), or "" when comments are rendered inline.
	comments(table meta.Table) string
}

func qualify(d dialect, schemaName, name string) string {
	if schemaName == "" {
		return d.quote(name)
	}
	return d.quote(schemaName) + "." + d.quote(name)
}

func quoteAll(d dialect, names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = d.quote(name)
	}
	return strings.Join(quoted, ", ")
}

// literal renders a single-quoted SQL string literal.
func literal(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func createTable(table meta.Table, d dialect) string {
	var defs []string
	var pk []string
	for _, column := range table.Columns {
		defs = append(defs, columnDef(column, d))
		if column.PrimaryKey {
			pk = append(pk, column.Name)
		}
	}
	if len(pk) > 0 {
		defs = append(defs, "PRIMARY KEY ("+quoteAll(d, pk)+")")
	}
	for _, check := range table.Checks {
		defs = append(defs, "CONSTRAINT "+d.quote(check.Name)+" CHECK ("+check.Expression+")")
	}

	var sb strings.Builder
	sb.WriteString("CREATE TABLE " + qualify(d, table.Schema.Name, table.Name) + " (\n")
	for i, def := range defs {
		sb.WriteString("    " + def)
		if i < len(defs)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(")" + d.tableSuffix(table) + ";")
	return sb.String()
}

func columnDef(column meta.Column, d dialect) string {
	if clause, ok := d.serialColumn(column); ok {
		return d.quote(column.Name) + " " + clause + d.columnSuffix(column)
	}
	def := d.quote(column.Name) + " " + d.typeName(column)
	if !column.Nullable {
		def += " NOT NULL"
	}
	if column.DefaultExpr != "" {
		def += " DEFAULT " + column.DefaultExpr
	}
	return def + d.columnSuffix(column)
}

func createIndex(table meta.Table, index meta.Index, d dialect) string {
	kind := "INDEX"
	if index.Unique {
		kind = "UNIQUE INDEX"
	}
	return "CREATE " + kind + " " + d.quote(index.Name) + " ON " +
		qualify(d, table.Schema.Name, table.Name) + " (" + quoteAll(d, index.Columns) + ");"
}

func addForeignKey(table meta.Table, fk meta.ForeignKey, d dialect) string {
	return "ALTER TABLE " + qualify(d, table.Schema.Name, table.Name) +
		" ADD CONSTRAINT " + d.quote(fk.Name) +
		" FOREIGN KEY (" + quoteAll(d, fk.Columns) + ")" +
		" REFERENCES " + qualify(d, fk.RefSchema.Name, fk.RefTable) +
		" (" + quoteAll(d, fk.RefColumns) + ");"
}

// typeWithArgs appends (length) or (precision, scale) when the type uses one.
func typeWithArgs(name string, column meta.Column, lengthTypes, decimalTypes map[string]bool) string {
	dataType := strings.ToLower(column.DataType)
	switch {
	case lengthTypes[dataType] && column.Length > 0:
		return fmt.Sprintf("%s(%d)", name, column.Length)
	case decimalTypes[dataType] && column.Precision > 0:
		return fmt.Sprintf("%s(%d, %d)", name, column.Precision, column.Scale)
	}
	return name
}

type postgres struct{}

func (postgres) quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// pgCanonical maps udt names to the spelled-out SQL type names.
var pgCanonical = map[string]string{
	"int2":   "smallint",
	"int4":   "integer",
	"int8":   "bigint",
	"float4": "real",
	"float8": "double precision",
	"bool":   "boolean",
	"bpchar": "char",
}

var pgLengthTypes = map[string]bool{"varchar": true, "bpchar": true}
var pgDecimalTypes = map[string]bool{"numeric": true}

func (postgres) typeName(column meta.Column) string {
	name := strings.ToLower(column.DataType)
	if canonical, ok := pgCanonical[name]; ok {
		name = canonical
	}
	return typeWithArgs(name, column, pgLengthTypes, pgDecimalTypes)
}

var pgSerial = map[string]string{
	"int2": "smallserial",
	"int4": "serial",
	"int8": "bigserial",
}

func (postgres) serialColumn(column meta.Column) (string, bool) {
	if !column.PrimaryKey || !column.HasDefault || column.DefaultExpr != "" {
		return "", false
	}
	clause, ok := pgSerial[strings.ToLower(column.DataType)]
	return clause, ok
}

func (postgres) columnSuffix(meta.Column) string { return "" }
func (postgres) tableSuffix(meta.Table) string   { return "" }

func (p postgres) comments(table meta.Table) string {
	var stmts []string
	qualified := qualify(p, table.Schema.Name, table.Name)
	if table.Comment != "" {
		stmts = append(stmts, "COMMENT ON TABLE "+qualified+" IS "+literal(table.Comment)+";")
	}
	for _, column := range table.Columns {
		if column.Comment != "" {
			stmts = append(stmts, "COMMENT ON COLUMN "+qualified+"."+p.quote(column.Name)+" IS "+literal(column.Comment)+";")
		}
	}
	return strings.Join(stmts, "\n")
}

type mysql struct{}

func (mysql) quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

var mysqlLengthTypes = map[string]bool{"varchar": true, "char": true, "binary": true, "varbinary": true}
var mysqlDecimalTypes = map[string]bool{"decimal": true, "numeric": true}

func (mysql) typeName(column meta.Column) string {
	return typeWithArgs(strings.ToLower(column.DataType), column, mysqlLengthTypes, mysqlDecimalTypes)
}

var mysqlIntegerTypes = map[string]bool{
	"tinyint": true, "smallint": true, "mediumint": true,
	"int": true, "integer": true, "bigint": true,
}

func (m mysql) serialColumn(column meta.Column) (string, bool) {
	if !column.PrimaryKey || !column.HasDefault || column.DefaultExpr != "" {
		return "", false
	}
	if !mysqlIntegerTypes[strings.ToLower(column.DataType)] {
		return "", false
	}
	return m.typeName(column) + " NOT NULL AUTO_INCREMENT", true
}

func (mysql) columnSuffix(column meta.Column) string {
	if column.Comment == "" {
		return ""
	}
	return " COMMENT " + literal(column.Comment)
}

func (mysql) tableSuffix(table meta.Table) string {
	if table.Comment == "" {
		return ""
	}
	return " COMMENT=" + literal(table.Comment)
}

func (mysql) comments(meta.Table) string { return "" }
