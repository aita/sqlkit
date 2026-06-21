package roundtrip_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aita/sqlkit/sqlparse"
)

// FuzzRoundTrip drives the same L1/L2 invariants as TestRoundTrip but over
// arbitrary inputs. The seed corpus is the on-disk .sql corpus, so the fuzzer
// starts from real SQL and mutates outward.
//
// Most random strings are not valid SQL for either frontend; those are simply
// ignored. The contract being fuzzed is narrow but strong: for any input a
// frontend *does* accept, the compiler must render it without erroring or
// panicking, that rendering must re-parse, and rendering the re-parse must be a
// fixpoint. No input may make ToSQL panic.
func FuzzRoundTrip(f *testing.F) {
	for _, stmt := range seedStatements(f) {
		f.Add(stmt)
	}

	f.Fuzz(func(t *testing.T, stmt string) {
		for _, fe := range frontends() {
			q1, err := sqlparse.ParseOne(fe.parser, stmt)
			if err != nil {
				// Not valid SQL for this frontend: nothing to assert.
				continue
			}

			text1, _, err := q1.ToSQL(fe.dialect)
			if err != nil {
				t.Fatalf("[%s] ToSQL errored on a parseable statement: %v\n  stmt: %s", fe.name, err, stmt)
			}

			// A RawQuery is the backing parser's own deparse of a construct sqlkit
			// does not model; its re-parseability and stability are that parser's
			// concern, not sqlkit's. sqlkit's contract for it is only that ToSQL not
			// error or panic (checked above). The invariants below test sqlkit's own
			// typed rendering, so they apply to typed statements only.
			if isRaw(q1) {
				continue
			}

			q2, err := sqlparse.ParseOne(fe.parser, text1)
			if err != nil {
				t.Fatalf("[%s] compiler emitted SQL its own grammar rejects: %v\n  generated: %s\n  from: %s", fe.name, err, text1, stmt)
			}

			if isRaw(q2) {
				t.Fatalf("[%s] generated SQL re-parsed only as a raw passthrough:\n  generated: %s\n  from: %s", fe.name, text1, stmt)
			}

			text2, _, err := q2.ToSQL(fe.dialect)
			if err != nil {
				t.Fatalf("[%s] ToSQL errored on re-parsed statement: %v\n  generated: %s", fe.name, err, text1)
			}

			if text1 != text2 {
				t.Fatalf("[%s] rendering is not a fixpoint:\n  first:  %s\n  second: %s", fe.name, text1, text2)
			}
		}
	})
}

// seedStatements returns every statement in the on-disk corpus, deduplicated,
// for use as fuzz seeds. It reads the same testdata tree the oracle uses.
func seedStatements(f *testing.F) []string {
	f.Helper()
	seen := make(map[string]struct{})
	var out []string
	for _, dir := range []string{"common", "postgres", "mysql"} {
		root := filepath.Join("testdata", dir)
		entries, err := os.ReadDir(root)
		if err != nil {
			f.Fatalf("read corpus dir %s: %v", root, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			path := filepath.Join(root, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				f.Fatalf("read corpus file %s: %v", path, err)
			}
			for _, stmt := range splitStatements(string(data)) {
				if _, ok := seen[stmt]; ok {
					continue
				}
				seen[stmt] = struct{}{}
				out = append(out, stmt)
			}
		}
	}
	return out
}
