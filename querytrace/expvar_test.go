package querytrace_test

import (
	"context"
	"expvar"
	"testing"
	"time"

	"github.com/aita/sqlkit/analyze"
	querytrace "github.com/aita/sqlkit/querytrace"
)

func TestExpvarRecorder(t *testing.T) {
	// A standalone, unpublished map keeps the global /debug/vars registry clean
	// across test runs (publishing the same name twice panics).
	root := new(expvar.Map).Init()
	rec := querytrace.NewExpvarRecorder(root)

	trace := querytrace.New("GET /users")
	ctx := context.Background()
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventQuery,
		Duration: 10 * time.Millisecond,
		Query:    &querytrace.QueryInfo{Analysis: analyze.Analysis{Fingerprint: "SELECT 1", Kind: querytrace.StmtSelect}},
	})
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventQuery,
		Duration: 5 * time.Millisecond,
		Query:    &querytrace.QueryInfo{Analysis: analyze.Analysis{Fingerprint: "SELECT 1", Kind: querytrace.StmtSelect}},
	})
	rec.Record(trace)

	// Labels are folded into the name, sorted by key.
	if got := varString(root, "querytrace.statements{kind=select,operation=GET /users}"); got != "2" {
		t.Fatalf("statements{kind=select} = %q, want 2", got)
	}
	// Duration accumulates as a total in seconds (15ms = 0.015s).
	if got := varString(root, "querytrace.query_duration{operation=GET /users}"); got != "0.015" {
		t.Fatalf("query_duration = %q, want 0.015", got)
	}

	// A second trace accumulates into the same vars.
	rec.Record(trace)
	if got := varString(root, "querytrace.statements{kind=select,operation=GET /users}"); got != "4" {
		t.Fatalf("statements after second record = %q, want 4", got)
	}
}

func varString(root *expvar.Map, name string) string {
	v := root.Get(name)
	if v == nil {
		return "<nil>"
	}
	return v.String()
}
