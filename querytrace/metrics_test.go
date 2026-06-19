package querytrace_test

import (
	"context"
	"testing"
	"time"

	"github.com/aita/sqlkit/analyze"
	querytrace "github.com/aita/sqlkit/querytrace"
)

// recordingRecorder captures emitted metrics keyed by name plus its labels, so
// tests can assert on the values without a real metrics backend.
type recordingRecorder struct {
	counters  map[string]int64
	durations map[string]time.Duration
}

func newRecordingRecorder() *recordingRecorder {
	return &recordingRecorder{counters: map[string]int64{}, durations: map[string]time.Duration{}}
}

func key(name string, labels []querytrace.Label) string {
	k := name
	for _, l := range labels {
		k += "|" + l.Key + "=" + l.Value
	}
	return k
}

func (r *recordingRecorder) Counter(name string, value int64, labels ...querytrace.Label) {
	r.counters[key(name, labels)] += value
}

func (r *recordingRecorder) Duration(name string, d time.Duration, labels ...querytrace.Label) {
	r.durations[key(name, labels)] += d
}

func TestMetricsAggregatesTrace(t *testing.T) {
	trace := querytrace.New("GET /users")
	ctx := context.Background()

	txID := trace.RecordTxBegin()
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventQuery,
		Duration: 10 * time.Millisecond,
		Query:    &querytrace.QueryInfo{Analysis: analyze.Analysis{Fingerprint: "SELECT * FROM users", Kind: querytrace.StmtSelect}},
		Rows:     &querytrace.RowsInfo{IterationDuration: 4 * time.Millisecond},
	})
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventQuery,
		Duration: 5 * time.Millisecond,
		Query:    &querytrace.QueryInfo{Analysis: analyze.Analysis{Fingerprint: "SELECT * FROM users", Kind: querytrace.StmtSelect}},
	})
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventExec,
		Duration: 7 * time.Millisecond,
		Query:    &querytrace.QueryInfo{Analysis: analyze.Analysis{Fingerprint: "INSERT INTO users", Kind: querytrace.StmtInsert}},
	})
	trace.RecordTxEnd(querytrace.TxCommit, txID)

	m := trace.Metrics()

	if m.Operation != "GET /users" {
		t.Fatalf("Operation = %q", m.Operation)
	}
	if m.QueryCount != 3 || m.WriteCount != 1 {
		t.Fatalf("QueryCount=%d WriteCount=%d, want 3 and 1", m.QueryCount, m.WriteCount)
	}
	if m.UniqueFingerprints != 2 {
		t.Fatalf("UniqueFingerprints = %d, want 2", m.UniqueFingerprints)
	}
	if m.TransactionCount != 1 {
		t.Fatalf("TransactionCount = %d, want 1", m.TransactionCount)
	}
	if want := 22 * time.Millisecond; m.TotalQueryDuration != want {
		t.Fatalf("TotalQueryDuration = %v, want %v", m.TotalQueryDuration, want)
	}
	if want := 4 * time.Millisecond; m.TotalRowsIterationDuration != want {
		t.Fatalf("TotalRowsIterationDuration = %v, want %v", m.TotalRowsIterationDuration, want)
	}
	if m.ByStatement[querytrace.StmtSelect] != 2 || m.ByStatement[querytrace.StmtInsert] != 1 {
		t.Fatalf("ByStatement = %v", m.ByStatement)
	}
}

func TestMetricsEmit(t *testing.T) {
	trace := querytrace.New("GET /users")
	ctx := context.Background()
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventQuery,
		Duration: 3 * time.Millisecond,
		Query:    &querytrace.QueryInfo{Analysis: analyze.Analysis{Fingerprint: "SELECT 1", Kind: querytrace.StmtSelect}},
	})

	rec := newRecordingRecorder()
	trace.RecordMetrics(rec)

	op := []querytrace.Label{{Key: "operation", Value: "GET /users"}}
	selectKey := key(querytrace.MetricStatements, append(append([]querytrace.Label{}, op...), querytrace.Label{Key: "kind", Value: "select"}))
	if got := rec.counters[selectKey]; got != 1 {
		t.Fatalf("statements{kind=select} = %d, want 1", got)
	}
	if got := rec.counters[key(querytrace.MetricUniqueFingerprint, op)]; got != 1 {
		t.Fatalf("unique_fingerprints = %d, want 1", got)
	}
	if got := rec.durations[key(querytrace.MetricQueryDuration, op)]; got != 3*time.Millisecond {
		t.Fatalf("query_duration = %v, want 3ms", got)
	}
}

func TestMetricsEmitNoOperationOmitsLabel(t *testing.T) {
	trace := querytrace.New("")
	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtSelect}},
	})

	rec := newRecordingRecorder()
	trace.Metrics().Emit(rec)

	// With no operation, the counter carries only the kind label.
	noOpKey := key(querytrace.MetricStatements, []querytrace.Label{{Key: "kind", Value: "select"}})
	if got := rec.counters[noOpKey]; got != 1 {
		t.Fatalf("statements{kind=select} (no operation) = %d, want 1", got)
	}
}
