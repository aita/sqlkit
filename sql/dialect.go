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

// JSONAccessor is an optional capability a Dialect implements to render JSON
// path extraction (JSONAccessExpression) in its native syntax. The compiler
// detects it with a type assertion, so adding it is not a breaking change to
// Dialect; a dialect that does not implement it rejects JSON access at compile
// time. operand is the already-compiled JSON expression, path steps are object
// keys (string) or array indices (int), and asText selects the text form.
type JSONAccessor interface {
	JSONAccess(operand string, path []any, asText bool) (string, error)
}

// CaseInsensitiveLiker is an optional capability a Dialect implements to render
// a case-insensitive LIKE (ILikeExpression) with a native operator. The
// compiler detects it with a type assertion, so it is not a breaking change to
// Dialect; a dialect without it falls back to LOWER(left) LIKE LOWER(pattern).
// left and pattern are already-compiled SQL fragments.
type CaseInsensitiveLiker interface {
	ILike(left, pattern string) string
}

// Concatenator is an optional capability for dialects whose string
// concatenation is not the standard || operator (MySQL, where || is logical OR
// and CONCAT(...) concatenates). The compiler detects it with a type assertion,
// so it is not a breaking change to Dialect; a dialect without it falls back to
// the || operator. operands are already-compiled SQL fragments.
type Concatenator interface {
	Concat(operands []string) string
}

// JSONObjectRenderer is an optional capability for dialects whose JSON object
// constructor does not use the SQL/JSON standard "key VALUE value" separator
// (MySQL takes a flat JSON_OBJECT(key, value, ...) list). The compiler detects
// it with a type assertion, so it is not a breaking change to Dialect; without
// it the standard VALUE form is used. funcName is JSON_OBJECT or
// JSON_OBJECTAGG, and pairs are already-compiled {key, value} fragments.
type JSONObjectRenderer interface {
	JSONObject(funcName string, pairs [][2]string) string
}

// UpsertClause carries the already-compiled pieces of an upsert for a dialect to
// assemble into its native syntax. Target holds the already-quoted conflict
// columns; DoUpdate distinguishes DO UPDATE from DO NOTHING; Assignments are the
// already-compiled "col = expr" fragments; WhereSQL is an optional already-
// compiled predicate ("" when none).
type UpsertClause struct {
	Target      []string
	DoUpdate    bool
	Assignments []string
	WhereSQL    string
}

// UpsertRenderer is an optional capability a Dialect implements to render an
// INSERT conflict clause (PostgreSQL ON CONFLICT, MySQL ON DUPLICATE KEY
// UPDATE). The compiler detects it with a type assertion, so it is not a
// breaking change to Dialect; a dialect without it rejects upserts at compile
// time. The returned fragment is appended to the INSERT, leading space included.
type UpsertRenderer interface {
	Upsert(clause UpsertClause) (string, error)
}

// ExcludedRenderer is an optional capability for dialects whose reference to an
// upsert's proposed row value is not PostgreSQL's excluded.<col> (MySQL uses
// VALUES(<col>)). The compiler detects it with a type assertion; without it the
// excluded.<col> form is used. column is the bare column name.
type ExcludedRenderer interface {
	Excluded(column string) string
}

// JoinedUpdateDeleter is an optional capability for dialects whose multi-table
// UPDATE/DELETE syntax differs from the SQL-standard FROM/USING form. MySQL puts
// the joined sources inline (UPDATE t JOIN j ON ... SET ...) and names the
// delete targets after DELETE (DELETE t FROM t JOIN ...), whereas PostgreSQL
// appends them after SET / the target with FROM / USING. The compiler detects
// it with a type assertion, so it is not a breaking change to Dialect; a
// dialect that does not implement it uses the standard FROM/USING layout.
type JoinedUpdateDeleter interface {
	// UsesInlineJoins reports the MySQL-style inline-join layout.
	UsesInlineJoins() bool
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

// jsonPathStep renders one JSON path step into the dialect-neutral parts a
// JSONAccessor needs: ok reports a valid step, key/index carry the value.
func jsonPathStep(index int, step any) (isKey bool, key string, idx int, err error) {
	switch s := step.(type) {
	case string:
		return true, s, 0, nil
	case int:
		return false, "", s, nil
	default:
		return false, "", 0, fmt.Errorf("sql: JSON path step %d must be a string key or int index, got %T", index, step)
	}
}

// isSimpleJSONKey reports whether a key needs no quoting inside a MySQL JSON
// path (a leading letter/underscore followed by letters, digits, underscores).
func isSimpleJSONKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
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

// ILike renders PostgreSQL's native case-insensitive ILIKE operator.
func (PostgreSQLDialect) ILike(left, pattern string) string {
	return "(" + left + " ILIKE " + pattern + ")"
}

// Upsert renders PostgreSQL's ON CONFLICT clause.
func (PostgreSQLDialect) Upsert(cl UpsertClause) (string, error) {
	// PostgreSQL allows ON CONFLICT DO NOTHING without a target, but DO UPDATE
	// requires a conflict target (column list or constraint) to arbitrate.
	if cl.DoUpdate && len(cl.Target) == 0 {
		return "", fmt.Errorf("sql: PostgreSQL ON CONFLICT DO UPDATE requires a conflict target")
	}
	var sb strings.Builder
	sb.WriteString(" ON CONFLICT")
	if len(cl.Target) > 0 {
		sb.WriteString(" (")
		sb.WriteString(strings.Join(cl.Target, ", "))
		sb.WriteString(")")
	}
	if !cl.DoUpdate {
		sb.WriteString(" DO NOTHING")
		return sb.String(), nil
	}
	sb.WriteString(" DO UPDATE SET ")
	sb.WriteString(strings.Join(cl.Assignments, ", "))
	if cl.WhereSQL != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(cl.WhereSQL)
	}
	return sb.String(), nil
}

// JSONAccess chains the -> / ->> operators, one per path step (the last uses
// ->> when asText), e.g. (("data" -> 'a') -> 0) ->> 'b'.
func (PostgreSQLDialect) JSONAccess(operand string, path []any, asText bool) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("sql: JSON access requires at least one path step")
	}
	expr := operand
	for i, step := range path {
		isKey, key, idx, err := jsonPathStep(i, step)
		if err != nil {
			return "", err
		}
		operator := "->"
		if asText && i == len(path)-1 {
			operator = "->>"
		}
		operand := strconv.Itoa(idx)
		if isKey {
			operand = literal(key)
		}
		expr = "(" + expr + " " + operator + " " + operand + ")"
	}
	return expr, nil
}

// MySQLDialect implements MySQL identifier quoting, placeholders, and DDL
// rendering. The query builders compile against it; RETURNING is rejected at
// compile time (MySQL has no RETURNING — read generated keys via
// Result.LastInsertId). sqlkit's schema loading is PostgreSQL-only for now.
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

// UsesInlineJoins selects MySQL's inline multi-table UPDATE/DELETE layout.
func (MySQLDialect) UsesInlineJoins() bool { return true }

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

// Concat renders MySQL string concatenation, whose || operator means logical OR.
func (MySQLDialect) Concat(operands []string) string {
	return "CONCAT(" + strings.Join(operands, ", ") + ")"
}

// Upsert renders MySQL's ON DUPLICATE KEY UPDATE clause. MySQL infers the
// conflicting key, so the target is ignored; it has no DO NOTHING form and no
// per-action WHERE, both of which are reported as errors.
func (MySQLDialect) Upsert(cl UpsertClause) (string, error) {
	if !cl.DoUpdate {
		return "", fmt.Errorf("sql: MySQL has no ON CONFLICT DO NOTHING; use INSERT IGNORE or an OnConflictDoUpdate")
	}
	if cl.WhereSQL != "" {
		return "", fmt.Errorf("sql: MySQL ON DUPLICATE KEY UPDATE does not support a WHERE clause")
	}
	return " ON DUPLICATE KEY UPDATE " + strings.Join(cl.Assignments, ", "), nil
}

// Excluded renders MySQL's reference to the proposed row value, VALUES(col).
func (d MySQLDialect) Excluded(column string) string {
	return "VALUES(" + d.QuoteName(column) + ")"
}

// JSONObject renders MySQL's flat JSON_OBJECT(key, value, ...) form, which does
// not use the SQL/JSON standard VALUE separator.
func (MySQLDialect) JSONObject(funcName string, pairs [][2]string) string {
	parts := make([]string, 0, len(pairs)*2)
	for _, kv := range pairs {
		parts = append(parts, kv[0], kv[1])
	}
	return funcName + "(" + strings.Join(parts, ", ") + ")"
}

// JSONAccess builds a JSON path string ($.a[0].b) and wraps the operand in
// JSON_EXTRACT, adding JSON_UNQUOTE for the text form.
func (MySQLDialect) JSONAccess(operand string, path []any, asText bool) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("sql: JSON access requires at least one path step")
	}
	var p strings.Builder
	p.WriteString("$")
	for i, step := range path {
		isKey, key, idx, err := jsonPathStep(i, step)
		if err != nil {
			return "", err
		}
		if !isKey {
			p.WriteString("[")
			p.WriteString(strconv.Itoa(idx))
			p.WriteString("]")
			continue
		}
		p.WriteString(".")
		if isSimpleJSONKey(key) {
			p.WriteString(key)
		} else {
			p.WriteString(`"` + strings.ReplaceAll(key, `"`, `\"`) + `"`)
		}
	}
	extract := "JSON_EXTRACT(" + operand + ", " + literal(p.String()) + ")"
	if asText {
		return "JSON_UNQUOTE(" + extract + ")", nil
	}
	return extract, nil
}
