package querytrace

import (
	"context"
	"reflect"
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

// recordAnalyzed records a single analyzed statement event and returns its
// trace, for exercising the derived analyzer warnings.
func recordAnalyzed(t *testing.T, cfg Config, q QueryInfo) *Trace {
	t.Helper()
	q.Parsed = true
	trace := New("op", WithConfig(cfg))
	trace.RecordStatement(context.Background(), Event{Kind: EventQuery, Query: &q})
	return trace
}

func hasCode(trace *Trace, code WarningCode) bool {
	for _, w := range trace.Warnings() {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestLargeInListWarning(t *testing.T) {
	cfg := NewConfig(WithWarnings(WarningConfig{MaxInListLen: 100}))

	over := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, Tables: []string{"users"}, MaxInList: 100}})
	if !hasCode(over, WarnLargeInList) {
		t.Error("IN list at budget should warn")
	}

	under := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, Tables: []string{"users"}, MaxInList: 99}})
	if hasCode(under, WarnLargeInList) {
		t.Error("IN list below budget should not warn")
	}

	off := recordAnalyzed(t, NewConfig(), QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, MaxInList: 5000}})
	if hasCode(off, WarnLargeInList) {
		t.Error("disabled check should not warn")
	}
}

func TestSelectStarWarning(t *testing.T) {
	cfg := NewConfig(WithWarnings(WarningConfig{SelectStar: true}))

	star := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, Tables: []string{"users"}, SelectStar: true}})
	if !hasCode(star, WarnSelectStar) {
		t.Error("SELECT * should warn when enabled")
	}

	explicit := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, Tables: []string{"users"}, SelectStar: false}})
	if hasCode(explicit, WarnSelectStar) {
		t.Error("explicit projection should not warn")
	}

	off := recordAnalyzed(t, NewConfig(), QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, SelectStar: true}})
	if hasCode(off, WarnSelectStar) {
		t.Error("disabled check should not warn")
	}
}

func TestTenantPredicateWarning(t *testing.T) {
	cfg := NewConfig(WithWarnings(WarningConfig{RequireTenantColumns: []string{"tenant_id"}}))

	missing := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if !hasCode(missing, WarnMissingTenantPredicate) {
		t.Error("select without tenant predicate should warn")
	}

	present := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtUpdate, Tables: []string{"users"}, FilterColumns: []string{"tenant_id", "id"}}})
	if hasCode(present, WarnMissingTenantPredicate) {
		t.Error("statement with tenant predicate should not warn")
	}

	// An INSERT carries its tenant value in the row, not a WHERE: out of scope.
	ins := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtInsert, Tables: []string{"users"}}})
	if hasCode(ins, WarnMissingTenantPredicate) {
		t.Error("insert should be out of scope for the tenant check")
	}

	// No base table (or unanalyzed) means nothing to scope.
	noTable := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect}})
	if hasCode(noTable, WarnMissingTenantPredicate) {
		t.Error("statement without a base table should not warn")
	}
}

func TestSoftDeletePredicateWarning(t *testing.T) {
	cfg := NewConfig(WithWarnings(WarningConfig{SoftDeleteColumns: []string{"deleted_at"}}))

	missing := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if !hasCode(missing, WarnMissingSoftDeletePredicate) {
		t.Error("select without soft-delete predicate should warn")
	}

	present := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"deleted_at"}}})
	if hasCode(present, WarnMissingSoftDeletePredicate) {
		t.Error("select with soft-delete predicate should not warn")
	}

	// The soft-delete check is for reads; a DELETE is not subject to it.
	del := recordAnalyzed(t, cfg, QueryInfo{Analysis: analyze.Analysis{Kind: StmtDelete, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if hasCode(del, WarnMissingSoftDeletePredicate) {
		t.Error("delete should be out of scope for the soft-delete check")
	}
}

// TestAnalyzeWiresAdvancedFacts checks the parse → analyze path populates the
// new structural facts the analyzer warnings read.
func TestAnalyzeWiresAdvancedFacts(t *testing.T) {
	cfg := NewConfig(WithParser(analyzerTestParser{}))
	qi := cfg.Analyze("SELECT star with tenant_id IN list", nil)
	if !qi.SelectStar {
		t.Error("SelectStar not set from the parsed star projection")
	}
	if qi.MaxInList != 3 {
		t.Errorf("MaxInList = %d, want 3", qi.MaxInList)
	}
	if !reflect.DeepEqual(qi.FilterColumns, []string{"tenant_id"}) {
		t.Errorf("filterColumns = %v, want [tenant_id]", qi.FilterColumns)
	}
}

// analyzerTestParser returns a fixed SELECT * ... WHERE tenant_id IN (1,2,3) AST,
// so Analyze's wiring of the advanced facts can be tested without a real parser.
type analyzerTestParser struct{}

func (analyzerTestParser) Parse(string) ([]sql.Query, error) {
	q := sql.Select(sql.Star()).From(sql.Table{Name: "users"}).
		Where(sql.In(sql.Col("tenant_id"), 1, 2, 3))
	return []sql.Query{q}, nil
}
