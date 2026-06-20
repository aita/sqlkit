package sql

import "fmt"

// CompileInline compiles q to a single SQL string with every bound value
// rendered inline as a literal instead of a placeholder, so the result carries
// no separate args. A ValueExpression marked Quoted (see Val and Quote) is
// single-quoted with embedded quotes doubled; a nil value is NULL; numbers and
// booleans render verbatim.
//
// It is for display, logging, and snapshots — NOT for execution: the values are
// not parameterized, so compiling untrusted input this way risks SQL injection.
// Use ToSQL for anything sent to a database.
func CompileInline(dialect Dialect, q Query) (string, error) {
	if q == nil {
		return "", fmt.Errorf("sqlkit/sql: nil query")
	}
	stmt, ok := astOf(q).(compilable)
	if !ok {
		return "", fmt.Errorf("sqlkit/sql: %T cannot be compiled", q)
	}
	c := NewCompiler(dialect)
	c.inlineValues = true
	text, args, err := stmt.compile(c)
	if err != nil {
		return "", err
	}
	if len(args) > 0 {
		return "", fmt.Errorf("sqlkit/sql: inline compile produced %d bind args", len(args))
	}
	return text, nil
}

// inlineLiteral renders a ValueExpression as an inline SQL literal for the
// inline-values compile mode. A Quoted value is single-quoted via literal, its
// text taken with fmt.Sprint so a string-wrapping type renders through its
// String method (or its underlying string); a nil value is NULL; everything else
// (numbers, booleans) renders with its default form.
func inlineLiteral(e ValueExpression) string {
	if e.Value == nil {
		return "NULL"
	}
	if e.Quoted {
		return literal(fmt.Sprint(e.Value))
	}
	return fmt.Sprint(e.Value)
}
