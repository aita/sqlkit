//go:build sqlparser_rs

package sqlparser

import (
	"encoding/json"
	"testing"
)

func TestParseSimpleSelect(t *testing.T) {
	stmts, err := Parse(DialectPostgreSQL, "SELECT id, name FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	// The native AST is externally tagged: a SELECT is a Query statement.
	var stmt map[string]json.RawMessage
	if err := json.Unmarshal(stmts[0], &stmt); err != nil {
		t.Fatalf("unmarshal statement: %v", err)
	}
	if _, ok := stmt["Query"]; !ok {
		t.Fatalf("expected a Query statement, got keys %v", keys(stmt))
	}
}

func TestParseMultipleStatements(t *testing.T) {
	stmts, err := Parse(DialectGeneric, "SELECT 1; SELECT 2;")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want 2", len(stmts))
	}
}

func TestParseSyntaxErrorIsParseError(t *testing.T) {
	_, err := Parse(DialectPostgreSQL, "SELECT FROM WHERE )(")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if _, ok := err.(*ParseError); !ok {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
}

func TestParseDialectDifference(t *testing.T) {
	// Backtick-quoted identifiers parse under MySQL but not under PostgreSQL.
	if _, err := Parse(DialectMySQL, "SELECT `id` FROM `users`"); err != nil {
		t.Fatalf("MySQL backticks should parse: %v", err)
	}
	if _, err := Parse(DialectPostgreSQL, "SELECT `id` FROM `users`"); err == nil {
		t.Fatal("PostgreSQL should reject backtick identifiers")
	}
}

func TestParseZeroDialectIsGeneric(t *testing.T) {
	// The zero value of Dialect must behave like DialectGeneric.
	if _, err := Parse("", "SELECT 1"); err != nil {
		t.Fatalf("zero dialect should parse as generic: %v", err)
	}
}

func TestParseUnknownDialectErrors(t *testing.T) {
	_, err := Parse("nope", "SELECT 1")
	if err == nil {
		t.Fatal("expected an error for an unknown dialect")
	}
	if _, ok := err.(*ParseError); !ok {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
}

func TestEveryAdvertisedDialectParses(t *testing.T) {
	// Every dialect from Dialects() must be accepted by the Rust side.
	for _, d := range Dialects() {
		if _, err := Parse(d, "SELECT 1"); err != nil {
			t.Errorf("dialect %q rejected: %v", d, err)
		}
	}
}

func TestParseEmptyInput(t *testing.T) {
	stmts, err := Parse(DialectGeneric, "")
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	if len(stmts) != 0 {
		t.Fatalf("got %d statements, want 0", len(stmts))
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
