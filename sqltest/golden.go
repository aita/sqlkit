package sqltest

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit/querytrace"
)

// updateGoldenEnv is the environment variable that switches the golden helpers
// from comparing to rewriting. Set it (to any non-empty value) to regenerate the
// golden files, then commit the result:
//
//	SQLKIT_UPDATE_GOLDEN=1 go test ./...
//
// It is read rather than registered as a -update flag so that importing sqltest
// never adds a flag to an unrelated binary.
const updateGoldenEnv = "SQLKIT_UPDATE_GOLDEN"

// updateGolden reports whether the golden files should be rewritten.
func updateGolden() bool { return os.Getenv(updateGoldenEnv) != "" }

// Golden compares got against the contents of the golden file at path and fails
// the test on any difference. With the SQLKIT_UPDATE_GOLDEN environment variable
// set it rewrites the file (creating parent directories) instead of comparing,
// so a deliberate change is recorded by re-running the test rather than editing
// fixtures by hand. It is the text-golden primitive the result helpers build on,
// and works for any string a test wants to pin (rendered rows, a query log, a
// report).
func Golden(t testing.TB, path, got string) {
	t.Helper()
	if updateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("sqltest: create golden dir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("sqltest: write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sqltest: read golden %s (set %s=1 to create it): %v", path, updateGoldenEnv, err)
	}
	if got != string(want) {
		t.Errorf("sqltest: golden mismatch for %s (set %s=1 to update)\n--- got ---\n%s\n--- want ---\n%s",
			path, updateGoldenEnv, got, string(want))
	}
}

// AssertGoldenRows renders rows and compares them to the golden file at path.
// Add an ORDER BY to the query so the row order — and therefore the golden file
// — is stable. rows is consumed and closed.
func AssertGoldenRows(t testing.TB, path string, rows *stdsql.Rows) {
	t.Helper()
	got, err := RenderRows(rows)
	if err != nil {
		t.Fatalf("sqltest: render rows for %s: %v", path, err)
	}
	Golden(t, path, got)
}

// AssertGoldenQuery runs query (with args) through the DB's connection and
// compares the rendered rows to the golden file at path — the DB-method
// convenience over AssertGoldenRows. Add an ORDER BY so the row order, and so
// the golden file, is stable.
func (d *DB) AssertGoldenQuery(t testing.TB, path, query string, args ...any) {
	t.Helper()
	rows, err := d.db.SQL().QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("sqltest: golden query for %s: %v", path, err)
	}
	AssertGoldenRows(t, path, rows)
}

// AssertGoldenTrace renders trace's query log in a timing-free form and compares
// it to the golden file at path. Pinning the query log this way catches an
// accidental change in the SQL an operation issues — a query that turns into an
// N+1, a new statement, a dropped index hint — that a count-based budget would
// miss.
//
// Unlike TextReport, the rendered form leaves out everything that varies from
// run to run: durations, the total DB time, and the warnings (some of which are
// derived from timing). It keeps the operation name, the query/unique/write
// counts, and the ordered timeline of statements (kind, name, fingerprint),
// transaction control points, and marks.
func AssertGoldenTrace(t testing.TB, path string, trace *querytrace.Trace) {
	t.Helper()
	Golden(t, path, renderTraceGolden(trace))
}

// renderTraceGolden renders a trace report without any timing-dependent part, so
// the same operation produces the same text on every run.
func renderTraceGolden(trace *querytrace.Trace) string {
	r := trace.Report()
	type item struct {
		at   time.Time
		text string
	}
	items := make([]item, 0, len(r.Events)+len(r.Marks))
	for _, ev := range r.Events {
		var text string
		switch ev.Kind {
		case querytrace.EventTx:
			action := ""
			if ev.Tx != nil {
				action = string(ev.Tx.Action)
			}
			text = "TX    " + strings.ToUpper(action)
		case querytrace.EventQuery, querytrace.EventExec:
			label := ""
			if ev.Query != nil {
				label = ev.Query.Fingerprint
			}
			if ev.Name != "" {
				label = ev.Name + "  " + label
			}
			text = fmt.Sprintf("%-5s %s", strings.ToUpper(string(ev.Kind)), label)
		default:
			text = string(ev.Kind)
		}
		items = append(items, item{at: ev.StartedAt, text: text})
	}
	for _, m := range r.Marks {
		items = append(items, item{at: m.At, text: "MARK  " + m.Name})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })

	var b strings.Builder
	fmt.Fprintf(&b, "SQL trace: %s\n", r.Operation)
	fmt.Fprintf(&b, "queries=%d unique=%d writes=%d\n", r.QueryCount, r.UniqueFingerprintCount, r.WriteCount)
	if len(items) > 0 {
		b.WriteString("\ntimeline:\n")
		for i, it := range items {
			fmt.Fprintf(&b, "  %02d  %s\n", i+1, it.text)
		}
	}
	return b.String()
}

// RenderRows renders a result set as the canonical text the golden helpers
// compare: a tab-separated header line of column names, then one tab-separated
// line per row. A NULL renders as the literal NULL and byte/string columns as
// their text, so the form is stable across drivers. A backslash, tab, newline,
// or carriage return inside a value is escaped (\\, \t, \n, \r) so distinct
// results never render to the same text. It consumes and closes rows.
func RenderRows(rows *stdsql.Rows) (string, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(strings.Join(cols, "\t"))
	b.WriteByte('\n')
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		cells := make([]string, len(cols))
		for i, v := range dest {
			cells[i] = cellString(v)
		}
		b.WriteString(strings.Join(cells, "\t"))
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// cellString renders one scanned value as stable golden text.
func cellString(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return cellEscaper.Replace(string(x))
	default:
		return cellEscaper.Replace(fmt.Sprintf("%v", v))
	}
}

// cellEscaper escapes the characters that would otherwise let a value's text
// collide with the table's own separators.
var cellEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)
