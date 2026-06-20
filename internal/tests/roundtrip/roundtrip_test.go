// Package roundtrip hosts the parser round-trip oracle: it proves that every
// statement sqlkit's compiler emits is itself valid SQL its own grammar accepts,
// and that the rendering is stable.
//
// The oracle is the L1/L2 layer of the production-readiness plan. It lives in
// its own module because it needs the builder/compiler (the root module) and the
// real PostgreSQL and MySQL parser frontends (each a heavyweight, isolated
// module) together in one test, a combination no other module pulls in.
//
// For each statement in the SQL corpus under testdata/ the oracle runs:
//
//		text ──Parse──▶ q1 ──ToSQL──▶ text1 ──Parse──▶ q2 ──ToSQL──▶ text2
//
//	  - L1 (no invalid SQL): ToSQL on a parseable statement never errors, and the
//	    SQL it produces (text1) parses again without error — the compiler cannot
//	    emit SQL its own grammar rejects.
//	  - L2 (fixpoint): rendering the re-parsed query yields text2 == text1, so the
//	    compiler's output is stable under a further round-trip.
//
// Comparing the two *generated* renderings (text1 vs text2), never the original
// source against a rendering, keeps the oracle free of formatting-noise false
// negatives: it asserts the compiler is self-consistent, not that it preserves
// the author's whitespace or literal style.
package roundtrip_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/aita/sqlkit/sqlparse/mysql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// frontend pairs a parser with the dialect its output is rendered for and the
// corpus directories it draws statements from (its own plus the portable set).
type frontend struct {
	name    string
	parser  sqlparse.Parser
	dialect sql.Dialect
	dirs    []string
}

func frontends() []frontend {
	return []frontend{
		{
			name:    "postgres",
			parser:  postgres.New(),
			dialect: sql.PostgreSQLDialect{},
			dirs:    []string{"common", "postgres"},
		},
		{
			name:    "mysql",
			parser:  mysql.New(),
			dialect: sql.MySQLDialect{},
			dirs:    []string{"common", "mysql"},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	for _, fe := range frontends() {
		fe := fe
		t.Run(fe.name, func(t *testing.T) {
			for _, dir := range fe.dirs {
				for _, c := range loadCorpus(t, dir) {
					c := c
					t.Run(c.id, func(t *testing.T) {
						roundTrip(t, fe, c.stmt)
					})
				}
			}
		})
	}
}

// roundTrip runs the two-pass parse/render cycle for one statement and asserts
// the L1 and L2 invariants. A failure is reported (not fatal) so one bad
// statement does not mask the rest of the corpus.
func roundTrip(t *testing.T, fe frontend, stmt string) {
	t.Helper()

	q1, err := sqlparse.ParseOne(fe.parser, stmt)
	if err != nil {
		// The corpus is meant to hold statements the frontend accepts; a parse
		// failure here is a corpus bug, surfaced as a test failure.
		t.Fatalf("parse failed for corpus statement: %v\n  stmt: %s", err, stmt)
	}

	text1, _, err := q1.ToSQL(fe.dialect)
	if err != nil {
		t.Fatalf("ToSQL errored on a parseable statement (L1): %v\n  stmt: %s", err, stmt)
	}

	q2, err := sqlparse.ParseOne(fe.parser, text1)
	if err != nil {
		t.Fatalf("compiler emitted SQL its own grammar rejects (L1): %v\n  generated: %s\n  from: %s", err, text1, stmt)
	}

	// A typed statement that degrades to a verbatim RawQuery on re-parse is a
	// signal that the rendering, while syntactically accepted, is no longer
	// understood as the construct it started as — worth surfacing, not a silent
	// pass. (A statement that was already Raw on the first pass is exempt.)
	if !isRaw(q1) && isRaw(q2) {
		t.Errorf("generated SQL re-parsed only as a raw passthrough (L1):\n  generated: %s\n  from: %s", text1, stmt)
	}

	text2, _, err := q2.ToSQL(fe.dialect)
	if err != nil {
		t.Fatalf("ToSQL errored on re-parsed statement (L2): %v\n  generated: %s", err, text1)
	}

	if text1 != text2 {
		t.Errorf("rendering is not a fixpoint (L2):\n  first:  %s\n  second: %s", text1, text2)
	}
}

func isRaw(q sql.Query) bool {
	_, ok := q.(sql.RawQuery)
	return ok
}

// corpusCase is one statement and a stable id (file:index) for the subtest name.
type corpusCase struct {
	id   string
	stmt string
}

// loadCorpus reads every .sql file under testdata/<dir>, splitting each into the
// statements it holds. Statements are separated by ';'; '--' line comments and
// blank lines are ignored. Keeping the format plain text (rather than Go
// literals) lets the corpus grow by dropping in SQL — including, later,
// statements imported from external suites.
func loadCorpus(t *testing.T, dir string) []corpusCase {
	t.Helper()
	root := filepath.Join("testdata", dir)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus dir %s: %v", root, err)
	}
	var cases []corpusCase
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		path := filepath.Join(root, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read corpus file %s: %v", path, err)
		}
		base := strings.TrimSuffix(e.Name(), ".sql")
		for i, stmt := range splitStatements(string(data)) {
			cases = append(cases, corpusCase{
				id:   base + "/" + strconv.Itoa(i),
				stmt: stmt,
			})
		}
	}
	if len(cases) == 0 {
		t.Fatalf("no statements found under %s", root)
	}
	return cases
}

// splitStatements strips '--' line comments, then splits on ';' and returns the
// non-empty, trimmed statements. Statements in the corpus must not embed a ';'
// inside a string literal (none need to).
func splitStatements(content string) []string {
	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	var out []string
	for _, frag := range strings.Split(b.String(), ";") {
		if s := strings.TrimSpace(frag); s != "" {
			out = append(out, s)
		}
	}
	return out
}
