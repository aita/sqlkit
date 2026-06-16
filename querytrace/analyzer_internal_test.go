package querytrace

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestTopLevelFilterColumns(t *testing.T) {
	users := sql.Table{Name: "users"}
	tests := []struct {
		name  string
		query sql.Query
		want  []string
	}{
		{
			name:  "single equality",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("tenant_id"), sql.Expr(1))),
			want:  []string{"tenant_id"},
		},
		{
			name: "and of predicates, qualified names folded",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.And(
				sql.Eq(sql.Col("users.tenant_id"), sql.Expr(1)),
				sql.Eq(sql.Col("DELETED_AT"), sql.Expr(nil)),
			)),
			want: []string{"tenant_id", "deleted_at"},
		},
		{
			name:  "in list",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.In(sql.Col("status"), 1, 2, 3)),
			want:  []string{"status"},
		},
		{
			name:  "no where",
			query: sql.Select(sql.Col("id")).From(users),
			want:  nil,
		},
		{
			name:  "update where",
			query: sql.Update(users).Set(sql.Set("name", sql.Expr("x"))).Where(sql.Eq(sql.Col("org_id"), sql.Expr(7))),
			want:  []string{"org_id"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := topLevelFilterColumns(tc.query)
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("filter columns = %v, want %v", got, want)
			}
		})
	}
}

func TestMaxInListLen(t *testing.T) {
	users := sql.Table{Name: "users"}
	sub := sql.Select(sql.Col("id")).From(sql.Table{Name: "banned"})
	tests := []struct {
		name  string
		query sql.Query
		want  int
	}{
		{
			name:  "in of three",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.In(sql.Col("id"), 1, 2, 3)),
			want:  3,
		},
		{
			name: "largest of several",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.And(
				sql.In(sql.Col("a"), 1, 2),
				sql.In(sql.Col("b"), 1, 2, 3, 4, 5),
			)),
			want: 5,
		},
		{
			name:  "subquery in does not count",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.InQuery(sql.Col("id"), sub)),
			want:  0,
		},
		{
			name:  "no in",
			query: sql.Select(sql.Col("id")).From(users).Where(sql.Eq(sql.Col("id"), sql.Expr(1))),
			want:  0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := maxInListLen(tc.query); got != tc.want {
				t.Errorf("maxInListLen = %d, want %d", got, tc.want)
			}
		})
	}
}

// recordAnalyzed records a single analyzed statement event and returns its
// trace, for exercising the derived analyzer warnings.
func recordAnalyzed(t *testing.T, cfg Config, q QueryInfo) *Trace {
	t.Helper()
	q.analyzed = true
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

	over := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect, Tables: []string{"users"}, MaxInList: 100})
	if !hasCode(over, WarnLargeInList) {
		t.Error("IN list at budget should warn")
	}

	under := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect, Tables: []string{"users"}, MaxInList: 99})
	if hasCode(under, WarnLargeInList) {
		t.Error("IN list below budget should not warn")
	}

	off := recordAnalyzed(t, NewConfig(), QueryInfo{Operation: StmtSelect, MaxInList: 5000})
	if hasCode(off, WarnLargeInList) {
		t.Error("disabled check should not warn")
	}
}

func TestSelectStarWarning(t *testing.T) {
	cfg := NewConfig(WithWarnings(WarningConfig{SelectStar: true}))

	star := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect, Tables: []string{"users"}, SelectStar: true})
	if !hasCode(star, WarnSelectStar) {
		t.Error("SELECT * should warn when enabled")
	}

	explicit := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect, Tables: []string{"users"}, SelectStar: false})
	if hasCode(explicit, WarnSelectStar) {
		t.Error("explicit projection should not warn")
	}

	off := recordAnalyzed(t, NewConfig(), QueryInfo{Operation: StmtSelect, SelectStar: true})
	if hasCode(off, WarnSelectStar) {
		t.Error("disabled check should not warn")
	}
}

func TestTenantPredicateWarning(t *testing.T) {
	cfg := NewConfig(WithWarnings(WarningConfig{RequireTenantColumns: []string{"tenant_id"}}))

	missing := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect, Tables: []string{"users"}, filterColumns: []string{"id"}})
	if !hasCode(missing, WarnMissingTenantPredicate) {
		t.Error("select without tenant predicate should warn")
	}

	present := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtUpdate, Tables: []string{"users"}, filterColumns: []string{"tenant_id", "id"}})
	if hasCode(present, WarnMissingTenantPredicate) {
		t.Error("statement with tenant predicate should not warn")
	}

	// An INSERT carries its tenant value in the row, not a WHERE: out of scope.
	ins := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtInsert, Tables: []string{"users"}})
	if hasCode(ins, WarnMissingTenantPredicate) {
		t.Error("insert should be out of scope for the tenant check")
	}

	// No base table (or unanalyzed) means nothing to scope.
	noTable := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect})
	if hasCode(noTable, WarnMissingTenantPredicate) {
		t.Error("statement without a base table should not warn")
	}
}

func TestSoftDeletePredicateWarning(t *testing.T) {
	cfg := NewConfig(WithWarnings(WarningConfig{SoftDeleteColumns: []string{"deleted_at"}}))

	missing := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect, Tables: []string{"users"}, filterColumns: []string{"id"}})
	if !hasCode(missing, WarnMissingSoftDeletePredicate) {
		t.Error("select without soft-delete predicate should warn")
	}

	present := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtSelect, Tables: []string{"users"}, filterColumns: []string{"deleted_at"}})
	if hasCode(present, WarnMissingSoftDeletePredicate) {
		t.Error("select with soft-delete predicate should not warn")
	}

	// The soft-delete check is for reads; a DELETE is not subject to it.
	del := recordAnalyzed(t, cfg, QueryInfo{Operation: StmtDelete, Tables: []string{"users"}, filterColumns: []string{"id"}})
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
	if !reflect.DeepEqual(qi.filterColumns, []string{"tenant_id"}) {
		t.Errorf("filterColumns = %v, want [tenant_id]", qi.filterColumns)
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
