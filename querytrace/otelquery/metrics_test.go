package otelquery_test

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/aita/sqlkit/analysis"
	qt "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/querytrace/otelquery"
)

func metricsTrace() *qt.Trace {
	trace := qt.New("GET /users")
	ctx := context.Background()
	txID := trace.RecordTxBegin()
	trace.RecordStatement(ctx, qt.Event{
		Kind:     qt.EventQuery,
		Duration: 10 * time.Millisecond,
		Query:    &qt.QueryInfo{Analysis: analysis.Analysis{Fingerprint: "SELECT * FROM users", Kind: qt.StmtSelect}},
	})
	trace.RecordStatement(ctx, qt.Event{
		Kind:     qt.EventQuery,
		Duration: 5 * time.Millisecond,
		Query:    &qt.QueryInfo{Analysis: analysis.Analysis{Fingerprint: "SELECT * FROM users", Kind: qt.StmtSelect}},
	})
	trace.RecordStatement(ctx, qt.Event{
		Kind:     qt.EventExec,
		Duration: 7 * time.Millisecond,
		Query:    &qt.QueryInfo{Analysis: analysis.Analysis{Fingerprint: "INSERT INTO users", Kind: qt.StmtInsert}},
	})
	trace.RecordTxEnd(qt.TxCommit, txID)
	return trace
}

func collect(t *testing.T, fn func(provider *sdkmetric.MeterProvider)) metricdata.ResourceMetrics {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	fn(provider)
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return rm
}

func findMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

func TestExportMetrics(t *testing.T) {
	rm := collect(t, func(provider *sdkmetric.MeterProvider) {
		if err := otelquery.ExportMetrics(context.Background(), provider.Meter("querytrace"), metricsTrace()); err != nil {
			t.Fatalf("ExportMetrics() error = %v", err)
		}
	})

	// querytrace.statements is an Int64 sum, one data point per kind.
	stmts, ok := findMetric(rm, qt.MetricStatements)
	if !ok {
		t.Fatalf("metric %q not exported", qt.MetricStatements)
	}
	sum, ok := stmts.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%q data = %T, want Sum[int64]", qt.MetricStatements, stmts.Data)
	}
	byKind := map[string]int64{}
	var total int64
	for _, dp := range sum.DataPoints {
		total += dp.Value
		if v, present := dp.Attributes.Value("kind"); present {
			byKind[v.AsString()] = dp.Value
		}
	}
	if total != 3 {
		t.Fatalf("statements total = %d, want 3", total)
	}
	if byKind["select"] != 2 || byKind["insert"] != 1 {
		t.Fatalf("statements by kind = %v, want select=2 insert=1", byKind)
	}

	// querytrace.query_duration is a histogram with one observation (the trace's
	// total), recorded in seconds.
	dur, ok := findMetric(rm, qt.MetricQueryDuration)
	if !ok {
		t.Fatalf("metric %q not exported", qt.MetricQueryDuration)
	}
	hist, ok := dur.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("%q data = %T, want Histogram[float64]", qt.MetricQueryDuration, dur.Data)
	}
	if len(hist.DataPoints) != 1 {
		t.Fatalf("query_duration data points = %d, want 1", len(hist.DataPoints))
	}
	if got := hist.DataPoints[0]; got.Count != 1 || got.Sum <= 0 {
		t.Fatalf("query_duration = {count:%d sum:%v}, want count 1 and a positive sum", got.Count, got.Sum)
	}
	if dur.Unit != "s" {
		t.Fatalf("query_duration unit = %q, want \"s\"", dur.Unit)
	}
}
