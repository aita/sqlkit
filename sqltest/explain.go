package sqltest

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
)

// Plan is a captured query plan — the output of EXPLAIN — ready for assertions
// about how the database will run a statement: whether it falls back to a full
// table scan, whether it uses a particular index, or any substring of the plan.
// It complements the query spy (which counts statements) and the data assertions
// (which check rows) with the third question a test may care about: is the query
// served efficiently.
//
// Build one with ExplainQuery to run EXPLAIN through a live connection, or with
// NewPlan from EXPLAIN text captured elsewhere. The structural checks
// (HasSeqScan, UsesIndex) read PostgreSQL's text plan and MySQL's tabular
// EXPLAIN; for any other shape fall back to Contains on Text.
type Plan struct {
	dialect sqlkit.Dialect
	header  []string
	rows    [][]string
}

// ExplainQuery runs EXPLAIN for query (with args) through q and returns the
// captured Plan. q is any Queryer — the same *sql.DB or *sql.Tx the code under
// test uses — so the plan reflects the real schema and statistics. The query is
// not executed, only planned.
func ExplainQuery(t testing.TB, q Queryer, dialect sqlkit.Dialect, query string, args ...any) *Plan {
	t.Helper()
	header, rows := runExplain(t, q, query, args)
	return &Plan{dialect: dialect, header: header, rows: rows}
}

// ExplainQuery runs EXPLAIN through the DB's connection, supplying its dialect —
// the DB-method convenience over the package-level ExplainQuery.
func (d *DB) ExplainQuery(t testing.TB, query string, args ...any) *Plan {
	t.Helper()
	return ExplainQuery(t, d.db.SQL(), d.db.Dialect(), query, args...)
}

// NewPlan builds a Plan from EXPLAIN output captured outside this package: header
// is the column names (["QUERY PLAN"] for PostgreSQL's text format, the EXPLAIN
// columns for MySQL) and rows the corresponding cells. It is the seam the
// structural checks are tested through and lets a caller analyze a plan it
// obtained another way.
func NewPlan(dialect sqlkit.Dialect, header []string, rows [][]string) *Plan {
	return &Plan{dialect: dialect, header: header, rows: rows}
}

// Text renders the plan as the EXPLAIN output reads: a tab-separated header line
// (omitted when there is a single unnamed plan column) followed by one
// tab-separated line per row. It is what Contains searches and what a golden
// comparison would pin.
func (p *Plan) Text() string {
	var b strings.Builder
	if len(p.header) > 1 {
		b.WriteString(strings.Join(p.header, "\t"))
		b.WriteByte('\n')
	}
	for _, row := range p.rows {
		b.WriteString(strings.Join(row, "\t"))
		b.WriteByte('\n')
	}
	return b.String()
}

// Contains reports whether any plan cell contains sub, case-insensitively. It is
// the portable check when the structural helpers do not fit a given plan shape.
func (p *Plan) Contains(sub string) bool {
	low := strings.ToLower(sub)
	for _, row := range p.rows {
		for _, cell := range row {
			if strings.Contains(strings.ToLower(cell), low) {
				return true
			}
		}
	}
	return false
}

// HasSeqScan reports whether the plan includes a full table scan: a "Seq Scan"
// node on PostgreSQL, or an access type of ALL on MySQL (the EXPLAIN type column).
func (p *Plan) HasSeqScan() bool {
	if p.isMySQL() {
		for _, row := range p.rows {
			if v, ok := p.cell(row, "type"); ok && strings.EqualFold(v, "ALL") {
				return true
			}
		}
		return false
	}
	return seqScanRe.MatchString(p.Text())
}

// UsesIndex reports whether the plan reads through the named index: an
// "... using <name>" or "... on <name>" node on PostgreSQL, or the index in
// MySQL's key column.
func (p *Plan) UsesIndex(name string) bool {
	if p.isMySQL() {
		for _, row := range p.rows {
			if v, ok := p.cell(row, "key"); ok && strings.EqualFold(v, name) {
				return true
			}
		}
		return false
	}
	low := strings.ToLower(name)
	return p.Contains("using "+low) || p.Contains("index scan on "+low) || p.Contains("index only scan on "+low)
}

// AssertNoSeqScan fails the test if the plan includes a full table scan.
func (p *Plan) AssertNoSeqScan(t testing.TB) {
	t.Helper()
	if p.HasSeqScan() {
		t.Errorf("sqltest: query plan has a sequential (full table) scan:\n%s", p.Text())
	}
}

// AssertUsesIndex fails the test unless the plan reads through the named index.
func (p *Plan) AssertUsesIndex(t testing.TB, name string) {
	t.Helper()
	if !p.UsesIndex(name) {
		t.Errorf("sqltest: query plan does not use index %q:\n%s", name, p.Text())
	}
}

// AssertContains fails the test unless some plan cell contains sub.
func (p *Plan) AssertContains(t testing.TB, sub string) {
	t.Helper()
	if !p.Contains(sub) {
		t.Errorf("sqltest: query plan does not contain %q:\n%s", sub, p.Text())
	}
}

// AssertNotContains fails the test if some plan cell contains sub.
func (p *Plan) AssertNotContains(t testing.TB, sub string) {
	t.Helper()
	if p.Contains(sub) {
		t.Errorf("sqltest: query plan unexpectedly contains %q:\n%s", sub, p.Text())
	}
}

// seqScanRe matches a PostgreSQL sequential-scan node.
var seqScanRe = regexp.MustCompile(`(?i)\bseq scan\b`)

// isMySQL reports whether the plan came from MySQL, whose EXPLAIN is tabular
// rather than a single text column.
func (p *Plan) isMySQL() bool { return p.dialect != nil && p.dialect.Name() == "mysql" }

// cell returns the value of the named column in row (case-insensitive), for
// reading MySQL EXPLAIN columns such as type and key.
func (p *Plan) cell(row []string, name string) (string, bool) {
	for i, h := range p.header {
		if strings.EqualFold(h, name) && i < len(row) {
			return row[i], true
		}
	}
	return "", false
}

// runExplain executes EXPLAIN for the query and returns the result's column
// names and rows, each cell stringified.
func runExplain(t testing.TB, q Queryer, query string, args []any) ([]string, [][]string) {
	t.Helper()
	rows, err := q.QueryContext(context.Background(), "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatalf("sqltest: EXPLAIN %s: %v", query, err)
	}
	defer rows.Close()
	header, err := rows.Columns()
	if err != nil {
		t.Fatalf("sqltest: EXPLAIN columns: %v", err)
	}
	var out [][]string
	for rows.Next() {
		dest := make([]any, len(header))
		ptrs := make([]any, len(header))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("sqltest: EXPLAIN scan: %v", err)
		}
		cells := make([]string, len(header))
		for i, v := range dest {
			cells[i] = explainCell(v)
		}
		out = append(out, cells)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("sqltest: EXPLAIN: %v", err)
	}
	return header, out
}

// explainCell stringifies one scanned EXPLAIN value, rendering NULL as the empty
// string (so an absent MySQL key reads as empty rather than the literal NULL).
func explainCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}
