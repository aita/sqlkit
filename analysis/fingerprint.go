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
)

// Fingerprint returns the normalized form of a parsed query used to group
// repeats. It recompiles the AST through a dialect that emits a constant "?" for
// every bind placeholder (the parser has already collapsed literals to
// placeholders), so the result is numbering- and dialect-placeholder-independent
// with no text post-processing. ok is false when q is nil or fails to compile,
// in which case callers fall back to FingerprintRaw on the SQL text.
func Fingerprint(q sql.Query, dialect sql.Dialect) (string, bool) {
	if q == nil {
		return "", false
	}
	normalized, _, err := q.ToSQL(fingerprintDialect{dialect})
	if err != nil {
		return "", false
	}
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
