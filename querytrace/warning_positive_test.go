package querytrace_test

import (
	"context"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sqltest"
)

// This file is the positive corpus: the mirror of the negative one. Each case is
// a genuine problem that MUST be detected, asserting the specific WarningCode is
// present. Together with warning_falsepositive_test.go it forms a two-way labeled
// corpus that pins both false positives and false negatives.

// posCase is one offending trace and the code it must raise.
type posCase struct {
	name   string
	cfg    querytrace.Config
	events []querytrace.Event
	want   querytrace.WarningCode
}

func TestNoFalseNegatives(t *testing.T) {
	siteA := at("a.go", 11)
	siteB := at("b.go", 22)
	siteC := at("c.go", 33)
	loop := at("svc.go", 44)

	cases := []posCase{
		{
			// A real cross-caller N+1: the same shape issued from three distinct
			// call sites (the fan-out signature).
			name: "cross_caller_n_plus_one",
			cfg:  nplusOneConfig(3),
			events: []querytrace.Event{
				fpQuery{fp: "SELECT * FROM users WHERE id = ?", kind: querytrace.StmtSelect, caller: siteA}.event(),
				fpQuery{fp: "SELECT * FROM users WHERE id = ?", kind: querytrace.StmtSelect, caller: siteB}.event(),
				fpQuery{fp: "SELECT * FROM users WHERE id = ?", kind: querytrace.StmtSelect, caller: siteC}.event(),
			},
			want: querytrace.WarnPossibleNPlusOne,
		},
		{
			// A same-caller loop: the same shape repeated from one call site.
			name: "same_caller_loop",
			cfg:  nplusOneConfig(3),
			events: []querytrace.Event{
				fpQuery{fp: "SELECT * FROM comments WHERE post_id = ?", kind: querytrace.StmtSelect, caller: loop}.event(),
				fpQuery{fp: "SELECT * FROM comments WHERE post_id = ?", kind: querytrace.StmtSelect, caller: loop}.event(),
				fpQuery{fp: "SELECT * FROM comments WHERE post_id = ?", kind: querytrace.StmtSelect, caller: loop}.event(),
			},
			want: querytrace.WarnQueryInLoop,
		},
		{
			// Over the query budget (3 > 2).
			name: "over_query_budget",
			cfg:  querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{MaxQueries: 2}},
			events: []querytrace.Event{
				fpQuery{fp: "SELECT 1", kind: querytrace.StmtSelect}.event(),
				fpQuery{fp: "SELECT 2", kind: querytrace.StmtSelect}.event(),
				fpQuery{fp: "SELECT 3", kind: querytrace.StmtSelect}.event(),
			},
			want: querytrace.WarnTooManyQueries,
		},
		{
			// An unclosed cursor.
			name: "unclosed_cursor",
			cfg:  querytrace.Config{Enabled: true},
			events: []querytrace.Event{
				fpQuery{
					fp:   "SELECT * FROM t",
					kind: querytrace.StmtSelect,
					rows: &querytrace.RowsInfo{NextCount: 2, Closed: false},
				}.event(),
			},
			want: querytrace.WarnRowsNotClosed,
		},
		{
			// A large IN list at the budget (4 >= 4).
			name: "large_in_list",
			cfg:  querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{MaxInListLen: 4}},
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT id FROM t WHERE x IN (?, ?, ?, ?)",
					kind:   querytrace.StmtSelect,
					parsed: true,
					tables: []string{"t"},
					maxIn:  4,
				}.event(),
			},
			want: querytrace.WarnLargeInList,
		},
		{
			// A SELECT * projection.
			name: "select_star",
			cfg:  querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{SelectStar: true}},
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT * FROM t",
					kind:   querytrace.StmtSelect,
					parsed: true,
					tables: []string{"t"},
					star:   true,
				}.event(),
			},
			want: querytrace.WarnSelectStar,
		},
		{
			// A SELECT touching a base table with no tenant predicate.
			name: "missing_tenant_predicate",
			cfg: querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{
				RequireTenantColumns: []string{"tenant_id"},
			}},
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT id FROM orders WHERE total > ?",
					kind:   querytrace.StmtSelect,
					parsed: true,
					tables: []string{"orders"},
					cols:   []string{"total"},
				}.event(),
			},
			want: querytrace.WarnMissingTenantPredicate,
		},
		{
			// A SELECT touching a base table with no soft-delete predicate.
			name: "missing_soft_delete_predicate",
			cfg: querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{
				SoftDeleteColumns: []string{"deleted_at"},
			}},
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT id FROM orders WHERE id = ?",
					kind:   querytrace.StmtSelect,
					parsed: true,
					tables: []string{"orders"},
					cols:   []string{"id"},
				}.event(),
			},
			want: querytrace.WarnMissingSoftDeletePredicate,
		},
		{
			// Over the bind-parameter budget (at the budget, 100 >= 100).
			name: "over_bind_param_budget",
			cfg:  querytrace.Config{Enabled: true, Warnings: querytrace.WarningConfig{MaxParams: 100}},
			events: []querytrace.Event{
				fpQuery{
					fp:     "SELECT id FROM t WHERE x IN (?)",
					kind:   querytrace.StmtSelect,
					params: 100,
				}.event(),
			},
			want: querytrace.WarnTooManyParameters,
		},
		{
			// Context canceled mid-iteration.
			name: "context_canceled_during_iteration",
			cfg:  querytrace.Config{Enabled: true},
			events: []querytrace.Event{
				fpQuery{
					fp:   "SELECT * FROM t",
					kind: querytrace.StmtSelect,
					rows: &querytrace.RowsInfo{NextCount: 4, Closed: true, IterErr: context.Canceled},
				}.event(),
			},
			want: querytrace.WarnQueryCanceled,
		},
		{
			// Context deadline exceeded mid-iteration.
			name: "context_deadline_during_iteration",
			cfg:  querytrace.Config{Enabled: true},
			events: []querytrace.Event{
				fpQuery{
					fp:   "SELECT * FROM t",
					kind: querytrace.StmtSelect,
					rows: &querytrace.RowsInfo{NextCount: 1, Closed: true, IterErr: context.DeadlineExceeded},
				}.event(),
			},
			want: querytrace.WarnQueryDeadlineExceeded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace := querytrace.New("op", querytrace.WithConfig(tc.cfg))
			for _, ev := range tc.events {
				trace.RecordStatement(context.Background(), ev)
			}
			sqltest.Expect(t, trace).Warns(tc.want)
		})
	}
}
