package analysis_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// TestFingerprintUnifiesPlaceholders checks the parsed path: a recompiled query
// renders every bind as "?", so queries differing only in their values (or
// placeholder numbering) share a fingerprint with no numbered placeholder left.
func TestFingerprintUnifiesPlaceholders(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q1 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(1)), sql.Eq(sql.Col("name"), sql.Val("a"))))
	q2 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(999)), sql.Eq(sql.Col("name"), sql.Val("z"))))

	f1, ok1 := analysis.Fingerprint(q1, sql.PostgreSQLDialect{})
	f2, ok2 := analysis.Fingerprint(q2, sql.PostgreSQLDialect{})
	if !ok1 || !ok2 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok1, ok2)
	}
	if f1 != f2 {
		t.Errorf("fingerprints differ for value-only difference:\n %q\n %q", f1, f2)
	}
	if strings.ContainsAny(f1, "$") {
		t.Errorf("fingerprint kept a numbered placeholder: %q", f1)
	}
	if !strings.Contains(f1, "?") {
		t.Errorf("fingerprint has no ? placeholder: %q", f1)
	}
}

// TestFingerprintCollapsesRawPlaceholders covers the raw-text path that the
// builder's sql.Val tests miss. The postgres frontend lowers $N placeholders to
// sql.Raw("$N") fragments, which are re-emitted verbatim and never flow through
// Placeholder. Two queries differing only in placeholder numbering must still
// share a fingerprint, with no numbered placeholder surviving.
func TestFingerprintCollapsesRawPlaceholders(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	// sql.Raw("$1") mimics what the postgres frontend produces: a raw passthrough
	// fragment, not a bind value that would route through Placeholder().
	q1 := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Raw("$1")))
	q2 := sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Raw("$2")))

	f1, ok1 := analysis.Fingerprint(q1, sql.PostgreSQLDialect{})
	f2, ok2 := analysis.Fingerprint(q2, sql.PostgreSQLDialect{})
	if !ok1 || !ok2 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok1, ok2)
	}
	if f1 != f2 {
		t.Errorf("fingerprints differ for $1 vs $2:\n %q\n %q", f1, f2)
	}
	if strings.ContainsAny(f1, "$") {
		t.Errorf("fingerprint kept a numbered placeholder: %q", f1)
	}
	if !strings.Contains(f1, "?") {
		t.Errorf("fingerprint has no ? placeholder: %q", f1)
	}
}

// TestFingerprintCollapsesLimitOffset checks that LIMIT/OFFSET counts, which the
// compiler emits as plain integer literals (not placeholders), are normalized so
// that paginated reads differing only in page size or offset share a fingerprint.
func TestFingerprintCollapsesLimitOffset(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q1 := sql.Select(sql.Col("id")).From(users).Limit(10).Offset(0)
	q2 := sql.Select(sql.Col("id")).From(users).Limit(20).Offset(100)

	f1, ok1 := analysis.Fingerprint(q1, sql.PostgreSQLDialect{})
	f2, ok2 := analysis.Fingerprint(q2, sql.PostgreSQLDialect{})
	if !ok1 || !ok2 {
		t.Fatalf("Fingerprint ok = %v, %v, want both true", ok1, ok2)
	}
	if f1 != f2 {
		t.Errorf("fingerprints differ for LIMIT/OFFSET-only difference:\n %q\n %q", f1, f2)
	}
	if !strings.Contains(f1, "LIMIT ?") || !strings.Contains(f1, "OFFSET ?") {
		t.Errorf("LIMIT/OFFSET operands not collapsed: %q", f1)
	}

	// LIMIT alone and OFFSET alone must each collapse too.
	lim10, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Limit(10), sql.PostgreSQLDialect{})
	lim20, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Limit(20), sql.PostgreSQLDialect{})
	if lim10 != lim20 {
		t.Errorf("LIMIT 10 vs 20 differ:\n %q\n %q", lim10, lim20)
	}
	off0, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Offset(0), sql.PostgreSQLDialect{})
	off100, _ := analysis.Fingerprint(sql.Select(sql.Col("id")).From(users).Offset(100), sql.PostgreSQLDialect{})
	if off0 != off100 {
		t.Errorf("OFFSET 0 vs 100 differ:\n %q\n %q", off0, off100)
	}
}

// TestFingerprintNoNewCollisions guards against over-collapse: structurally
// different queries must keep distinct fingerprints even after the $N and
// LIMIT/OFFSET normalization passes. Clauses that change the shape of the query
// (presence vs absence of LIMIT/OFFSET, IN vs NOT IN, UNION vs UNION ALL, a
// different column/table/operator) must still differ.
func TestFingerprintNoNewCollisions(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	posts := sql.Table{Name: sql.NewIdent("posts")}
	base := func() sql.SelectBuilder { return sql.Select(sql.Col("id")).From(users) }

	cases := map[string]sql.Query{
		"in":            base().Where(sql.In(sql.Col("id"), 1, 2, 3)),
		"not_in":        base().Where(sql.NotIn(sql.Col("id"), 1, 2, 3)),
		"eq":            base().Where(sql.Eq(sql.Col("id"), 1)),
		"ne":            base().Where(sql.Ne(sql.Col("id"), 1)),
		"gt":            base().Where(sql.Gt(sql.Col("id"), 1)),
		"other_col":     base().Where(sql.Eq(sql.Col("name"), "x")),
		"other_table":   sql.Select(sql.Col("id")).From(posts).Where(sql.Eq(sql.Col("id"), 1)),
		"union":         base().Union(sql.Select(sql.Col("id")).From(posts)),
		"union_all":     base().UnionAll(sql.Select(sql.Col("id")).From(posts)),
		"eq_with_limit": base().Where(sql.Eq(sql.Col("id"), 1)).Limit(10),
		"limit_only":    base().Limit(10),
		"offset_only":   base().Offset(10),
		// Note: a raw $N placeholder is intentionally NOT listed here — it
		// canonicalizes to the same "?" as the "eq" bind value, which is the
		// desired collapse (see TestFingerprintCollapsesRawPlaceholders), not a
		// spurious collision.
	}

	seen := make(map[string]string, len(cases))
	for name, q := range cases {
		f, ok := analysis.Fingerprint(q, sql.PostgreSQLDialect{})
		if !ok {
			t.Fatalf("Fingerprint(%s) ok = false", name)
		}
		if prev, dup := seen[f]; dup {
			t.Errorf("collision: %q and %q both fingerprint to %q", prev, name, f)
		}
		seen[f] = name
	}
}

func TestFingerprintNil(t *testing.T) {
	if _, ok := analysis.Fingerprint(nil, sql.PostgreSQLDialect{}); ok {
		t.Error("Fingerprint(nil) ok = true, want false")
	}
}

func TestFingerprintRaw(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "string and number literals",
			in:   "SELECT * FROM users WHERE id = 123 AND name = 'ryo'",
			want: "SELECT * FROM users WHERE id = ? AND name = ?",
		},
		{
			name: "postgres placeholders",
			in:   "SELECT * FROM users WHERE id = $1 AND name = $2",
			want: "SELECT * FROM users WHERE id = ? AND name = ?",
		},
		{
			name: "question placeholders",
			in:   "SELECT * FROM users WHERE id = ? AND name = ?",
			want: "SELECT * FROM users WHERE id = ? AND name = ?",
		},
		{
			name: "collapses whitespace",
			in:   "SELECT   *\n  FROM users\t WHERE id = 1",
			want: "SELECT * FROM users WHERE id = ?",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := analysis.FingerprintRaw(tt.in); got != tt.want {
				t.Errorf("FingerprintRaw(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectKind(t *testing.T) {
	tests := []struct {
		in   string
		want analysis.StmtKind
	}{
		{"SELECT 1", analysis.StmtSelect},
		{"  select * from t", analysis.StmtSelect},
		{"WITH x AS (SELECT 1) SELECT * FROM x", analysis.StmtSelect},
		{"INSERT INTO t VALUES (1)", analysis.StmtInsert},
		{"UPDATE t SET x = 1", analysis.StmtUpdate},
		{"DELETE FROM t", analysis.StmtDelete},
		{"CREATE TABLE t (id int)", analysis.StmtDDL},
		{"-- a comment\nSELECT 1", analysis.StmtSelect},
		{"/* c */ DELETE FROM t", analysis.StmtDelete},
		{"EXPLAIN SELECT 1", analysis.StmtUnknown},
	}
	for _, tt := range tests {
		if got := analysis.DetectKind(tt.in); got != tt.want {
			t.Errorf("DetectKind(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
