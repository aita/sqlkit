package roundtrip_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// This file verifies the redactor's actual masking — not just that it does not
// crash — over statements produced by the real PostgreSQL parser. Every generated
// statement ties the sentinel maskSecret to the sensitive column users.name, so
// after redaction it must be gone; the sentinel maskKeep is tied only to the
// non-sensitive users.email, so it must survive (a guard against over-masking).
//
// Going through the parser matters: its AST shapes differ from the builder's, so
// this catches a masking gap the builder-driven FuzzRedactNoLeak (in the redaction
// package) cannot reach.

const (
	maskSecret = "PIISECRETVALUE"
	maskKeep   = "KEEPNONPIIVALUE"
)

// fuzzBytes is a tiny byte-driven chooser the generator reads to pick shapes, so
// libFuzzer's mutation of the input bytes explores the space of statements.
type fuzzBytes struct {
	b []byte
	i int
}

func (g *fuzzBytes) next() byte {
	if g.i >= len(g.b) {
		g.i++
		return 0
	}
	v := g.b[g.i]
	g.i++
	return v
}

func (g *fuzzBytes) choice(n int) int { return int(g.next()) % n }

func (g *fuzzBytes) bool() bool { return g.next()&1 == 1 }

// sensitiveCase is one generated statement together with whether it carries the
// maskKeep control literal (so the test only asserts survival when present).
type sensitiveCase struct {
	sql     string
	hasKeep bool
}

// namePredicate builds a predicate that ties ref (an expression denoting
// users.name) to the secret, choosing the comparison form from g.
func namePredicate(g *fuzzBytes, ref string) string {
	switch g.choice(5) {
	case 0:
		return ref + " = '" + maskSecret + "'"
	case 1:
		return "'" + maskSecret + "' = " + ref
	case 2:
		return ref + " IN ('" + maskSecret + "', 'other')"
	case 3:
		return ref + " LIKE '" + maskSecret + "'"
	default:
		return ref + " BETWEEN '" + maskSecret + "' AND 'zzz'"
	}
}

// nameRef wraps a column reference so it still reads users.name.
func nameRef(g *fuzzBytes, base string) string {
	if g.bool() {
		return "lower(" + base + ")"
	}
	return base
}

// buildSensitiveSQL assembles a PostgreSQL statement that compares users.name to
// the secret through a shape chosen by g: direct, derived table, CTE, UNION CTE,
// correlated subquery, INSERT, UPDATE, or DELETE.
func buildSensitiveSQL(g *fuzzBytes) sensitiveCase {
	switch g.choice(8) {
	case 0:
		// Direct, with a non-sensitive control literal that must survive.
		return sensitiveCase{
			sql:     "SELECT * FROM users u WHERE " + namePredicate(g, nameRef(g, "u.name")) + " AND email = '" + maskKeep + "'",
			hasKeep: true,
		}
	case 1:
		return sensitiveCase{sql: "SELECT * FROM users WHERE " + namePredicate(g, nameRef(g, "name"))}
	case 2:
		return sensitiveCase{sql: "SELECT * FROM (SELECT name FROM users) t WHERE " + namePredicate(g, nameRef(g, "t.name"))}
	case 3:
		return sensitiveCase{sql: "WITH c AS (SELECT name FROM users) SELECT * FROM c WHERE " + namePredicate(g, nameRef(g, "c.name"))}
	case 4:
		return sensitiveCase{sql: "WITH c AS (SELECT name FROM users UNION SELECT name FROM users) SELECT * FROM c WHERE " + namePredicate(g, nameRef(g, "c.name"))}
	case 5:
		return sensitiveCase{sql: "SELECT * FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE " + namePredicate(g, nameRef(g, "u.name")) + ")"}
	case 6:
		return sensitiveCase{
			sql:     "INSERT INTO users (name, email) VALUES ('" + maskSecret + "', '" + maskKeep + "')",
			hasKeep: true,
		}
	default:
		return sensitiveCase{
			sql:     "UPDATE users SET name = '" + maskSecret + "', email = '" + maskKeep + "' WHERE id = 1",
			hasKeep: true,
		}
	}
}

// renderedHaystack compiles q and folds the SQL text and every string bind argument
// into one string, so a value is found whether it rode in the text or as an arg.
func renderedHaystack(q sql.Query) (string, bool) {
	text, args, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		return "", false
	}
	for _, v := range args {
		if s, ok := v.(string); ok {
			text += "\x00" + s
		}
	}
	return text, true
}

// FuzzRedactParsedNoLeak generates a statement that ties the secret to users.name,
// parses it with the real PostgreSQL frontend, redacts it (policy users.name), and
// asserts the secret never survives while the non-sensitive control value does.
func FuzzRedactParsedNoLeak(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{1})
	for i := 0; i < 256; i++ {
		f.Add([]byte{byte(i), byte(i * 7), byte(i * 13)})
	}

	m := redaction.New(redaction.Columns("users.name"), redaction.WithRawParser(postgres.ParseOne))
	f.Fuzz(func(t *testing.T, data []byte) {
		g := &fuzzBytes{b: data}
		tc := buildSensitiveSQL(g)

		q, err := postgres.ParseOne(tc.sql)
		if err != nil || q == nil {
			return // a combination the frontend rejects is not a masking concern
		}
		before, ok := renderedHaystack(q)
		if !ok || !strings.Contains(before, maskSecret) {
			return // the secret did not survive parsing intact; nothing to assert
		}

		out := m.Apply(q)
		after, ok := renderedHaystack(out)
		if !ok {
			t.Fatalf("redacted query failed to compile\n  sql: %s", tc.sql)
		}
		if strings.Contains(after, maskSecret) {
			text, args, _ := out.ToSQL(sql.PostgreSQLDialect{})
			t.Fatalf("LEAK: secret survived redaction of parsed SQL\n  in:   %s\n  out:  %s\n  args: %v", tc.sql, text, args)
		}
		if tc.hasKeep && strings.Contains(before, maskKeep) && !strings.Contains(after, maskKeep) {
			t.Fatalf("OVER-MASK: non-sensitive value was masked\n  in:  %s", tc.sql)
		}
	})
}
