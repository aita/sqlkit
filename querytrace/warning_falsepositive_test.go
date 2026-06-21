package querytrace_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit/analysis"
	querytrace "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sqltest"
)

// This file is the negative corpus: a consolidated, table-driven set of correct,
// benign traces that must produce ZERO warnings (or, where another check is
// deliberately tripped, must not produce one specific code). It guards against
// false positives — the most damaging failure mode for an advisory linter, since
// a noisy detector trains its users to ignore it.

// fpQuery is a compact constructor for an analyzed query event used by the
// false-positive corpus. By default it produces a fully parsed analysis so the
// AST-gated checks (large IN, SELECT *, tenant, soft-delete) are in scope; an
// option can flip Parsed off to exercise the keyword-fallback path.
type fpQuery struct {
	fp       string
	kind     querytrace.StmtKind
	parsed   bool
	tables   []string
	cols     []string
	maxIn    int
	star     bool
	params   int
	caller   *querytrace.Caller
	rows     *querytrace.RowsInfo
	kindEv   querytrace.EventKind
	noFinger bool
}

func (q fpQuery) event() querytrace.Event {
	fp := q.fp
	if fp == "" && !q.noFinger {
		fp = "SELECT 1"
	}
	kindEv := q.kindEv
	if kindEv == "" {
		kindEv = querytrace.EventQuery
	}
	return querytrace.Event{
		Kind: kindEv,
		Query: &querytrace.QueryInfo{
			Analysis: analysis.Analysis{
				Kind:          q.kind,
				Fingerprint:   fp,
				Parsed:        q.parsed,
				Tables:        q.tables,
				FilterColumns: q.cols,
				MaxInList:     q.maxIn,
				SelectStar:    q.star,
			},
			ParamCount: q.params,
		},
		Caller: q.caller,
		Rows:   q.rows,
	}
}

// fpCase is one benign trace: a config, the events to record, and the codes that
// must NOT appear. An empty wantAbsent means "no warnings at all".
type fpCase struct {
	name       string
	cfg        querytrace.Config
	events     []querytrace.Event
	wantAbsent []querytrace.WarningCode
}

// analyzerCfg enables every AST-policy check at a tight threshold so a benign
// trace has the maximum chance to (wrongly) trip one. Used across the corpus.
func analyzerCfg() querytrace.Config {
	return querytrace.NewConfig(
		querytrace.WithMaxInList(3),
		querytrace.WithSelectStarWarning(),
		querytrace.WithTenantCheck(querytrace.Columns("tenant_id")),
		querytrace.WithSoftDeleteCheck(querytrace.Columns("deleted_at")),
	)
}

func TestNoFalsePositives(t *testing.T) {
	siteA := at("a.go", 1)
	siteB := at("b.go", 2)

	cases := []fpCase{
		{
			// A batched Preload-style access: one parent query plus a single
			// follow-up that loads all children in one IN-keyed statement. The
			// follow-up runs once, so its fingerprint never repeats and N+1 must
			// not fire.
			name: "batched_preload_is_not_n_plus_one",
			cfg:  nplusOneConfig(2),
			events: []querytrace.Event{
				fpQuery{fp: "SELECT * FROM posts", kind: querytrace.StmtSelect, caller: siteA}.event(),
				fpQuery{fp: "SELECT * FROM comments WHERE post_id IN (?)", kind: querytrace.StmtSelect, caller: siteB}.event(),
			},
			wantAbsent: []querytrace.WarningCode{querytrace.WarnPossibleNPlusOne, querytrace.WarnQueryInLoop},
		},
		{
			// A fingerprint repeated fewer times than the threshold (2 < 3).
			name: "below_n_plus_one_threshold",
			cfg:  nplusOneConfig(3),
			events: []querytrace.Event{
				fpQuery{fp: "SELECT * FROM users WHERE id = ?", kind: querytrace.StmtSelect, caller: siteA}.event(),
				fpQuery{fp: "SELECT * FROM users WHERE id = ?", kind: querytrace.StmtSelect, caller: siteB}.event(),
			},
			wantAbsent: []querytrace.WarningCode{querytrace.WarnPossibleNPlusOne, querytrace.WarnQueryInLoop},
		},
		{
			// AST analysis failed (keyword-fallback path, Parsed == false). Even
			// though the runtime fields would otherwise trip every analyzer
			// policy, the q.Parsed gate must keep all of them silent.
			name: "parse_failure_raises_no_analyzer_warnings",
			cfg:  analyzerCfg(),
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT * FROM accounts WHERE id IN (?, ?, ?, ?)",
					kind:   querytrace.StmtSelect,
					parsed: false,
					tables: nil, // unparsed: no tables, so tenant/soft-delete out of scope
					maxIn:  0,   // unparsed: MaxInList stays zero
					star:   false,
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{
				querytrace.WarnLargeInList, querytrace.WarnSelectStar,
				querytrace.WarnMissingTenantPredicate, querytrace.WarnMissingSoftDeletePredicate,
			},
		},
		{
			// A SELECT that carries both the tenant and soft-delete predicates.
			name: "tenant_and_soft_delete_predicates_present",
			cfg:  analyzerCfg(),
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT id FROM orders WHERE tenant_id = ? AND deleted_at IS NULL",
					kind:   querytrace.StmtSelect,
					parsed: true,
					tables: []string{"orders"},
					cols:   []string{"tenant_id", "deleted_at"},
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{
				querytrace.WarnMissingTenantPredicate, querytrace.WarnMissingSoftDeletePredicate,
			},
		},
		{
			// A qualified tenant column name ("o.tenant_id") must still match the
			// bare "tenant_id" requirement through BareColumn normalization.
			name: "qualified_tenant_column_matches",
			cfg:  querytrace.NewConfig(querytrace.WithTenantCheck(querytrace.Columns("o.tenant_id"))),
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT id FROM orders WHERE tenant_id = ?",
					kind:   querytrace.StmtSelect,
					parsed: true,
					tables: []string{"orders"},
					cols:   []string{"tenant_id"},
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{querytrace.WarnMissingTenantPredicate},
		},
		{
			// An INSERT carries its tenant value in the row, not a WHERE, so the
			// tenant/soft-delete predicate checks are out of scope and must not
			// fire even with no filter columns.
			name: "insert_is_out_of_scope_for_predicate_checks",
			cfg:  analyzerCfg(),
			events: []querytrace.Event{
				fpQuery{
					fp:     "INSERT INTO orders (tenant_id, total) VALUES (?, ?)",
					kind:   querytrace.StmtInsert,
					kindEv: querytrace.EventExec,
					parsed: true,
					tables: []string{"orders"},
					cols:   nil,
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{
				querytrace.WarnMissingTenantPredicate, querytrace.WarnMissingSoftDeletePredicate,
			},
		},
		{
			// A non-SELECT (UPDATE) is in scope for the tenant check but not for
			// the soft-delete check, which is SELECT-only. With the tenant column
			// present, no warning at all.
			name: "update_with_tenant_skips_soft_delete_check",
			cfg:  analyzerCfg(),
			events: []querytrace.Event{
				fpQuery{
					fp:     "UPDATE orders SET total = ? WHERE tenant_id = ?",
					kind:   querytrace.StmtUpdate,
					kindEv: querytrace.EventExec,
					parsed: true,
					tables: []string{"orders"},
					cols:   []string{"tenant_id"},
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{
				querytrace.WarnMissingTenantPredicate, querytrace.WarnMissingSoftDeletePredicate,
			},
		},
		{
			// An IN list and a parameter count both below their budgets.
			name: "in_list_and_params_below_budget",
			cfg:  querytrace.NewConfig(querytrace.WithMaxInList(5), querytrace.WithMaxParams(100)),
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT id FROM t WHERE x IN (?, ?, ?, ?)",
					kind:   querytrace.StmtSelect,
					parsed: true,
					tables: []string{"t"},
					maxIn:  4,  // one under the budget of 5
					params: 99, // one under the budget of 100
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{querytrace.WarnLargeInList, querytrace.WarnTooManyParameters},
		},
		{
			// A query count exactly at the budget (not over) must not warn.
			name: "query_count_at_budget",
			cfg:  querytrace.NewConfig(querytrace.WithMaxQueries(2)),
			events: []querytrace.Event{
				fpQuery{fp: "SELECT 1", kind: querytrace.StmtSelect}.event(),
				fpQuery{fp: "SELECT 2", kind: querytrace.StmtSelect}.event(),
			},
			wantAbsent: []querytrace.WarningCode{querytrace.WarnTooManyQueries},
		},
		{
			// A properly closed cursor must not raise rows-not-closed.
			name: "closed_rows_are_clean",
			cfg:  querytrace.NewConfig(),
			events: []querytrace.Event{
				fpQuery{
					fp:   "SELECT * FROM t",
					kind: querytrace.StmtSelect,
					rows: &querytrace.RowsInfo{NextCount: 10, Closed: true},
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{querytrace.WarnRowsNotClosed},
		},
		{
			// A clean cursor that was drained to completion (IterErr nil) must not
			// raise a context-cancel / deadline warning.
			name: "drained_cursor_no_ctx_warning",
			cfg:  querytrace.NewConfig(),
			events: []querytrace.Event{
				fpQuery{
					fp:   "SELECT * FROM t",
					kind: querytrace.StmtSelect,
					rows: &querytrace.RowsInfo{NextCount: 3, Closed: true, IterErr: nil},
				}.event(),
			},
			wantAbsent: []querytrace.WarningCode{
				querytrace.WarnQueryCanceled, querytrace.WarnQueryDeadlineExceeded,
			},
		},
		{
			// With N+1 detection disabled, even a clearly looping query is silent.
			name: "n_plus_one_disabled",
			cfg:  querytrace.NewConfig(querytrace.WithoutNPlusOne()),
			events: []querytrace.Event{
				fpQuery{fp: "SELECT * FROM t WHERE id = ?", kind: querytrace.StmtSelect, caller: siteA}.event(),
				fpQuery{fp: "SELECT * FROM t WHERE id = ?", kind: querytrace.StmtSelect, caller: siteA}.event(),
				fpQuery{fp: "SELECT * FROM t WHERE id = ?", kind: querytrace.StmtSelect, caller: siteA}.event(),
			},
			wantAbsent: []querytrace.WarningCode{querytrace.WarnPossibleNPlusOne, querytrace.WarnQueryInLoop},
		},
		{
			// An event with no Query (a mark-like or txn-less statement) must not
			// crash or warn the analyzer checks.
			name: "event_without_query_is_quiet",
			cfg:  analyzerCfg(),
			events: []querytrace.Event{
				{Kind: querytrace.EventQuery, Query: nil},
			},
			wantAbsent: nil, // no warnings at all
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace := querytrace.New("op", querytrace.WithConfig(tc.cfg))
			for _, ev := range tc.events {
				trace.RecordStatement(context.Background(), ev)
			}
			if len(tc.wantAbsent) == 0 {
				sqltest.Expect(t, trace).NoWarnings()
				return
			}
			for _, code := range tc.wantAbsent {
				sqltest.Expect(t, trace).NoWarn(code)
			}
		})
	}
}

// TestWarningsAreRecomputedFresh guards the derived-warning contract across
// every category at once: calling Warnings() and Report() repeatedly must return
// the same set, never accumulating or duplicating warnings.
func TestWarningsAreRecomputedFresh(t *testing.T) {
	cfg := querytrace.NewConfig(
		querytrace.WithNPlusOneThreshold(2),
		querytrace.WithMaxQueries(1),
		querytrace.WithMaxInList(2),
		querytrace.WithSelectStarWarning(),
	)
	trace := querytrace.New("op", querytrace.WithConfig(cfg))
	loop := at("svc.go", 9)
	for i := 0; i < 3; i++ {
		trace.RecordStatement(context.Background(), fpQuery{
			fp:     "SELECT * FROM t WHERE id IN (?, ?, ?)",
			kind:   querytrace.StmtSelect,
			parsed: true,
			tables: []string{"t"},
			maxIn:  3,
			star:   true,
			caller: loop,
			rows:   &querytrace.RowsInfo{Closed: false},
		}.event())
	}

	first := len(trace.Warnings())
	second := len(trace.Warnings())
	third := len(trace.Report().Warnings)
	if first == 0 {
		t.Fatal("expected warnings, got none")
	}
	if first != second || first != third {
		t.Fatalf("warning count not stable across calls: Warnings=%d then %d, Report=%d", first, second, third)
	}
}
