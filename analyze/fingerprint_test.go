package analyze_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/analyze"
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

	f1, ok1 := analyze.Fingerprint(q1, sql.PostgreSQLDialect{})
	f2, ok2 := analyze.Fingerprint(q2, sql.PostgreSQLDialect{})
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

func TestFingerprintNil(t *testing.T) {
	if _, ok := analyze.Fingerprint(nil, sql.PostgreSQLDialect{}); ok {
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
			if got := analyze.FingerprintRaw(tt.in); got != tt.want {
				t.Errorf("FingerprintRaw(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectKind(t *testing.T) {
	tests := []struct {
		in   string
		want analyze.StmtKind
	}{
		{"SELECT 1", analyze.StmtSelect},
		{"  select * from t", analyze.StmtSelect},
		{"WITH x AS (SELECT 1) SELECT * FROM x", analyze.StmtSelect},
		{"INSERT INTO t VALUES (1)", analyze.StmtInsert},
		{"UPDATE t SET x = 1", analyze.StmtUpdate},
		{"DELETE FROM t", analyze.StmtDelete},
		{"CREATE TABLE t (id int)", analyze.StmtDDL},
		{"-- a comment\nSELECT 1", analyze.StmtSelect},
		{"/* c */ DELETE FROM t", analyze.StmtDelete},
		{"EXPLAIN SELECT 1", analyze.StmtUnknown},
	}
	for _, tt := range tests {
		if got := analyze.DetectKind(tt.in); got != tt.want {
			t.Errorf("DetectKind(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
