package querytrace

import (
	"context"
	"reflect"
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/meta"
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
	cfg := NewConfig(WithMaxInList(100))

	over := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, MaxInList: 100}})
	if !hasCode(over, WarnLargeInList) {
		t.Error("IN list at budget should warn")
	}

	under := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, MaxInList: 99}})
	if hasCode(under, WarnLargeInList) {
		t.Error("IN list below budget should not warn")
	}

	off := recordAnalyzed(t, NewConfig(), QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, MaxInList: 5000}})
	if hasCode(off, WarnLargeInList) {
		t.Error("disabled check should not warn")
	}
}

func TestSelectStarWarning(t *testing.T) {
	cfg := NewConfig(WithSelectStarWarning())

	star := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, SelectStar: true}})
	if !hasCode(star, WarnSelectStar) {
		t.Error("SELECT * should warn when enabled")
	}

	explicit := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, SelectStar: false}})
	if hasCode(explicit, WarnSelectStar) {
		t.Error("explicit projection should not warn")
	}

	off := recordAnalyzed(t, NewConfig(), QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, SelectStar: true}})
	if hasCode(off, WarnSelectStar) {
		t.Error("disabled check should not warn")
	}
}

func TestTenantPredicateWarning(t *testing.T) {
	cfg := NewConfig(WithTenantCheck(Columns("tenant_id")))

	missing := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if !hasCode(missing, WarnMissingTenantPredicate) {
		t.Error("select without tenant predicate should warn")
	}

	present := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtUpdate, Tables: []string{"users"}, FilterColumns: []string{"tenant_id", "id"}}})
	if hasCode(present, WarnMissingTenantPredicate) {
		t.Error("statement with tenant predicate should not warn")
	}

	// An INSERT carries its tenant value in the row, not a WHERE: out of scope.
	ins := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtInsert, Tables: []string{"users"}}})
	if hasCode(ins, WarnMissingTenantPredicate) {
		t.Error("insert should be out of scope for the tenant check")
	}

	// No base table (or unanalyzed) means nothing to scope.
	noTable := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect}})
	if hasCode(noTable, WarnMissingTenantPredicate) {
		t.Error("statement without a base table should not warn")
	}
}

func TestSoftDeletePredicateWarning(t *testing.T) {
	cfg := NewConfig(WithSoftDeleteCheck(Columns("deleted_at")))

	missing := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if !hasCode(missing, WarnMissingSoftDeletePredicate) {
		t.Error("select without soft-delete predicate should warn")
	}

	present := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"deleted_at"}}})
	if hasCode(present, WarnMissingSoftDeletePredicate) {
		t.Error("select with soft-delete predicate should not warn")
	}

	// The soft-delete check is for reads; a DELETE is not subject to it.
	del := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtDelete, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if hasCode(del, WarnMissingSoftDeletePredicate) {
		t.Error("delete should be out of scope for the soft-delete check")
	}
}

// countCode returns how many warnings on the trace carry code.
func countCode(trace *Trace, code WarningCode) int {
	n := 0
	for _, w := range trace.Warnings() {
		if w.Code == code {
			n++
		}
	}
	return n
}

// metaOf builds a meta.Metadata from a table→columns map, for the schema-scoping
// tests.
func metaOf(tableColumns map[string][]string) meta.Metadata {
	var md meta.Metadata
	for table, cols := range tableColumns {
		columns := make([]meta.Column, len(cols))
		for i, c := range cols {
			columns[i] = meta.Column{Name: c}
		}
		md.Tables = append(md.Tables, meta.Table{Name: table, Columns: columns})
	}
	return md
}

// TestSchemaScopesPredicateChecks checks ScopedToSchema limits the soft-delete and
// tenant checks to the tables that actually have a marker column.
func TestSchemaScopesPredicateChecks(t *testing.T) {
	md := metaOf(map[string][]string{
		"users": {"id", "tenant_id", "deleted_at"},
		// countries is a reference table with no soft-delete or tenant column.
		"countries": {"code", "name"},
	})
	cfg := NewConfig(
		WithTenantCheck(Columns("tenant_id"), ScopedToSchema(md)),
		WithSoftDeleteCheck(Columns("deleted_at"), ScopedToSchema(md)),
	)

	// users has both marker columns, and this SELECT references neither: it warns.
	users := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if !hasCode(users, WarnMissingSoftDeletePredicate) || !hasCode(users, WarnMissingTenantPredicate) {
		t.Error("users lacks both predicates and should warn for both")
	}

	// countries has neither marker column, so the checks do not apply to it.
	countries := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"countries"}, FilterColumns: []string{"code"}}})
	if hasCode(countries, WarnMissingSoftDeletePredicate) || hasCode(countries, WarnMissingTenantPredicate) {
		t.Error("a reference table without marker columns must be skipped")
	}

	// A table the schema does not cover at all is left alone too.
	unknown := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"events"}, FilterColumns: []string{"id"}}})
	if hasCode(unknown, WarnMissingSoftDeletePredicate) || hasCode(unknown, WarnMissingTenantPredicate) {
		t.Error("a table absent from the schema must be skipped")
	}
}

// TestSchemaScopeQualifiedAndCase checks table and column matching folds case and
// strips a schema qualifier.
func TestSchemaScopeQualifiedAndCase(t *testing.T) {
	md := metaOf(map[string][]string{"Users": {"Deleted_At"}})
	cfg := NewConfig(
		WithSoftDeleteCheck(Columns("deleted_at"), ScopedToSchema(md)),
	)

	missing := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"public.users"}, FilterColumns: []string{"id"}}})
	if !hasCode(missing, WarnMissingSoftDeletePredicate) {
		t.Error("qualified table with the marker column should warn when unfiltered")
	}
	present := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"public.users"}, FilterColumns: []string{"deleted_at"}}})
	if hasCode(present, WarnMissingSoftDeletePredicate) {
		t.Error("filtering on the marker column should clear the warning")
	}
}

// TestPredicateWarningPerTable checks a multi-table statement warns once per
// in-scope table that misses the predicate, naming each in the details.
func TestPredicateWarningPerTable(t *testing.T) {
	md := metaOf(map[string][]string{
		"users":  {"deleted_at"},
		"orders": {"deleted_at"},
	})
	cfg := NewConfig(
		WithSoftDeleteCheck(Columns("deleted_at"), ScopedToSchema(md)),
	)

	trace := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users", "orders"}, FilterColumns: []string{"id"}}})
	if got := countCode(trace, WarnMissingSoftDeletePredicate); got != 2 {
		t.Errorf("missing predicate on two tables = %d warnings, want 2", got)
	}
}

// TestPredicateColumnGlob checks a glob column spec matches a family of columns.
func TestPredicateColumnGlob(t *testing.T) {
	cfg := NewConfig(WithSoftDeleteCheck(Columns("*_at")))

	missing := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if !hasCode(missing, WarnMissingSoftDeletePredicate) {
		t.Error("no *_at column in WHERE should warn")
	}
	// deactivated_at matches the *_at glob, so the predicate is satisfied.
	present := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"deactivated_at"}}})
	if hasCode(present, WarnMissingSoftDeletePredicate) {
		t.Error("deactivated_at matches *_at and should clear the warning")
	}
}

// TestPredicateMultipleMarkers checks any of several marker columns satisfies the
// check.
func TestPredicateMultipleMarkers(t *testing.T) {
	cfg := NewConfig(WithSoftDeleteCheck(Columns("deleted_at", "deactivated_at")))

	ok := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"deactivated_at"}}})
	if hasCode(ok, WarnMissingSoftDeletePredicate) {
		t.Error("one of the markers present should clear the warning")
	}
}

// TestPredicateTableQualifiedSpec checks a "table.column" spec governs only the
// named table and leaves others unchecked.
func TestPredicateTableQualifiedSpec(t *testing.T) {
	cfg := NewConfig(WithSoftDeleteCheck(Columns("orders.deleted_at")))

	// orders is governed and misses the predicate: it warns.
	orders := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"orders"}, FilterColumns: []string{"id"}}})
	if !hasCode(orders, WarnMissingSoftDeletePredicate) {
		t.Error("orders is governed by the spec and should warn")
	}
	// users is not named by the spec, so it is not checked.
	users := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if hasCode(users, WarnMissingSoftDeletePredicate) {
		t.Error("users is not named by the table-qualified spec and should be skipped")
	}
	// A schema-qualified table name still resolves to the bare table.
	qualified := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"public.orders"}, FilterColumns: []string{"id"}}})
	if !hasCode(qualified, WarnMissingSoftDeletePredicate) {
		t.Error("public.orders should match the orders spec")
	}
}

// TestPredicateTableGlob checks a table glob governs a family of tables.
func TestPredicateTableGlob(t *testing.T) {
	cfg := NewConfig(WithTenantCheck(Columns("tenant_*.tenant_id")))

	governed := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"tenant_orders"}, FilterColumns: []string{"id"}}})
	if !hasCode(governed, WarnMissingTenantPredicate) {
		t.Error("tenant_orders matches tenant_* and should warn")
	}
	other := recordAnalyzed(t, cfg, QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, Tables: []string{"users"}, FilterColumns: []string{"id"}}})
	if hasCode(other, WarnMissingTenantPredicate) {
		t.Error("users does not match tenant_* and should be skipped")
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
	q := sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("users")}).
		Where(sql.In(sql.Col("tenant_id"), 1, 2, 3))
	return []sql.Query{q}, nil
}

// TestEqNullWarning covers the always-on correctness check: a = NULL / <> NULL
// predicate raises eq_null whenever analysis saw it, with no policy to enable.
func TestEqNullWarning(t *testing.T) {
	warned := recordAnalyzed(t, NewConfig(), QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, NullEquality: true}})
	if !hasCode(warned, WarnEqNull) {
		t.Error("= NULL comparison should warn")
	}

	clean := recordAnalyzed(t, NewConfig(), QueryInfo{Analysis: analysis.Analysis{Kind: StmtSelect, NullEquality: false}})
	if hasCode(clean, WarnEqNull) {
		t.Error("clean predicate should not warn")
	}
}

// TestAnalyzeWiresNullEquality checks the parse → analyze path sets NullEquality
// from a parsed = NULL predicate.
func TestAnalyzeWiresNullEquality(t *testing.T) {
	cfg := NewConfig(WithParser(eqNullTestParser{}))
	if qi := cfg.Analyze("anything", nil); !qi.NullEquality {
		t.Error("NullEquality not set from the parsed = NULL predicate")
	}
}

// eqNullTestParser returns a fixed SELECT ... WHERE deleted_at = NULL AST so the
// wiring of NullEquality can be tested without a real parser.
type eqNullTestParser struct{}

func (eqNullTestParser) Parse(string) ([]sql.Query, error) {
	q := sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
		Where(sql.Eq(sql.Col("deleted_at"), sql.Expr(nil)))
	return []sql.Query{q}, nil
}
