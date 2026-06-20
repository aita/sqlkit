package querytrace

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

// TestFingerprintParsedUnifiesPlaceholders checks the parsed path: a recompiled
// query renders every bind as "?", so queries differing only in their values (or
// placeholder numbering) share a fingerprint with no numbered placeholder left.
func TestFingerprintParsedUnifiesPlaceholders(t *testing.T) {
	var cfg Config // dialect defaults to PostgreSQL
	users := sql.Table{Name: sql.NewIdent("users")}
	q1 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(1)), sql.Eq(sql.Col("name"), sql.Val("a"))))
	q2 := sql.Select(sql.Col("id")).From(users).
		Where(sql.And(sql.Eq(sql.Col("id"), sql.Val(999)), sql.Eq(sql.Col("name"), sql.Val("z"))))

	a := analyze.New(analyze.WithDialect(cfg.dialect()))
	f1 := fingerprint(a, "", q1)
	f2 := fingerprint(a, "", q2)
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

func TestAnalyzeFallbackWithoutParser(t *testing.T) {
	cfg := Config{Enabled: true, Fingerprint: true, AnalyzeSQL: true}
	qi := cfg.Analyze("UPDATE users SET name = 'x'", nil)

	if qi.Kind != StmtUpdate {
		t.Errorf("Operation = %v, want update", qi.Kind)
	}
	// Without a parser the WHERE presence is unknown, so analyzed stays false
	// and the missing-WHERE check must not fire on it.
	if qi.Parsed {
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
