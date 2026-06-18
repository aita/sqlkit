package querytrace

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

// fingerprint returns the normalized form of a statement used to group repeats.
// When parsed is non-nil it recompiles the AST through fingerprintDialect, which
// emits a constant "?" for every bind placeholder (the parser has already
// collapsed literals to placeholders), so the result is numbering- and
// dialect-placeholder-independent with no text post-processing. Otherwise it
// falls back to collapsing literals and placeholders in the raw text.
func (c Config) fingerprint(sqlText string, parsed sql.Query) string {
	if parsed != nil {
		if normalized, _, err := parsed.ToSQL(fingerprintDialect{c.dialect()}); err == nil {
			return collapse(normalized)
		}
	}
	return fingerprintRaw(sqlText)
}

// fingerprintDialect renders a query for fingerprinting: it follows the
// configured dialect for identifiers and syntax but emits a single "?" for every
// bind placeholder, so two queries that differ only in their bind values — or in
// placeholder numbering — recompile to the same canonical string.
type fingerprintDialect struct{ sql.Dialect }

// Placeholder renders every bind position as "?", regardless of its index.
func (fingerprintDialect) Placeholder(int) string { return "?" }

// fingerprintRaw normalizes SQL text without a parser: string and numeric
// literals and bind placeholders all collapse to ?, and whitespace is squeezed.
func fingerprintRaw(sqlText string) string {
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
