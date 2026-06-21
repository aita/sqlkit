package sql

import "testing"

// TestRawQuery covers the verbatim ToSQL and compile entry points: a RawQuery
// returns its text and args unchanged and does not advance the ordinal counter.
func TestRawQuery(t *testing.T) {
	q := RawSQL("SELECT * FROM t WHERE id = $1", 7)

	if got := q.SQL(); got != "SELECT * FROM t WHERE id = $1" {
		t.Errorf("SQL() = %q", got)
	}

	text, args, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	if text != "SELECT * FROM t WHERE id = $1" || len(args) != 1 || args[0] != 7 {
		t.Errorf("ToSQL = %q, %#v", text, args)
	}

	text, args, err = q.compile(NewCompiler(PostgreSQLDialect{}))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if text != "SELECT * FROM t WHERE id = $1" || len(args) != 1 {
		t.Errorf("compile = %q, %#v", text, args)
	}
}
