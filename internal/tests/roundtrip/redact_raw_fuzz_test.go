package roundtrip_test

import (
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// FuzzRedactRawParser exercises the WithRawParser path over arbitrary SQL: a
// statement is parsed, redacted with the raw re-parser enabled, and the result must
// still compile. Because redaction re-parses any Raw fragment the frontend produced
// (a construct sqlkit does not model natively), this fuzzes the parse → rewrite →
// substitute loop that FuzzRedactRobust deliberately skips. The contract: for any
// parseable, compilable statement, redaction with a raw parser must not panic and
// must keep the statement compilable.
func FuzzRedactRawParser(f *testing.F) {
	for _, stmt := range seedStatements(f) {
		f.Add(stmt)
	}
	// A few raw-leaning seeds so the corpus starts with fragments the typed builder
	// does not model and the parser round-trips as Raw.
	f.Add(`SELECT * FROM users WHERE name = 'x' AND created_at > now() - interval '1 day'`)
	f.Add(`SELECT name FROM users WHERE position('a' in name) > 0`)

	m := redaction.New(
		redaction.Columns("*.name", "*.email", "*.ssn", "*.token", "*.password"),
		redaction.WithRawParser(postgres.ParseOne),
	)

	f.Fuzz(func(t *testing.T, stmt string) {
		q, err := postgres.ParseOne(stmt)
		if err != nil || q == nil {
			return
		}
		if _, _, err := q.ToSQL(sql.PostgreSQLDialect{}); err != nil {
			return // original does not compile; not a redaction concern
		}

		out := m.Apply(q) // must not panic
		once, _, err := out.ToSQL(sql.PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("raw-parser redaction broke compilation: %v\n  stmt: %s", err, stmt)
		}
		twice, _, err := m.Apply(out).ToSQL(sql.PostgreSQLDialect{})
		if err != nil || once != twice {
			t.Fatalf("raw-parser redaction not idempotent\n  stmt:  %s\n  once:  %s\n  twice: %s", stmt, once, twice)
		}
	})
}
