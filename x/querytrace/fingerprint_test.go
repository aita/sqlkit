package querytrace

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
)

// TestFingerprintParsedUnifiesPlaceholders checks the parsed path: a recompiled
// query renders every bind as "?", so queries differing only in their values (or
// placeholder numbering) share a fingerprint with no numbered placeholder left.
func TestFingerprintParsedUnifiesPlaceholders(t *testing.T) {
	var cfg Config // dialect defaults to PostgreSQL
	users := sql.Table{Name: "users"}
	q1 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(1)), sql.Eq(sql.Col("name"), sql.Val("a"))))
	q2 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(999)), sql.Eq(sql.Col("name"), sql.Val("z"))))

	f1 := cfg.fingerprint("", q1)
	f2 := cfg.fingerprint("", q2)
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
			if got := fingerprintRaw(tt.in); got != tt.want {
				t.Errorf("fingerprintRaw(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectOperation(t *testing.T) {
	tests := []struct {
		in   string
		want StmtKind
	}{
		{"SELECT 1", StmtSelect},
		{"  select * from t", StmtSelect},
		{"WITH x AS (SELECT 1) SELECT * FROM x", StmtSelect},
		{"INSERT INTO t VALUES (1)", StmtInsert},
		{"UPDATE t SET x = 1", StmtUpdate},
		{"DELETE FROM t", StmtDelete},
		{"CREATE TABLE t (id int)", StmtDDL},
		{"-- a comment\nSELECT 1", StmtSelect},
		{"/* c */ DELETE FROM t", StmtDelete},
		{"EXPLAIN SELECT 1", StmtUnknown},
	}
	for _, tt := range tests {
		if got := detectOperation(tt.in); got != tt.want {
			t.Errorf("detectOperation(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestAnalyzeFallbackWithoutParser(t *testing.T) {
	cfg := Config{Enabled: true, Fingerprint: true, AnalyzeSQL: true}
	qi := cfg.Analyze("UPDATE users SET name = 'x'", nil)

	if qi.Operation != StmtUpdate {
		t.Errorf("Operation = %v, want update", qi.Operation)
	}
	// Without a parser the WHERE presence is unknown, so analyzed stays false
	// and the missing-WHERE check must not fire on it.
	if qi.analyzed {
		t.Errorf("analyzed = true, want false on the fallback path")
	}
	if qi.Fingerprint != "UPDATE users SET name = ?" {
		t.Errorf("Fingerprint = %q", qi.Fingerprint)
	}
}

func TestArgTypeNames(t *testing.T) {
	got := argTypeNames([]any{int64(1), "x", nil})
	want := []string{"int64", "string", "nil"}
	if len(got) != len(want) {
		t.Fatalf("argTypeNames len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argTypeNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
