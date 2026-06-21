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
	// stringLiteralRe matches a single-quoted string literal (best-effort; it
	// does not unescape doubled quotes).
	stringLiteralRe = regexp.MustCompile(`'[^']*'`)
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
)

// Fingerprint returns the normalized form of a parsed query used to group
// repeats. It recompiles the AST through a dialect that emits a constant "?" for
// every bind placeholder (the parser has already collapsed literals to
// placeholders). Two text passes then canonicalize the values that do not flow
// through Placeholder: a $N/?/:name placeholder that arrived verbatim as a raw
// fragment (sql.Raw, as the postgres frontend produces) is collapsed to the
// canonical "?", and LIMIT/OFFSET counts (compiled as plain integers) are
// collapsed too. The result is therefore numbering- and value-independent. ok is
// false when q is nil or fails to compile, in which case callers fall back to
// FingerprintRaw on the SQL text.
func Fingerprint(q sql.Query, dialect sql.Dialect) (string, bool) {
	if q == nil {
		return "", false
	}
	normalized, _, err := q.ToSQL(fingerprintDialect{dialect})
	if err != nil {
		return "", false
	}
	// A placeholder that reached the AST as a raw fragment (e.g. sql.Raw("$1"))
	// is re-emitted verbatim and never calls Placeholder, so collapse any such
	// $N/?/:name to the same canonical "?" that the dialect emits.
	normalized = placeholderRe.ReplaceAllString(normalized, "?")
	// LIMIT/OFFSET counts are integer literals in the SQL, not placeholders;
	// collapse their operands so paginated reads that differ only in page size
	// or offset share a fingerprint.
	normalized = limitOffsetRe.ReplaceAllString(normalized, "${1}?")
	return collapse(normalized), true
}

// fingerprintDialect renders a query for fingerprinting: it follows the
// configured dialect for identifiers and syntax but emits a single "?" for every
// bind placeholder, so two queries that differ only in their bind values — or in
// placeholder numbering — recompile to the same canonical string.
type fingerprintDialect struct{ sql.Dialect }

// Placeholder renders every bind position as "?", regardless of its index.
func (fingerprintDialect) Placeholder(int) string { return "?" }

// FingerprintRaw normalizes SQL text without a parser: string and numeric
// literals and bind placeholders all collapse to ?, and whitespace is squeezed.
// It is the fallback for FingerprintRaw when no parsed AST is available.
func FingerprintRaw(sqlText string) string {
	// Order matters: collapse string literals first (their contents may contain
	// digits), then placeholders such as $1 (before the numeric pass would
	// strip their digits), then bare numeric literals.
	s := stringLiteralRe.ReplaceAllString(sqlText, "?")
	s = placeholderRe.ReplaceAllString(s, "?")
	s = numberLiteralRe.ReplaceAllString(s, "?")
	return collapse(s)
}

// collapse squeezes runs of whitespace to single spaces and trims the result.
func collapse(s string) string {
	return strings.TrimSpace(whitespaceRe.ReplaceAllString(s, " "))
}
