package analyze_test

import (
	"errors"
	"reflect"
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
	if !a.HasTopLevelWhere(q) || a.HasTopLevelJoin(q) || !a.SelectsStar(q) {
		t.Errorf("clauses: HasTopLevelWhere=%v HasTopLevelJoin=%v SelectsStar=%v", a.HasTopLevelWhere(q), a.HasTopLevelJoin(q), a.SelectsStar(q))
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

func TestStmtKindString(t *testing.T) {
	tests := []struct {
		kind analyze.StmtKind
		want string
	}{
		{analyze.StmtSelect, "select"},
		{analyze.StmtInsert, "insert"},
		{analyze.StmtUpdate, "update"},
		{analyze.StmtDelete, "delete"},
		{analyze.StmtDDL, "ddl"},
		{analyze.StmtUnknown, "unknown"},
		{analyze.StmtKind(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("StmtKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestStmtKindFiltersRows(t *testing.T) {
	tests := []struct {
		kind analyze.StmtKind
		want bool
	}{
		{analyze.StmtSelect, true},
		{analyze.StmtUpdate, true},
		{analyze.StmtDelete, true},
		{analyze.StmtInsert, false},
		{analyze.StmtDDL, false},
		{analyze.StmtUnknown, false},
	}
	for _, tt := range tests {
		if got := tt.kind.FiltersRows(); got != tt.want {
			t.Errorf("%s.FiltersRows() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestAnalyzerAnalyzeParsed(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	orders := sql.Table{Name: sql.NewIdent("orders")}
	q := sql.Select(sql.Star()).
		From(sql.SourceAlias(users, "u")).
		Join(sql.SourceAlias(orders, "o"), sql.Eq(sql.Col("u.id"), sql.Col("o.user_id"))).
		Where(sql.And(sql.In(sql.Col("tenant_id"), 1, 2, 3), sql.Eq(sql.Col("deleted_at"), sql.Val(nil))))

	got := analyze.New().Analyze("SELECT ...", q)

	if !got.Parsed {
		t.Fatal("Parsed = false, want true")
	}
	if got.Kind != analyze.StmtSelect {
		t.Errorf("Kind = %v, want select", got.Kind)
	}
	if !reflect.DeepEqual(got.Tables, []string{"users", "orders"}) {
		t.Errorf("Tables = %v, want [users orders]", got.Tables)
	}
	if !got.HasTopLevelWhere || !got.HasTopLevelJoin || !got.SelectStar {
		t.Errorf("clauses: where=%v join=%v star=%v", got.HasTopLevelWhere, got.HasTopLevelJoin, got.SelectStar)
	}
	if !reflect.DeepEqual(got.FilterColumns, []string{"tenant_id", "deleted_at"}) {
		t.Errorf("FilterColumns = %v, want [tenant_id deleted_at]", got.FilterColumns)
	}
	if got.MaxInList != 3 {
		t.Errorf("MaxInList = %d, want 3", got.MaxInList)
	}
	if !got.NullEquality {
		t.Error("NullEquality = false, want true")
	}
	// Analyze never sets Fingerprint or Err — the caller fills those.
	if got.Fingerprint != "" || got.Err != nil {
		t.Errorf("Analyze set Fingerprint=%q Err=%v, want both empty", got.Fingerprint, got.Err)
	}
}

func TestAnalyzerAnalyzeFallback(t *testing.T) {
	// q is nil (a parse failure), so Analyze falls back to a keyword guess on the
	// SQL text: only Kind is set, from DetectKind, and Parsed is false.
	got := analyze.New().Analyze("UPDATE users SET x = 1", nil)
	if got.Parsed {
		t.Error("Parsed = true, want false on the fallback path")
	}
	if got.Kind != analyze.StmtUpdate {
		t.Errorf("Kind = %v, want update", got.Kind)
	}
	if got.Tables != nil || got.HasTopLevelWhere || got.HasTopLevelJoin || got.SelectStar {
		t.Errorf("fallback populated structural fields: %+v", got)
	}
}

func TestAnalyzerTables(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	q := sql.Select(sql.Col("id")).From(users)
	if got := analyze.New().Tables(q); !reflect.DeepEqual(got, []string{"users"}) {
		t.Errorf("Analyzer.Tables = %v, want [users]", got)
	}
}

// TestAnalysisErrField pins that an Err set by the producer survives — Analyze
// itself leaves it nil, but the struct carries it for the caller.
func TestAnalysisErrField(t *testing.T) {
	a := analyze.Analysis{Err: errors.New("boom")}
	if a.Err == nil {
		t.Error("Analysis.Err = nil, want the producer's error")
	}
}
