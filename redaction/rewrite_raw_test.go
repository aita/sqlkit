package redaction_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// fakeRawParser is a hermetic stand-in for a real SQL frontend (postgres.ParseOne
// et al.), avoiding any cgo/parser dependency in the root module's tests. It models
// only the handful of fragments the WithRawParser tests need: it dispatches on the
// exact statement text the redactor passes ("SELECT <fragment>" for an expression
// fragment, or a RawSQL statement's text) and returns the typed AST a real parser
// would. Anything it does not recognize reports an error, exercising the
// does-not-parse fail-open path.
func fakeRawParser(statement string) (sql.Query, error) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	switch strings.TrimSpace(statement) {
	// Expression fragments: the redactor wraps a Raw(...) fragment as "SELECT <frag>"
	// and lifts the first projection back out via firstSelectExpr.
	case "SELECT name = 'taro'":
		// A bare expression: firstSelectExpr returns it directly (no alias).
		return sql.Select(sql.Eq(sql.Col("name"), sql.Val("taro"))), nil
	case "SELECT lower(name) = 'taro'":
		return sql.Select(sql.Eq(sql.Func("lower", sql.Col("name")), sql.Val("taro"))), nil
	case "SELECT email = 'x@y.z'":
		// Non-sensitive column: parsed, masked, nothing changes.
		return sql.Select(sql.Eq(sql.Col("email"), sql.Val("x@y.z"))), nil
	case "SELECT name = 'aliased' AS c":
		// An AS alias on the projection: firstSelectExpr must look through it.
		return sql.Select(sql.As(sql.Eq(sql.Col("name"), sql.Val("aliased")), "c")), nil
	case "SELECT name":
		// Parses to a bare column projection (no maskable literal).
		return sql.Select(sql.Col("name")), nil
	case "SELECT 'frag-modeled-as-raw'":
		// The frontend parses but cannot model it: it comes back as RawExpression,
		// so rewriteRawExpr keeps the original raw fragment (ok=false).
		return sql.Select(sql.Raw("'frag-modeled-as-raw'")), nil
	case "SELECT *":
		// Parses to a SelectStmt whose first selection is a "*" (not an Expression):
		// firstSelectExpr reports ok=false.
		return sql.Select(sql.Star()), nil

	// Whole-statement RawSQL: the redactor re-parses and rewrites the result.
	case "SELECT * FROM users WHERE name = 'taro'":
		return sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro"))), nil
	case "RETURN RAW":
		// Parses, but the result is still a RawQuery: rewriteQuery keeps the original.
		return sql.RawSQL("RETURN RAW"), nil
	case "RETURN NIL":
		// Parser succeeds but yields a nil query: rewriteQuery's nil guard keeps q.
		return nil, nil
	}
	return nil, errors.New("fakeRawParser: cannot parse " + statement)
}

// TestWithRawParserExprFragment pins that, with a parser configured, a Raw(...)
// expression fragment is re-parsed and its embedded sensitive literal masked — the
// gap the heuristic identifier scan cannot close (a literal *inside* the fragment).
func TestWithRawParserExprFragment(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"), redaction.WithRawParser(fakeRawParser))

	tests := []struct {
		name string
		raw  string
		want []any
	}{
		{"bare expr masks embedded literal", "name = 'taro'", []any{"*****"}},
		{"wrapped column masks embedded literal", "lower(name) = 'taro'", []any{"*****"}},
		{"non-sensitive column left intact", "email = 'x@y.z'", []any{"x@y.z"}},
		{"alias on projection looked through", "name = 'aliased' AS c", []any{"*****"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := sql.Select(sql.Star()).From(users).Where(sql.Raw(tt.raw))
			if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWithRawParserFailOpen pins the fail-open branches of rewriteRawExpr: when the
// fragment does not parse, comes back raw (unmodeled), or yields no expression, the
// original Raw fragment is left untouched. None of these must drop or alter the
// fragment.
func TestWithRawParserFailOpen(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"), redaction.WithRawParser(fakeRawParser))

	tests := []struct {
		name string
		raw  string
	}{
		{"does not parse", "this is not parseable"},
		{"frontend models it as raw", "'frag-modeled-as-raw'"},
		{"first selection is not an expression", "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Raw(tt.raw), sql.Val(1)))
			out := m.Apply(q)
			text, _, err := out.ToSQL(sql.PostgreSQLDialect{})
			if err != nil {
				t.Fatalf("ToSQL: %v", err)
			}
			// The raw fragment must survive verbatim in the rendered SQL.
			if !strings.Contains(text, tt.raw) {
				t.Errorf("raw fragment %q not preserved in %q", tt.raw, text)
			}
		})
	}
}

// TestWithRawParserNotConfigured pins that, without a parser, a Raw fragment is left
// to the heuristic scan: a literal embedded in the fragment is out of reach (the
// documented limitation), so it survives unmasked.
func TestWithRawParserNotConfigured(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))
	q := sql.Select(sql.Star()).From(users).Where(sql.Raw("name = 'taro'"))
	text, _, err := m.Apply(q).ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	if !strings.Contains(text, "name = 'taro'") {
		t.Errorf("raw fragment changed without a parser: %q", text)
	}
}

// TestWithRawParserRawStatement covers the RawQuery branch of rewriteQuery: a whole
// RawSQL(...) statement is re-parsed and its sensitive literal masked.
func TestWithRawParserRawStatement(t *testing.T) {
	m := redaction.New(redaction.Columns("users.name"), redaction.WithRawParser(fakeRawParser))
	q := sql.RawSQL("SELECT * FROM users WHERE name = 'taro'")
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("raw statement masked args = %v, want [*****]", got)
	}
}

// TestRawStatementFailOpen covers the RawQuery fail-open branches: no parser, a
// statement that does not parse, one that re-parses to a RawQuery again, and one
// that parses to nil. Each leaves the original statement untouched.
func TestRawStatementFailOpen(t *testing.T) {
	withParser := redaction.New(redaction.Columns("users.name"), redaction.WithRawParser(fakeRawParser))
	noParser := redaction.New(redaction.Columns("users.name"))

	tests := []struct {
		name string
		m    *redaction.Redactor
		stmt string
	}{
		{"no parser configured", noParser, "SELECT * FROM users WHERE name = 'taro'"},
		{"does not parse", withParser, "GARBAGE STATEMENT"},
		{"re-parses to raw again", withParser, "RETURN RAW"},
		{"parses to nil", withParser, "RETURN NIL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := sql.RawSQL(tt.stmt)
			text, _, err := tt.m.Apply(q).ToSQL(sql.PostgreSQLDialect{})
			if err != nil {
				t.Fatalf("ToSQL: %v", err)
			}
			if text != tt.stmt {
				t.Errorf("raw statement changed: got %q, want %q", text, tt.stmt)
			}
		})
	}
}
