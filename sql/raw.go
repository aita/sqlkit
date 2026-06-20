package sql

// RawQuery is a whole SQL statement kept as verbatim text plus optional bind
// arguments — the statement-level counterpart to RawExpression. It is an escape
// hatch for statements the typed builders do not model: it compiles to its text
// unchanged on every dialect, so the SQL must already be valid for the target.
//
// The sqlparse frontends use it to round-trip statements they cannot yet map to
// a typed query, deparsing the parse tree back to SQL so that every statement a
// frontend's grammar accepts still yields a usable Query.
type RawQuery struct {
	posField
	text string
	args []any
}

// RawSQL builds a RawQuery from verbatim SQL text and optional bind arguments.
// The text is emitted as-is, so any placeholders it contains must already match
// the target dialect's spelling.
func RawSQL(text string, args ...any) RawQuery {
	return RawQuery{text: text, args: args}
}

// SQL returns the verbatim statement text.
func (q RawQuery) SQL() string { return q.text }

// ToSQL returns the verbatim text and arguments unchanged, ignoring the dialect.
func (q RawQuery) ToSQL(Dialect) (string, []any, error) {
	return q.text, q.args, nil
}

// compile returns the verbatim text and arguments. A RawQuery carries its own
// placeholder spellings, so it does not advance the compiler's ordinal counter;
// it is intended as a top-level statement, not a nested subquery.
func (q RawQuery) compile(*Compiler) (string, []any, error) {
	return q.text, q.args, nil
}
