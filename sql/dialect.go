package sql

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
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

// StringAggregator is an optional capability for dialects whose ordered string
// aggregation is not the SQL standard STRING_AGG(expr, 'delim') (MySQL spells it
// GROUP_CONCAT(expr SEPARATOR 'delim')). The compiler detects it with a type
// assertion, so it is not a breaking change to Dialect; a dialect without it
// uses the standard STRING_AGG form. arg is the already-compiled expression and
// delimiter is an already-quoted SQL string literal.
type StringAggregator interface {
	StringAgg(arg, delimiter string) string
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
// columns, or TargetConstraint the already-quoted name of a conflict constraint
// (PostgreSQL ON CONSTRAINT) — at most one is set; DoUpdate distinguishes DO
// UPDATE from DO NOTHING; Assignments are the already-compiled "col = expr"
// fragments; WhereSQL is an optional already-compiled predicate ("" when none).
type UpsertClause struct {
	Target           []string
	TargetConstraint string
	DoUpdate         bool
	Assignments      []string
	WhereSQL         string
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

// MergeDialect is an optional capability a Dialect implements to report support
// for the MERGE statement (SQL:2003). The compiler detects it with a type
// assertion, so it is not a breaking change to Dialect; a dialect without it (or
// returning false) rejects MERGE at compile time. PostgreSQL (15+) and the
// standard target support it; MySQL does not.
type MergeDialect interface {
	SupportsMerge() bool
}

// BindParamLimiter is an optional capability a Dialect implements to report the
// maximum number of bind parameters a single statement may carry. Both
// PostgreSQL and MySQL cap this at 65535, because the parameter count is a
// 16-bit field in their wire protocols. sqlkit reads it (through a Database
// configured to auto-detect) to split bulk INSERTs and chunk large IN lists so
// a statement never exceeds the limit; the compiler does not need it, so adding
// it is not a breaking change to Dialect.
type BindParamLimiter interface {
	MaxBindParams() int
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

// RegexpMatcher is an optional capability a Dialect implements to render a
// regular-expression match (RegexpExpression) in its native syntax (PostgreSQL
// ~ / ~*, MySQL REGEXP, the SQL standard LIKE_REGEX). The compiler
// detects it with a type assertion, so it is not a breaking change to Dialect;
// a dialect without it rejects regex at compile time, because regex syntax is
// genuinely non-portable. left and pattern are already-compiled SQL fragments;
// caseInsensitive requests a case-insensitive match, which a dialect that
// cannot express one inline reports as an error.
type RegexpMatcher interface {
	Regexp(left, pattern string, negate, caseInsensitive bool) (string, error)
}

// FuncRenderer is an optional capability a Dialect implements to remap a small
// set of canonical function names to its native spelling or form (e.g. NOW vs
// CURRENT_TIMESTAMP, CHAR_LENGTH vs LENGTH). The compiler detects it with a type
// assertion, so it is not a breaking change to Dialect. name is matched case-
// insensitively and args are already-compiled argument fragments. It returns
// (rendered, true) to override the default name(args...) rendering, or ("",
// false) to fall through unchanged, so a function the dialect does not remap
// (including any raw Func) passes through verbatim.
type FuncRenderer interface {
	RenderFunc(name string, args []string) (string, bool)
}

// InlineAutoIncrementPrimaryKey is an optional capability for dialects whose
// auto-increment column clause already declares the primary key inline (SQLite's
// INTEGER PRIMARY KEY AUTOINCREMENT). When it reports true, CompileCreateTable
// omits the standalone PRIMARY KEY constraint for a single auto-increment
// primary-key column, which would otherwise be a duplicate. The compiler detects
// it with a type assertion; PostgreSQL/MySQL do not implement it and keep the
// standalone constraint.
type InlineAutoIncrementPrimaryKey interface {
	InlinesAutoIncrementPrimaryKey() bool
}

// BareCompoundArmsDialect is an optional capability for dialects whose grammar
// rejects a parenthesized arm in a set operation (SQLite: "(SELECT ...) UNION
// (SELECT ...)" is a syntax error). When it reports true the compiler writes a
// plain arm bare and wraps any other (one with its own ORDER BY / LIMIT /
// OFFSET, or a nested chain of a different operator) as SELECT * FROM (...).
type BareCompoundArmsDialect interface {
	BareCompoundArms() bool
}

// DerivedValuesUnionDialect is an optional capability for dialects that cannot
// alias the columns of a VALUES row constructor used as a derived table. MySQL
// names VALUES columns column_0, column_1, … with no way to override them, so a
// VALUES source (ValuesSource) must be rendered as a UNION ALL of SELECTs whose
// first arm supplies the column names. The compiler detects it with a type
// assertion, so it is not a breaking change to Dialect; a dialect that does not
// implement it uses the standard (VALUES ...) AS alias(cols) form (PostgreSQL).
type DerivedValuesUnionDialect interface {
	// UsesValuesUnion reports the UNION-ALL-of-SELECTs rendering for a VALUES
	// derived table.
	UsesValuesUnion() bool
}

// ValuesCaster is an optional capability for dialects that need the columns of a
// VALUES derived table (ValuesSource) typed explicitly. PostgreSQL infers a bare
// VALUES parameter as text, so a join or assignment against a non-text column
// (e.g. a bigint primary key in a bulk UPDATE ... FROM (VALUES ...)) fails with
// "operator does not exist: bigint = text". The compiler casts the first row's
// values to the type this returns so the server infers each column's type; ok =
// false leaves a value uncast. A dialect that does not implement it (MySQL,
// which types VALUES columns from the bound parameters) gets no casts.
type ValuesCaster interface {
	ValuesCastType(value any) (sqlType string, ok bool)
}

// ArrayDialect is an optional capability for dialects with a first-class array
// type and the ARRAY[...] constructor / "op ANY(array)" comparison (PostgreSQL).
// MySQL has no array type and rejects array expressions
// (ArrayExpression, QuantifiedArrayExpression) at compile time. The compiler
// detects it with a type assertion, so it is not a breaking change to Dialect.
type ArrayDialect interface {
	// ArrayConstructor renders an ARRAY[...] literal from already-compiled
	// element fragments.
	ArrayConstructor(elements []string) string
}

// RowLocker is an optional capability a Dialect implements to render a SELECT
// row-locking clause (SelectBuilder.ForUpdate / ForShare / …). PostgreSQL and
// MySQL (8.0+) support it; the standard target does not, and rejects it
// at compile time. The compiler detects it with a type assertion, so it is not a
// breaking change to Dialect. strength is "UPDATE", "SHARE", "NO KEY UPDATE", or
// "KEY SHARE"; of is the already-quoted, comma-joined OF table list ("" when
// none); wait is "", "NOWAIT", or "SKIP LOCKED". It returns the leading-space
// " FOR ..." fragment, or an error when the dialect cannot express the lock.
type RowLocker interface {
	LockClause(strength, of, wait string) (string, error)
}

// DistinctOnDialect is an optional capability a Dialect implements to report
// support for SELECT DISTINCT ON (exprs) (SelectBuilder.DistinctOn), a PostgreSQL
// extension with no portable equivalent. The compiler detects it with a type
// assertion, so it is not a breaking change to Dialect; a dialect that does not
// implement it (or returns false) rejects DISTINCT ON at compile time.
type DistinctOnDialect interface {
	SupportsDistinctOn() bool
}

// GroupingSetsDialect is an optional capability a Dialect implements to allow the
// ROLLUP / CUBE / GROUPING SETS grouping elements in a GROUP BY clause.
// PostgreSQL and standard SQL support them; MySQL has no such grammar (its only
// rollup spelling is the trailing GROUP BY ... WITH ROLLUP), so the compiler
// rejects them at compile time rather than emit SQL MySQL cannot parse. The
// compiler detects the capability with a type assertion, so adding it is not a
// breaking change to Dialect; a dialect that does not implement it (or returns
// false) rejects grouping sets at compile time.
type GroupingSetsDialect interface {
	SupportsGroupingSets() bool
}

// NullsOrderingDialect is an optional capability a Dialect implements to allow
// NULLS FIRST / NULLS LAST modifiers on ORDER BY terms. PostgreSQL and standard
// SQL support them; MySQL does not, so the compiler rejects the modifier there
// rather than emit SQL MySQL cannot parse.
type NullsOrderingDialect interface {
	SupportsNullsOrdering() bool
}

// SimilarToMatcher is an optional capability a Dialect implements to render a
// SQL-standard SIMILAR TO match (SimilarToExpression). PostgreSQL and the SQL
// standard support it; MySQL does not, and rejects it at compile time
// (the compiler detects the capability with a type assertion, so adding it is
// not a breaking change to Dialect). left and pattern are already-compiled SQL
// fragments; negate selects NOT SIMILAR TO.
type SimilarToMatcher interface {
	SimilarTo(left, pattern string, negate bool) string
}

// NullSafeEqualityDialect is an optional capability for dialects that spell the
// null-safe comparison (DistinctPredicate, IS [NOT] DISTINCT FROM) differently
// from the SQL standard. MySQL has no IS DISTINCT FROM and uses the null-safe
// equality operator <=> instead. The compiler detects it with a type assertion,
// so it is not a breaking change to Dialect; a dialect without it renders the
// standard IS [NOT] DISTINCT FROM (PostgreSQL, the standard target).
// left and right are already-compiled SQL fragments; negate selects IS NOT
// DISTINCT FROM (the null-safe equality).
type NullSafeEqualityDialect interface {
	IsDistinctFrom(left, right string, negate bool) string
}

// FullTextSearcher (the optional full-text-search capability) and the
// MatchExpression it renders live in fulltext.go.

// DialectByName returns the Dialect for a name: "postgres"/"postgresql" (or
// empty, the default), "mysql", or "standard"/"ansi"/"sql" (the SQL standard /
// ANSI text target). It errors on an unknown name.
func DialectByName(name string) (Dialect, error) {
	switch name {
	case "", "postgres", "postgresql":
		return PostgreSQLDialect{}, nil
	case "mysql":
		return MySQLDialect{}, nil
	case "sqlite", "sqlite3":
		return SQLiteDialect{}, nil
	case "libsql":
		return LibSQLDialect{}, nil
	case "standard", "ansi", "sql":
		return StandardSQLDialect{}, nil
	default:
		return nil, fmt.Errorf("sql: unsupported dialect %q (want postgres, mysql, sqlite, libsql, or standard)", name)
	}
}

// sqliteFamily reports whether name is SQLite or its libSQL fork, which share
// SQLite's DDL constraints: no IDENTITY columns, no TRUNCATE, no CREATE OR
// REPLACE VIEW, and no CASCADE on DROP VIEW.
func sqliteFamily(name string) bool {
	return name == "sqlite" || name == "libsql"
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

// renderSimilarTo renders the SQL-standard SIMILAR TO / NOT SIMILAR TO match,
// shared by PostgreSQL and the standard target (which spell it identically).
func renderSimilarTo(left, pattern string, negate bool) string {
	operator := "SIMILAR TO"
	if negate {
		operator = "NOT SIMILAR TO"
	}
	return "(" + left + " " + operator + " " + pattern + ")"
}

// renderLockClause renders a "FOR <strength> [OF ...] [NOWAIT|SKIP LOCKED]"
// row-locking clause, shared by PostgreSQL and MySQL (which spell it the same).
func renderLockClause(strength, of, wait string) string {
	s := " FOR " + strength
	if of != "" {
		s += " OF " + of
	}
	if wait != "" {
		s += " " + wait
	}
	return s
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

// SupportsMerge reports MERGE support (PostgreSQL 15+ has it).
func (PostgreSQLDialect) SupportsMerge() bool { return true }

// MaxBindParams reports PostgreSQL's per-statement bind-parameter cap (65535),
// the limit of the 16-bit parameter count in the wire protocol.
func (PostgreSQLDialect) MaxBindParams() int { return 65535 }

// ValuesCastType maps a Go value to the PostgreSQL type a VALUES column should be
// cast to so the server does not default the parameter to text. It dereferences
// pointers (nullable fields) and reports ok = false for nil or an unrecognized
// type, leaving the value uncast.
func (d PostgreSQLDialect) ValuesCastType(value any) (string, bool) {
	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return "", false
	}
	// Unwrap database/sql Null* wrappers and other driver.Valuer values to the
	// value they store, so e.g. sql.NullInt64{Valid: true} casts as bigint rather
	// than falling through as an unrecognized struct (left uncast => text).
	if valuer, ok := v.Interface().(driver.Valuer); ok {
		dv, err := valuer.Value()
		if err != nil || dv == nil {
			return "", false
		}
		return d.ValuesCastType(dv)
	}
	if _, ok := v.Interface().(time.Time); ok {
		return "timestamptz", true
	}
	switch v.Kind() {
	case reflect.Bool:
		return "boolean", true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "bigint", true
	case reflect.Float32, reflect.Float64:
		return "double precision", true
	case reflect.String:
		return "text", true
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return "bytea", true
		}
	}
	return "", false
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

// pgLengthTypes/pgDecimalTypes are keyed by the canonical (rendered) name.
// pgDecimalTypes lists both spellings PostgreSQL accepts for the same type
// (DECIMAL is a pure alias for NUMERIC) — like the MySQL and Standard
// dialects' equivalent maps — so a column declared with the engine name
// "decimal" (via meta.Of or a hand-built sql.EngineType, not just the
// decl.Decimal() registry path, which normalizes to "numeric" first) still
// renders its precision/scale instead of silently dropping them.
var pgLengthTypes = map[string]bool{"varchar": true, "char": true}
var pgDecimalTypes = map[string]bool{"numeric": true, "decimal": true}

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
	return renderOnConflict(cl, "PostgreSQL")
}

// renderOnConflict renders the PostgreSQL-style ON CONFLICT clause. engine names
// the dialect in error messages.
func renderOnConflict(cl UpsertClause, engine string) (string, error) {
	// ON CONFLICT DO NOTHING needs no target, but DO UPDATE requires a conflict
	// target (column list or constraint) to arbitrate.
	if cl.DoUpdate && len(cl.Target) == 0 && cl.TargetConstraint == "" {
		return "", fmt.Errorf("sql: %s ON CONFLICT DO UPDATE requires a conflict target", engine)
	}
	var sb strings.Builder
	sb.WriteString(" ON CONFLICT")
	if cl.TargetConstraint != "" {
		sb.WriteString(" ON CONSTRAINT ")
		sb.WriteString(cl.TargetConstraint)
	} else if len(cl.Target) > 0 {
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

// Regexp renders PostgreSQL's POSIX regular-expression match operators: ~ /
// ~* for match (case-sensitive / insensitive) and !~ / !~* for the negations.
func (PostgreSQLDialect) Regexp(left, pattern string, negate, caseInsensitive bool) (string, error) {
	operator := "~"
	if caseInsensitive {
		operator = "~*"
	}
	if negate {
		operator = "!" + operator
	}
	return "(" + left + " " + operator + " " + pattern + ")", nil
}

// SimilarTo renders PostgreSQL's SIMILAR TO / NOT SIMILAR TO operator.
func (PostgreSQLDialect) SimilarTo(left, pattern string, negate bool) string {
	return renderSimilarTo(left, pattern, negate)
}

// SupportsDistinctOn reports PostgreSQL's support for SELECT DISTINCT ON.
func (PostgreSQLDialect) SupportsDistinctOn() bool { return true }

// SupportsGroupingSets reports PostgreSQL's support for ROLLUP/CUBE/GROUPING SETS.
func (PostgreSQLDialect) SupportsGroupingSets() bool { return true }

// SupportsNullsOrdering reports PostgreSQL's support for NULLS FIRST/LAST.
func (PostgreSQLDialect) SupportsNullsOrdering() bool { return true }

// LockClause renders PostgreSQL's row-locking clause; it supports all four lock
// strengths (UPDATE, NO KEY UPDATE, SHARE, KEY SHARE).
func (PostgreSQLDialect) LockClause(strength, of, wait string) (string, error) {
	return renderLockClause(strength, of, wait), nil
}

// ArrayConstructor renders PostgreSQL's ARRAY[...] literal.
func (PostgreSQLDialect) ArrayConstructor(elements []string) string {
	return "ARRAY[" + strings.Join(elements, ", ") + "]"
}

// JSONAccess chains the -> / ->> operators, one per path step (the last uses
// ->> when asText), e.g. (("data" -> 'a') -> 0) ->> 'b'.
func (PostgreSQLDialect) JSONAccess(operand string, path []any, asText bool) (string, error) {
	return renderArrowJSONAccess(operand, path, asText)
}

// renderArrowJSONAccess chains the -> / ->> operators (the last uses ->> when
// asText).
func renderArrowJSONAccess(operand string, path []any, asText bool) (string, error) {
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

// buildJSONPath renders a SQL/JSON path expression ($.a[0].b) from portable path
// steps (string keys, int indices), shared by the MySQL JSON_EXTRACT form and
// the SQL-standard JSON_VALUE/JSON_QUERY form.
func buildJSONPath(path []any) (string, error) {
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
			// Escape backslashes before quotes so a key ending in (or containing)
			// a backslash can't escape the closing quote and break out of the
			// quoted path member — e.g. `a\` must render as "a\\", not "a\".
			esc := strings.ReplaceAll(key, `\`, `\\`)
			esc = strings.ReplaceAll(esc, `"`, `\"`)
			p.WriteString(`"` + esc + `"`)
		}
	}
	return p.String(), nil
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

// MaxBindParams reports MySQL's per-statement placeholder cap (65535), the
// limit of the 16-bit parameter count in the client/server protocol.
func (MySQLDialect) MaxBindParams() int { return 65535 }

// UsesInlineJoins selects MySQL's inline multi-table UPDATE/DELETE layout.
func (MySQLDialect) UsesInlineJoins() bool { return true }

// UsesValuesUnion selects MySQL's UNION-ALL-of-SELECTs rendering for a VALUES
// derived table, since MySQL cannot alias VALUES columns.
func (MySQLDialect) UsesValuesUnion() bool { return true }

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

// StringAgg renders MySQL's GROUP_CONCAT, whose SEPARATOR keyword takes the
// delimiter literal in place of the standard STRING_AGG second argument.
func (MySQLDialect) StringAgg(arg, delimiter string) string {
	return "GROUP_CONCAT(" + arg + " SEPARATOR " + delimiter + ")"
}

// Regexp renders MySQL's REGEXP / NOT REGEXP match. MySQL has no inline
// case-insensitivity flag (it follows the column/expression collation), so a
// case-insensitive match is rejected rather than silently rendered case-
// sensitive.
func (MySQLDialect) Regexp(left, pattern string, negate, caseInsensitive bool) (string, error) {
	if caseInsensitive {
		return "", fmt.Errorf("sql: MySQL cannot express a case-insensitive regular-expression match inline; it depends on collation")
	}
	operator := "REGEXP"
	if negate {
		operator = "NOT REGEXP"
	}
	return "(" + left + " " + operator + " " + pattern + ")", nil
}

// Upsert renders MySQL's ON DUPLICATE KEY UPDATE clause. MySQL infers the
// conflicting key, so the target is ignored; it has no DO NOTHING form and no
// per-action WHERE, both of which are reported as errors.
func (MySQLDialect) Upsert(cl UpsertClause) (string, error) {
	if cl.TargetConstraint != "" {
		return "", fmt.Errorf("sql: MySQL ON DUPLICATE KEY UPDATE cannot target a named constraint; it infers the conflicting key")
	}
	if !cl.DoUpdate {
		return "", fmt.Errorf("sql: MySQL has no ON CONFLICT DO NOTHING; use INSERT IGNORE or OnConflict(DoUpdate(...))")
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

// LockClause renders MySQL's row-locking clause (8.0+). MySQL has only FOR
// UPDATE and FOR SHARE; the PostgreSQL-only NO KEY UPDATE / KEY SHARE strengths
// are rejected.
func (MySQLDialect) LockClause(strength, of, wait string) (string, error) {
	switch strength {
	case "UPDATE", "SHARE":
		return renderLockClause(strength, of, wait), nil
	default:
		return "", fmt.Errorf("sql: MySQL supports only FOR UPDATE and FOR SHARE, not FOR %s", strength)
	}
}

// IsDistinctFrom renders MySQL's null-safe comparison with the <=> operator,
// which MySQL provides in place of the SQL-standard IS [NOT] DISTINCT FROM. The
// operator is itself IS NOT DISTINCT FROM (null-safe equality); IS DISTINCT FROM
// is its negation.
func (MySQLDialect) IsDistinctFrom(left, right string, negate bool) string {
	if negate {
		return "(" + left + " <=> " + right + ")"
	}
	return "(NOT (" + left + " <=> " + right + "))"
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
	pathStr, err := buildJSONPath(path)
	if err != nil {
		return "", err
	}
	extract := "JSON_EXTRACT(" + operand + ", " + literal(pathStr) + ")"
	if asText {
		return "JSON_UNQUOTE(" + extract + ")", nil
	}
	return extract, nil
}

// SQLiteDialect implements SQLite identifier quoting, placeholders, and DDL
// rendering. SQLite is dynamically typed with type affinity, so DDL types are
// rendered leniently; it supports RETURNING (>= 3.35), the PostgreSQL-style ON
// CONFLICT upsert (including excluded.<col>), and the -> / ->> JSON operators
// (>= 3.38). Its REGEXP operator requires a host-registered regexp() function.
type SQLiteDialect struct{}

// Name identifies the dialect.
func (SQLiteDialect) Name() string { return "sqlite" }

func (d SQLiteDialect) QuoteIdent(identifier string) string {
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		parts[i] = d.QuoteName(part)
	}
	return strings.Join(parts, ".")
}

func (SQLiteDialect) QuoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (SQLiteDialect) Placeholder(int) string { return "?" }

// SupportsReturning reports RETURNING support (SQLite >= 3.35 has it).
func (SQLiteDialect) SupportsReturning() bool { return true }

// MaxBindParams is SQLite's default SQLITE_MAX_VARIABLE_NUMBER (3.32+).
func (SQLiteDialect) MaxBindParams() int { return 32766 }

// BareCompoundArms reports that SQLite rejects parenthesized set-operation arms.
func (SQLiteDialect) BareCompoundArms() bool { return true }

// UsesValuesUnion renders a VALUES derived table as a UNION ALL of SELECTs:
// SQLite can't alias a VALUES list's columns ("(VALUES ...) AS v(x)").
func (SQLiteDialect) UsesValuesUnion() bool { return true }

var sqliteLengthTypes = map[string]bool{"varchar": true, "char": true}
var sqliteDecimalTypes = map[string]bool{"numeric": true, "decimal": true}

func (SQLiteDialect) ColumnType(name string, length, precision, scale int) string {
	return typeWithArgs(strings.ToLower(name), length, precision, scale, sqliteLengthTypes, sqliteDecimalTypes)
}

// AutoIncrementColumn renders SQLite's rowid auto-increment clause, which also
// declares the primary key inline (see InlinesAutoIncrementPrimaryKey). Any
// integer type qualifies — SQLite gives a declared type containing "INT"
// integer affinity (int8, bigint, smallint, ...) — and is rendered as the
// INTEGER the rowid alias requires.
func (SQLiteDialect) AutoIncrementColumn(name string, length, precision, scale int) (string, bool) {
	if !strings.Contains(strings.ToUpper(name), "INT") {
		return "", false
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT", true
}

// InlinesAutoIncrementPrimaryKey reports that the auto-increment clause already
// declares the primary key inline, so CREATE TABLE omits the standalone
// PRIMARY KEY constraint for that column.
func (SQLiteDialect) InlinesAutoIncrementPrimaryKey() bool { return true }

// SQLite has no COMMENT syntax, inline or standalone.
func (SQLiteDialect) InlineColumnComment(string) string           { return "" }
func (SQLiteDialect) InlineTableComment(string) string            { return "" }
func (SQLiteDialect) TableCommentStatement(string, string) string { return "" }
func (SQLiteDialect) ColumnCommentStatement(string, string, string) string {
	return ""
}

// DropForeignKey is a no-op: SQLite cannot drop a single foreign key in place
// (it requires rebuilding the table), so there is no standalone statement.
func (SQLiteDialect) DropForeignKey(string, string) string { return "" }

func (SQLiteDialect) DropIndex(qualifiedIndex, _, _ string) string {
	return "DROP INDEX IF EXISTS " + qualifiedIndex + ";"
}

// Upsert renders SQLite's PostgreSQL-style ON CONFLICT clause. SQLite has no
// ON CONFLICT ON CONSTRAINT form; name the conflict columns instead.
func (d SQLiteDialect) Upsert(cl UpsertClause) (string, error) {
	if cl.TargetConstraint != "" {
		return "", fmt.Errorf("sql: SQLite has no ON CONFLICT ON CONSTRAINT; give the conflict target columns instead")
	}
	return renderOnConflict(cl, "SQLite")
}

// JSONAccess chains the -> / ->> operators like PostgreSQL (SQLite >= 3.38).
func (SQLiteDialect) JSONAccess(operand string, path []any, asText bool) (string, error) {
	return renderArrowJSONAccess(operand, path, asText)
}

// JSONObject renders SQLite's flat json_object(key, value, ...) form; the
// aggregate is json_group_object rather than the standard JSON_OBJECTAGG.
func (SQLiteDialect) JSONObject(funcName string, pairs [][2]string) string {
	name := "json_object"
	if strings.EqualFold(funcName, "JSON_OBJECTAGG") {
		name = "json_group_object"
	}
	parts := make([]string, 0, len(pairs)*2)
	for _, kv := range pairs {
		parts = append(parts, kv[0], kv[1])
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

// Regexp renders SQLite's REGEXP / NOT REGEXP operator. It requires a host-
// registered regexp() function (SQLite ships no built-in implementation). SQLite
// has no inline case-insensitivity flag, so a case-insensitive match is rejected.
func (SQLiteDialect) Regexp(left, pattern string, negate, caseInsensitive bool) (string, error) {
	if caseInsensitive {
		return "", fmt.Errorf("sql: SQLite cannot express a case-insensitive regular-expression match inline")
	}
	operator := "REGEXP"
	if negate {
		operator = "NOT REGEXP"
	}
	return "(" + left + " " + operator + " " + pattern + ")", nil
}

// RenderFunc remaps canonical function names to SQLite's spelling.
func (SQLiteDialect) RenderFunc(name string, args []string) (string, bool) {
	switch strings.ToUpper(name) {
	case "NOW", "CURRENT_TIMESTAMP":
		return "CURRENT_TIMESTAMP", true
	case "CHAR_LENGTH", "LENGTH":
		return "LENGTH(" + strings.Join(args, ", ") + ")", true
	}
	return "", false
}

// LibSQLDialect renders SQL for libSQL (the Turso SQLite fork). libSQL is wire-
// and SQL-compatible with SQLite, so it embeds SQLiteDialect and inherits its
// quoting, placeholders, type rendering, upsert, and JSON behavior; only the
// dialect name differs. libSQL-specific extensions (ALTER COLUMN, vector types,
// RANDOM ROWID) are not modeled yet and can be layered on as overrides.
type LibSQLDialect struct{ SQLiteDialect }

// Name identifies the dialect.
func (LibSQLDialect) Name() string { return "libsql" }

// StandardSQLDialect renders portable ANSI/SQL-standard text. It is a text
// target with no bundled driver: identifiers are double-quoted, placeholders are
// positional "?", and features the standard lacks (RETURNING, upsert) are
// rejected at compile time. JSON access uses the SQL/JSON standard JSON_VALUE /
// JSON_QUERY functions, JSON objects use the standard "key VALUE value" form,
// and regex uses the standard LIKE_REGEX predicate (an optional feature, F841,
// not implemented by every engine).
type StandardSQLDialect struct{}

// Name identifies the dialect.
func (StandardSQLDialect) Name() string { return "standard" }

func (d StandardSQLDialect) QuoteIdent(identifier string) string {
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		parts[i] = d.QuoteName(part)
	}
	return strings.Join(parts, ".")
}

func (StandardSQLDialect) QuoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (StandardSQLDialect) Placeholder(int) string { return "?" }

// SupportsReturning reports RETURNING support (the SQL standard has none).
func (StandardSQLDialect) SupportsReturning() bool { return false }

// SupportsMerge reports MERGE support (the SQL standard defines MERGE).
func (StandardSQLDialect) SupportsMerge() bool { return true }

// SupportsNullsOrdering reports support for NULLS FIRST/LAST (standard SQL).
func (StandardSQLDialect) SupportsNullsOrdering() bool { return true }

// SupportsGroupingSets reports standard SQL's support for ROLLUP/CUBE/GROUPING SETS.
func (StandardSQLDialect) SupportsGroupingSets() bool { return true }

var standardLengthTypes = map[string]bool{"varchar": true, "char": true, "character": true}
var standardDecimalTypes = map[string]bool{"numeric": true, "decimal": true}

func (StandardSQLDialect) ColumnType(name string, length, precision, scale int) string {
	return typeWithArgs(strings.ToLower(name), length, precision, scale, standardLengthTypes, standardDecimalTypes)
}

// AutoIncrementColumn renders the SQL-standard identity column clause.
func (d StandardSQLDialect) AutoIncrementColumn(name string, length, precision, scale int) (string, bool) {
	return d.ColumnType(name, length, precision, scale) + " GENERATED ALWAYS AS IDENTITY", true
}

// InlineColumnComment and the sibling comment methods return empty: the SQL
// standard has no COMMENT syntax.
func (StandardSQLDialect) InlineColumnComment(string) string           { return "" }
func (StandardSQLDialect) InlineTableComment(string) string            { return "" }
func (StandardSQLDialect) TableCommentStatement(string, string) string { return "" }
func (StandardSQLDialect) ColumnCommentStatement(string, string, string) string {
	return ""
}

func (StandardSQLDialect) DropForeignKey(qualifiedTable, quotedConstraint string) string {
	return "ALTER TABLE " + qualifiedTable + " DROP CONSTRAINT " + quotedConstraint + ";"
}

func (StandardSQLDialect) DropIndex(qualifiedIndex, _, _ string) string {
	return "DROP INDEX " + qualifiedIndex + ";"
}

// JSONAccess renders SQL/JSON standard JSON_VALUE (text) / JSON_QUERY (JSON).
func (StandardSQLDialect) JSONAccess(operand string, path []any, asText bool) (string, error) {
	pathStr, err := buildJSONPath(path)
	if err != nil {
		return "", err
	}
	fn := "JSON_QUERY"
	if asText {
		fn = "JSON_VALUE"
	}
	return fn + "(" + operand + ", " + literal(pathStr) + ")", nil
}

// Regexp renders the SQL-standard LIKE_REGEX predicate (F841), with FLAG 'i'
// for a case-insensitive match.
func (StandardSQLDialect) Regexp(left, pattern string, negate, caseInsensitive bool) (string, error) {
	expr := left + " LIKE_REGEX " + pattern
	if caseInsensitive {
		expr += " FLAG 'i'"
	}
	if negate {
		return "(NOT (" + expr + "))", nil
	}
	return "(" + expr + ")", nil
}

// SimilarTo renders the SQL-standard SIMILAR TO / NOT SIMILAR TO predicate.
func (StandardSQLDialect) SimilarTo(left, pattern string, negate bool) string {
	return renderSimilarTo(left, pattern, negate)
}

// RenderFunc remaps canonical function names to their SQL-standard spelling.
func (StandardSQLDialect) RenderFunc(name string, args []string) (string, bool) {
	switch strings.ToUpper(name) {
	case "NOW", "CURRENT_TIMESTAMP":
		return "CURRENT_TIMESTAMP", true
	case "LENGTH", "CHAR_LENGTH":
		return "CHAR_LENGTH(" + strings.Join(args, ", ") + ")", true
	}
	return "", false
}
