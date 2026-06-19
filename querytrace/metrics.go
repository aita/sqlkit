package querytrace

import "time"

// Metrics is the numeric aggregate of a Trace, ready to emit to any metrics
// backend. It is a plain value — compute it with Trace.Metrics, inspect it, or
// push it through a MetricsRecorder with Emit. It deliberately depends on
// nothing but the standard library, so an application wires it to Prometheus,
// OpenTelemetry metrics, statsd, or its own counters by implementing the
// two-method MetricsRecorder; querytrace does not pull in an instrumentation
// library.
type Metrics struct {
	// Operation is the unit of work the trace covers, the natural metric label.
	Operation string

	// QueryCount is the number of read and write statements.
	QueryCount int
	// WriteCount is the number of write statements (INSERT/UPDATE/DELETE).
	WriteCount int
	// UniqueFingerprints is the number of distinct query shapes.
	UniqueFingerprints int
	// TransactionCount is the number of transactions begun.
	TransactionCount int

	// TotalQueryDuration is the summed time statements took to return.
	TotalQueryDuration time.Duration
	// TotalRowsIterationDuration is the summed time spent iterating row cursors.
	TotalRowsIterationDuration time.Duration

	// ByStatement counts statements per kind (select/insert/update/delete/ddl).
	ByStatement map[StmtKind]int
	// Warnings counts warnings per code (n+1, query budget, select star, ...).
	Warnings map[WarningCode]int
	// WarningsBySeverity counts warnings per severity.
	WarningsBySeverity map[Severity]int
}

// Label is a metric dimension, a key/value pair attached to an emitted metric.
type Label struct {
	Key   string
	Value string
}

// MetricsRecorder is the sink Metrics.Emit pushes to: an adapter over a real
// metrics backend. Counter adds a delta to a named counter and Duration records
// a duration observation (a histogram/summary sample), each tagged with zero or
// more labels. Implement it over prometheus.CounterVec/HistogramVec, an
// OpenTelemetry meter, or any other backend.
type MetricsRecorder interface {
	Counter(name string, value int64, labels ...Label)
	Duration(name string, d time.Duration, labels ...Label)
}

// Metric names emitted by Metrics.Emit. They are stable strings so dashboards
// and alerts can rely on them.
const (
	MetricStatements        = "querytrace.statements"              // counter, label "kind"
	MetricUniqueFingerprint = "querytrace.unique_fingerprints"     // counter
	MetricTransactions      = "querytrace.transactions"            // counter
	MetricWarnings          = "querytrace.warnings"                // counter, label "code"
	MetricQueryDuration     = "querytrace.query_duration"          // duration
	MetricRowsIteration     = "querytrace.rows_iteration_duration" // duration
)

// Metrics computes the aggregate for the Trace.
func (t *Trace) Metrics() Metrics {
	return MetricsFromReport(t.Report())
}

// RecordMetrics computes the Trace's Metrics and emits them to rec. It is the
// one-call form of t.Metrics().Emit(rec), typically deferred alongside the unit
// of work the way otelquery.ExportTrace is.
func (t *Trace) RecordMetrics(rec MetricsRecorder) {
	t.Metrics().Emit(rec)
}

// MetricsFromReport derives Metrics from an already-built Report, for callers
// that produce the Report for other purposes too.
func MetricsFromReport(r Report) Metrics {
	m := Metrics{
		Operation:                  r.Operation,
		QueryCount:                 r.QueryCount,
		WriteCount:                 r.WriteCount,
		UniqueFingerprints:         r.UniqueFingerprintCount,
		TotalQueryDuration:         r.TotalQueryDuration,
		TotalRowsIterationDuration: r.TotalRowsIterationDuration,
		ByStatement:                map[StmtKind]int{},
		Warnings:                   map[WarningCode]int{},
		WarningsBySeverity:         map[Severity]int{},
	}
	for _, ev := range r.Events {
		switch ev.Kind {
		case EventQuery, EventExec:
			if ev.Query != nil {
				m.ByStatement[ev.Query.Operation]++
			}
		case EventTx:
			if ev.Tx != nil && ev.Tx.Action == TxBegin {
				m.TransactionCount++
			}
		}
	}
	for _, w := range r.Warnings {
		m.Warnings[w.Code]++
		m.WarningsBySeverity[w.Severity]++
	}
	return m
}

// Emit pushes the metrics to rec under stable names (the Metric* constants),
// tagging each with the trace's operation when set. Statements are emitted per
// kind and warnings per code, so a backend can break them down.
func (m Metrics) Emit(rec MetricsRecorder) {
	base := func(extra ...Label) []Label {
		if m.Operation == "" {
			return extra
		}
		return append([]Label{{Key: "operation", Value: m.Operation}}, extra...)
	}

	for kind, n := range m.ByStatement {
		rec.Counter(MetricStatements, int64(n), base(Label{Key: "kind", Value: kind.String()})...)
	}
	rec.Counter(MetricUniqueFingerprint, int64(m.UniqueFingerprints), base()...)
	rec.Counter(MetricTransactions, int64(m.TransactionCount), base()...)
	for code, n := range m.Warnings {
		rec.Counter(MetricWarnings, int64(n), base(Label{Key: "code", Value: string(code)})...)
	}
	rec.Duration(MetricQueryDuration, m.TotalQueryDuration, base()...)
	rec.Duration(MetricRowsIteration, m.TotalRowsIterationDuration, base()...)
}
