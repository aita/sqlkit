package sql

import (
	"fmt"
	"strconv"
	"strings"
)

// Dialect captures SQL syntax details that vary by database engine. It is the
// single point where engine differences are resolved: the query builders
// compile through it, and the schema model renders DDL through it. Adding an
// engine means implementing this interface; PostgreSQL is the default and the
// most exercised.
type Dialect interface {
	// Name identifies the dialect, e.g. "postgres" or "mysql".
	Name() string

	// QuoteIdent quotes a possibly dot-qualified identifier, quoting each
	// dot-separated part (e.g. a "schema.table" reference from the compiler).
	QuoteIdent(identifier string) string

	// QuoteName quotes a single identifier as one unit, without splitting on
	// dots. DDL uses it for schema/table/column/constraint names, which already
	// arrive as separate parts and may legitimately contain a literal dot.
	QuoteName(name string) string

	// Placeholder renders the bind placeholder for the 1-based position.
	Placeholder(position int) string

	// SupportsReturning reports whether INSERT/UPDATE/DELETE may carry a
	// RETURNING clause. Dialects without it reject Returning at compile time.
	SupportsReturning() bool

	// ColumnType renders a column's engine type for DDL: the type name plus any
	// length/precision modifiers the dialect attaches (e.g. varchar(255),
	// numeric(10, 2)). length, precision and scale are 0 when not applicable.
	ColumnType(name string, length, precision, scale int) string

	// AutoIncrementColumn renders the full type clause for an auto-incrementing
	// integer primary key (PostgreSQL serial, MySQL ... AUTO_INCREMENT), or
	// reports false when name is not an integer type the dialect auto-increments.
	AutoIncrementColumn(name string, length, precision, scale int) (string, bool)

	// InlineColumnComment renders the suffix that carries a column comment within
	// the CREATE TABLE statement (MySQL), or "" when the dialect uses standalone
	// comment statements.
	InlineColumnComment(comment string) string

	// InlineTableComment renders the suffix that carries a table comment after
	// the closing parenthesis (MySQL), or "" when the dialect uses standalone
	// comment statements.
	InlineTableComment(comment string) string

	// TableCommentStatement renders a standalone table comment statement
	// (PostgreSQL COMMENT ON TABLE) for the already-quoted table reference, or ""
	// when comment is empty or the dialect comments inline.
	TableCommentStatement(quotedTable, comment string) string

	// ColumnCommentStatement renders a standalone column comment statement
	// (PostgreSQL COMMENT ON COLUMN) for the already-quoted table and column, or
	// "" when comment is empty or the dialect comments inline.
	ColumnCommentStatement(quotedTable, quotedColumn, comment string) string

	// DropForeignKey renders the statement that drops a foreign-key constraint
	// (PostgreSQL ALTER TABLE ... DROP CONSTRAINT, MySQL ... DROP FOREIGN KEY)
	// for the already-quoted table and constraint name.
	DropForeignKey(qualifiedTable, quotedConstraint string) string

	// DropIndex renders the DROP INDEX statement. PostgreSQL drops the
	// schema-qualified index (qualifiedIndex); MySQL drops the bare index
	// (quotedIndex) from its table (qualifiedTable).
	DropIndex(qualifiedIndex, quotedIndex, qualifiedTable string) string
}

// DialectByName returns the Dialect for a name ("postgres"/"postgresql" or
// "mysql"; empty defaults to postgres). It errors on an unknown name.
func DialectByName(name string) (Dialect, error) {
	switch name {
	case "", "postgres", "postgresql":
		return PostgreSQLDialect{}, nil
	case "mysql":
		return MySQLDialect{}, nil
	default:
		return nil, fmt.Errorf("sql: unsupported dialect %q (want postgres or mysql)", name)
	}
}

// typeWithArgs appends (length) or (precision, scale) when the type uses one.
func typeWithArgs(name string, length, precision, scale int, lengthTypes, decimalTypes map[string]bool) string {
	dataType := strings.ToLower(name)
	switch {
	case lengthTypes[dataType] && length > 0:
		return fmt.Sprintf("%s(%d)", name, length)
	case decimalTypes[dataType] && precision > 0:
		return fmt.Sprintf("%s(%d, %d)", name, precision, scale)
	}
	return name
}

// literal renders a single-quoted SQL string literal.
func literal(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// PostgreSQLDialect implements PostgreSQL identifier quoting, placeholders, and
// DDL rendering.
type PostgreSQLDialect struct{}

// Name identifies the dialect.
func (PostgreSQLDialect) Name() string { return "postgres" }

func (d PostgreSQLDialect) QuoteIdent(identifier string) string {
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		parts[i] = d.QuoteName(part)
	}
	return strings.Join(parts, ".")
}

func (PostgreSQLDialect) QuoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (PostgreSQLDialect) Placeholder(position int) string {
	return "$" + strconv.Itoa(position)
}

// SupportsReturning reports RETURNING support (PostgreSQL has it).
func (PostgreSQLDialect) SupportsReturning() bool { return true }

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

// pgLengthTypes/pgDecimalTypes are keyed by the canonical (rendered) name.
var pgLengthTypes = map[string]bool{"varchar": true, "char": true}
var pgDecimalTypes = map[string]bool{"numeric": true}

func (PostgreSQLDialect) ColumnType(name string, length, precision, scale int) string {
	n := strings.ToLower(name)
	if canonical, ok := pgCanonical[n]; ok {
		n = canonical
	}
	return typeWithArgs(n, length, precision, scale, pgLengthTypes, pgDecimalTypes)
}

var pgSerial = map[string]string{
	"int2": "smallserial",
	"int4": "serial",
	"int8": "bigserial",
}

func (PostgreSQLDialect) AutoIncrementColumn(name string, length, precision, scale int) (string, bool) {
	clause, ok := pgSerial[strings.ToLower(name)]
	return clause, ok
}

func (PostgreSQLDialect) InlineColumnComment(string) string { return "" }
func (PostgreSQLDialect) InlineTableComment(string) string  { return "" }

func (p PostgreSQLDialect) TableCommentStatement(quotedTable, comment string) string {
	if comment == "" {
		return ""
	}
	return "COMMENT ON TABLE " + quotedTable + " IS " + literal(comment) + ";"
}

func (p PostgreSQLDialect) ColumnCommentStatement(quotedTable, quotedColumn, comment string) string {
	if comment == "" {
		return ""
	}
	return "COMMENT ON COLUMN " + quotedTable + "." + quotedColumn + " IS " + literal(comment) + ";"
}

func (PostgreSQLDialect) DropForeignKey(qualifiedTable, quotedConstraint string) string {
	return "ALTER TABLE " + qualifiedTable + " DROP CONSTRAINT IF EXISTS " + quotedConstraint + ";"
}

func (PostgreSQLDialect) DropIndex(qualifiedIndex, _, _ string) string {
	return "DROP INDEX IF EXISTS " + qualifiedIndex + ";"
}

// MySQLDialect implements MySQL identifier quoting, placeholders, and DDL
// rendering. The query builders compile against it; RETURNING is rejected at
// compile time (MySQL has no RETURNING — read generated keys via
// Result.LastInsertId). sqlkitgen's schema loading is PostgreSQL-only for now.
type MySQLDialect struct{}

// Name identifies the dialect.
func (MySQLDialect) Name() string { return "mysql" }

func (d MySQLDialect) QuoteIdent(identifier string) string {
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		parts[i] = d.QuoteName(part)
	}
	return strings.Join(parts, ".")
}

func (MySQLDialect) QuoteName(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (MySQLDialect) Placeholder(int) string { return "?" }

// SupportsReturning reports RETURNING support (MySQL lacks it).
func (MySQLDialect) SupportsReturning() bool { return false }

var mysqlLengthTypes = map[string]bool{"varchar": true, "char": true, "binary": true, "varbinary": true}
var mysqlDecimalTypes = map[string]bool{"decimal": true, "numeric": true}

func (MySQLDialect) ColumnType(name string, length, precision, scale int) string {
	return typeWithArgs(strings.ToLower(name), length, precision, scale, mysqlLengthTypes, mysqlDecimalTypes)
}

var mysqlIntegerTypes = map[string]bool{
	"tinyint": true, "smallint": true, "mediumint": true,
	"int": true, "integer": true, "bigint": true,
}

func (d MySQLDialect) AutoIncrementColumn(name string, length, precision, scale int) (string, bool) {
	if !mysqlIntegerTypes[strings.ToLower(name)] {
		return "", false
	}
	return d.ColumnType(name, length, precision, scale) + " NOT NULL AUTO_INCREMENT", true
}

func (MySQLDialect) InlineColumnComment(comment string) string {
	if comment == "" {
		return ""
	}
	return " COMMENT " + literal(comment)
}

func (MySQLDialect) InlineTableComment(comment string) string {
	if comment == "" {
		return ""
	}
	return " COMMENT=" + literal(comment)
}

func (MySQLDialect) TableCommentStatement(string, string) string          { return "" }
func (MySQLDialect) ColumnCommentStatement(string, string, string) string { return "" }

func (MySQLDialect) DropForeignKey(qualifiedTable, quotedConstraint string) string {
	return "ALTER TABLE " + qualifiedTable + " DROP FOREIGN KEY " + quotedConstraint + ";"
}

func (MySQLDialect) DropIndex(_, quotedIndex, qualifiedTable string) string {
	return "DROP INDEX " + quotedIndex + " ON " + qualifiedTable + ";"
}
