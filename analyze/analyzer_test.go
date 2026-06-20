package analyze_test

import (
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

func TestAnalyzerDelegates(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q := sql.Select(sql.Star()).From(users).Where(sql.In(sql.Col("tenant_id"), 1, 2, 3))
	a := analyze.New()

	if kind, ok := a.Kind(q); !ok || kind != analyze.StmtSelect {
		t.Errorf("Kind = (%v, %v), want (select, true)", kind, ok)
	}
	if !a.HasWhere(q) || a.HasJoin(q) || !a.SelectsStar(q) {
		t.Errorf("clauses: HasWhere=%v HasJoin=%v SelectsStar=%v", a.HasWhere(q), a.HasJoin(q), a.SelectsStar(q))
	}
	cols, maxIn := a.WherePredicates(q)
	if len(cols) != 1 || cols[0] != "tenant_id" || maxIn != 3 {
		t.Errorf("WherePredicates = (%v, %d), want ([tenant_id], 3)", cols, maxIn)
	}
}

func TestAnalyzerFingerprintNeedsDialect(t *testing.T) {
	q := sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Eq(sql.Col("id"), sql.Val(1)))

	// Without a dialect, Fingerprint cannot recompile and reports ok=false.
	if _, ok := analyze.New().Fingerprint(q); ok {
		t.Error("Fingerprint ok = true without a dialect, want false")
	}

	a := analyze.New(analyze.WithDialect(sql.PostgreSQLDialect{}))
	fp, ok := a.Fingerprint(q)
	if !ok || fp == "" {
		t.Errorf("Fingerprint = (%q, %v), want a non-empty string and true", fp, ok)
	}
}
