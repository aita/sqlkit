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
