package analysis

import (
	"regexp"
	"strings"

	"github.com/aita/sqlkit/sql"
)

var (
	// placeholderRe matches a bind placeholder in any common dialect form:
	// $1, ?, @p1, :name.
	placeholderRe = regexp.MustCompile(`\$\d+|@p\d+|:[a-zA-Z_]\w*|\?`)
	// stringLiteralRe matches a single-quoted string literal, treating a doubled
	// quote ('') as an escaped quote within the literal rather than its end, so
	// 'O''Brien' collapses to a single "?" like any other value instead of two.
	stringLiteralRe = regexp.MustCompile(`'(?:[^']|'')*'`)
	// numberLiteralRe matches a standalone numeric literal.
	numberLiteralRe = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
	// whitespaceRe matches a run of whitespace.
	whitespaceRe = regexp.MustCompile(`\s+`)
	// limitOffsetRe matches the numeric operand of a LIMIT or OFFSET clause.
	// LIMIT/OFFSET counts are compiled straight into the SQL as integer literals
	// (never as bind placeholders), so two otherwise-identical paginated reads
	// that differ only in page size or offset would fingerprint differently
	// unless those numerals are canonicalized here. Only the operand is captured
	// for rewriting (the keyword is preserved), so structural numbers elsewhere
	// in the statement are left untouched.
	limitOffsetRe = regexp.MustCompile(`(?i)(\b(?:LIMIT|OFFSET)\s+)\d+`)
	// commaRe matches a list separator together with any surrounding spaces. The
	// value passes ahead collapse every literal to "?", so a comma in normalized
	// text is always a separator (SELECT list, IN list, VALUES row, call args),
	// never string content; rewriting it to a single ", " makes separator spacing
	// canonical so the value-list passes below can assume one form.
	commaRe = regexp.MustCompile(`\s*,\s*`)
	// inListRe matches an "IN (?, ?, …)" list of two or more placeholders and is
	// rewritten to the single-item "IN (?)", so that membership tests differing
	// only in the number of values share a fingerprint. The keyword anchor keeps
	// it from touching a function call's argument list, and requiring every item
	// to be the canonical "?" leaves "IN (col_a, col_b)" — a structural list of
	// non-values — untouched and excludes "IN (SELECT …)" subqueries. It runs
	// after placeholders are unified to "?", so it covers IN, NOT IN, and the
	// raw-fragment placeholders collapsed above alike. The keyword is captured and
	// re-emitted so its original case survives on the parser-free path.
	inListRe = regexp.MustCompile(`(?i)\b(IN)\s*\(\?(?:, \?)+\)`)
	// valuesRowsRe matches a "VALUES (…), (…), …" run of two or more rows whose
	// values are all placeholders and keeps only the first row, so inserts (and
	// standalone VALUES queries) that differ only in row count share a
	// fingerprint. The first row is captured so its width — the column count,
	// which is structural — is preserved; rows holding a non-placeholder value
	// do not match, mirroring the IN guard.
	valuesRowsRe = regexp.MustCompile(`(?i)(\bVALUES\s*\(\?(?:, \?)*\))(?:, \(\?(?:, \?)*\))+`)
)

// Fingerprint returns the normalized form of a parsed query used to group
// repeats. It recompiles the AST through a dialect that emits a constant "?" for
// every bind placeholder (the parser has already collapsed literals to
// placeholders). Text passes then canonicalize the values that do not flow
// through Placeholder: a $N/?/:name placeholder that arrived verbatim as a raw
// fragment (sql.Raw, as the postgres frontend produces) is collapsed to the
// canonical "?", LIMIT/OFFSET counts (compiled as plain integers) are collapsed,
// and variable-length value lists are made count-insensitive — an "IN (?, …)" of
// placeholders and a "VALUES (…), (…), …" run of placeholder rows each collapse
// to a single element. The result is therefore numbering-, value-, and
// arity-independent. ok is false when q is nil or fails to compile, in which
// case callers fall back to FingerprintRaw on the SQL text.
func Fingerprint(q sql.Query, dialect sql.Dialect) (string, bool) {
	if q == nil {
		return "", false
	}
	// Compile with the caller's concrete dialect, not a wrapper. A wrapper that
	// embeds sql.Dialect to override Placeholder would hide the dialect's optional
	// capability interfaces (ArrayDialect, JSON, UpsertDialect, FullTextSearcher,
	// …) from the compiler's type assertions, so any query using one would fail to
	// compile and Fingerprint would give up (ok=false). Every bind placeholder the
	// dialect emits ($N, ?, @pN, :name) is collapsed to a constant "?" by the pass
	// below, so the per-dialect placeholder spelling does not reach the fingerprint.
	normalized, _, err := q.ToSQL(dialect)
	if err != nil {
		return "", false
	}
	// Collapse every bind placeholder to the canonical "?" — both the ones the
	// dialect just emitted and any that reached the AST verbatim as a raw fragment
	// (e.g. sql.Raw("$1")), which never call Placeholder.
	normalized = placeholderRe.ReplaceAllString(normalized, "?")
	// LIMIT/OFFSET counts are integer literals in the SQL, not placeholders;
	// collapse their operands so paginated reads that differ only in page size
	// or offset share a fingerprint.
	normalized = limitOffsetRe.ReplaceAllString(normalized, "${1}?")
	// Squeeze whitespace and canonicalize separator spacing first so the
	// value-list passes see one form, then make IN/VALUES arity-insensitive.
	return collapseValueLists(collapse(normalized)), true
}

// collapseValueLists makes IN lists and VALUES row sets count-insensitive in an
// already placeholder-unified, comma-canonicalized statement: "IN (?, ?, …)"
// becomes "IN (?)" and a "VALUES (…), (…), …" run keeps only its first row. Both
// passes match only runs of the canonical "?", so structural lists such as
// IN (col_a, col_b) and IN (SELECT …) subqueries are left intact. It is shared
// by Fingerprint and FingerprintRaw so both paths normalize arity identically.
func collapseValueLists(s string) string {
	s = inListRe.ReplaceAllString(s, "${1} (?)")
	s = valuesRowsRe.ReplaceAllString(s, "${1}")
	return s
}

// FingerprintRaw normalizes SQL text without a parser: string and numeric
// literals and bind placeholders all collapse to ?, whitespace is squeezed, and
// IN/VALUES lists are made count-insensitive. It is the fallback for Fingerprint
// when no parsed AST is available.
func FingerprintRaw(sqlText string) string {
	// Order matters: collapse string literals first (their contents may contain
	// digits), then placeholders such as $1 (before the numeric pass would
	// strip their digits), then bare numeric literals.
	s := stringLiteralRe.ReplaceAllString(sqlText, "?")
	s = placeholderRe.ReplaceAllString(s, "?")
	s = numberLiteralRe.ReplaceAllString(s, "?")
	return collapseValueLists(collapse(s))
}

// collapse squeezes runs of whitespace to single spaces, canonicalizes list
// separators to a single ", ", and trims the result.
func collapse(s string) string {
	s = whitespaceRe.ReplaceAllString(s, " ")
	s = commaRe.ReplaceAllString(s, ", ")
	return strings.TrimSpace(s)
}
