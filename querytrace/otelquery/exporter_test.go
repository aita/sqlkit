package otelquery_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/aita/sqlkit/analysis"
	qt "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/querytrace/otelquery"
)

// recorder returns an Exporter wired to an in-memory SpanRecorder and the
// recorder itself, so a test can export a trace and inspect the spans.
func recorder(t *testing.T, opts ...otelquery.Option) (*otelquery.Exporter, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	return otelquery.New(tp.Tracer("querytrace"), opts...), sr
}

func queryEvent() qt.Event {
	return qt.Event{
		Kind: qt.EventQuery,
		Query: &qt.QueryInfo{
			Analysis: analysis.Analysis{
				Kind:        qt.StmtSelect,
				Tables:      []string{"users"},
				Fingerprint: "SELECT * FROM users WHERE id = ?",
			},
			RawSQL:     "SELECT * FROM users WHERE id = 42",
			ParamCount: 1,
		},
	}
}

func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func attr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestExportTraceSpanTree(t *testing.T) {
	exp, sr := recorder(t, otelquery.WithDBSystem("postgresql"))

	trace := qt.New("GET /users")
	trace.RecordStatement(context.Background(), queryEvent())
	exp.ExportTrace(context.Background(), trace)

	spans := sr.Ended()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (root + statement), got %d", len(spans))
	}

	root := findSpan(spans, "GET /users")
	if root == nil {
		t.Fatal("operation span not found")
	}
	if v, ok := attr(root, "querytrace.query_count"); !ok || v.AsInt64() != 1 {
		t.Errorf("root querytrace.query_count = %v (ok=%v), want 1", v.AsInt64(), ok)
	}

	stmt := findSpan(spans, "SELECT users")
	if stmt == nil {
		t.Fatalf("statement span not found; spans: %v", spanNames(spans))
	}
	if stmt.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Error("statement span is not a child of the operation span")
	}
	for key, want := range map[string]string{
		"db.system.name":     "postgresql",
		"db.operation.name":  "SELECT",
		"db.collection.name": "users",
		"db.query.text":      "SELECT * FROM users WHERE id = 42",
	} {
		if v, ok := attr(stmt, key); !ok || v.AsString() != want {
			t.Errorf("statement %s = %q (ok=%v), want %q", key, v.AsString(), ok, want)
		}
	}
	if v, ok := attr(stmt, "querytrace.query.param_count"); !ok || v.AsInt64() != 1 {
		t.Errorf("statement param_count = %v (ok=%v), want 1", v.AsInt64(), ok)
	}
}

func TestExportTraceWarningAsEvent(t *testing.T) {
	exp, sr := recorder(t)

	trace := qt.New("CacheHit")
	ctx := qt.ForbidQueries(qt.WithTrace(context.Background(), trace))
	trace.RecordStatement(ctx, queryEvent())
	exp.ExportTrace(context.Background(), trace)

	stmt := findSpan(sr.Ended(), "SELECT users")
	if stmt == nil {
		t.Fatal("statement span not found")
	}
	var found bool
	for _, e := range stmt.Events() {
		if e.Name != "querytrace.warning" {
			continue
		}
		for _, kv := range e.Attributes {
			if string(kv.Key) == "querytrace.warning.code" && kv.Value.AsString() == string(qt.WarnQueryForbidden) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected a query_forbidden warning span event; events: %v", stmt.Events())
	}
	if stmt.Status().Code == codes.Error {
		t.Error("statement span should not be Error without WithWarningsAsErrors")
	}
}

func TestExportTraceWarningsAsErrors(t *testing.T) {
	exp, sr := recorder(t, otelquery.WithWarningsAsErrors())

	trace := qt.New("CacheHit")
	ctx := qt.ForbidQueries(qt.WithTrace(context.Background(), trace))
	trace.RecordStatement(ctx, queryEvent())
	exp.ExportTrace(context.Background(), trace)

	stmt := findSpan(sr.Ended(), "SELECT users")
	if stmt == nil {
		t.Fatal("statement span not found")
	}
	if stmt.Status().Code != codes.Error {
		t.Errorf("statement span status = %v, want Error", stmt.Status().Code)
	}
}

// TestExportTraceLabelAttributesAreDeterministic covers ev.Labels/mark Attrs,
// both Go maps: building their span attributes by ranging the map directly
// produced a different attribute order across otherwise-identical runs. Order
// doesn't matter to OTel itself, but repeats the operation many times (per
// SKILL.md's determinism guidance) so a single lucky iteration order can't
// mask the bug the way one assertion would.
func TestExportTraceLabelAttributesAreDeterministic(t *testing.T) {
	labelOrder := func() []string {
		exp, sr := recorder(t)
		trace := qt.New("op")
		ev := queryEvent()
		ev.Labels = map[string]string{"f": "1", "e": "2", "d": "3", "c": "4", "b": "5", "a": "6"}
		trace.RecordStatement(context.Background(), ev)
		exp.ExportTrace(context.Background(), trace)

		stmt := findSpan(sr.Ended(), "SELECT users")
		if stmt == nil {
			t.Fatal("statement span not found")
		}
		var order []string
		for _, kv := range stmt.Attributes() {
			if strings.HasPrefix(string(kv.Key), "querytrace.label.") {
				order = append(order, string(kv.Key))
			}
		}
		return order
	}
	first := labelOrder()
	for i := 0; i < 100; i++ {
		if got := labelOrder(); !slices.Equal(got, first) {
			t.Fatalf("label attribute order varies across runs: %v vs %v", first, got)
		}
	}
}

func TestExportTraceEmpty(t *testing.T) {
	exp, sr := recorder(t)
	exp.ExportTrace(context.Background(), qt.New("empty"))

	spans := sr.Ended()
	if len(spans) != 1 || spans[0].Name() != "empty" {
		t.Fatalf("expected a single operation span, got %v", spanNames(spans))
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name()
	}
	return names
}
