package roundtrip_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
)

// FuzzRedactRobust parses real SQL with the heavyweight PostgreSQL/MySQL frontends
// and feeds it to the redactor, asserting the rewriter's structural contract over
// arbitrary statements: for any query a frontend accepts and the compiler renders,
// redaction must not panic, must keep the statement compilable, and must be
// idempotent. It complements the generator-based FuzzRedactNoLeak in the redaction
// package (which proves no sensitive literal leaks) by exercising the full shape of
// statements only a real parser produces.
//
// The policy masks a spread of common PII column names so most corpus statements
// have something to rewrite. A RawQuery is skipped: it is the parser's own deparse
// of a construct sqlkit does not model, opaque to the rewriter.
func FuzzRedactRobust(f *testing.F) {
	for _, stmt := range seedStatements(f) {
		f.Add(stmt)
	}

	m := redaction.New(redaction.Columns(
		"*.name", "*.email", "*.ssn", "*.password", "*.token", "*.phone",
	))

	render := func(q sql.Query, d sql.Dialect) (string, bool) {
		text, args, err := q.ToSQL(d)
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

	f.Fuzz(func(t *testing.T, stmt string) {
		for _, fe := range frontends() {
			q, err := sqlparse.ParseOne(fe.parser, stmt)
			if err != nil || q == nil || isRaw(q) {
				continue
			}
			if _, ok := render(q, fe.dialect); !ok {
				continue // original does not compile; not a redaction concern
			}

			out := m.Apply(q) // must not panic
			once, ok := render(out, fe.dialect)
			if !ok {
				t.Fatalf("[%s] redaction broke compilation of a parseable statement\n  stmt: %s", fe.name, stmt)
			}
			twice, ok := render(m.Apply(out), fe.dialect)
			if !ok || once != twice {
				t.Fatalf("[%s] redaction is not idempotent\n  stmt:  %s\n  once:  %s\n  twice: %s", fe.name, stmt, strings.TrimSpace(once), strings.TrimSpace(twice))
			}
		}
	})
}
